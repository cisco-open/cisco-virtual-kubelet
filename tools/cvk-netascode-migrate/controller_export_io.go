// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func readControllerExportInput(path string, fallback io.Reader) ([]byte, error) {
	input := fallback
	if path != "" && path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		input = file
	}
	if input == nil {
		return nil, io.ErrUnexpectedEOF
	}
	raw, err := io.ReadAll(io.LimitReader(input, controllerInputLimit+1))
	if err != nil || len(raw) > controllerInputLimit {
		return nil, io.ErrUnexpectedEOF
	}
	return raw, nil
}

// Never write model contents to stdout or overwrite an existing file/symlink.
func writeControllerExport(output string, resolved []byte, meta any, stdout, stderr io.Writer) int {
	report, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "ERROR: cannot encode provenance report")
		return 1
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		fmt.Fprintln(stderr, "ERROR: cannot create a new private output file; existing paths are never overwritten")
		return 1
	}
	original, _ := file.Stat()
	_, writeErr := file.Write(resolved)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		if current, statErr := os.Stat(output); statErr == nil && original != nil && os.SameFile(original, current) {
			_ = os.Remove(output)
		}
		fmt.Fprintln(stderr, "ERROR: cannot finish private output file")
		return 1
	}
	if _, err := stdout.Write(append(report, '\n')); err != nil {
		fmt.Fprintln(stderr, "ERROR: cannot write provenance report; private output was created")
		return 1
	}
	return 0
}
