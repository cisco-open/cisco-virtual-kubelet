// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");

// cvk-preparation-plan produces an offline, observation-only cleanup report.
// It never connects to a device, reads credentials or submits Kubernetes actions.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
)

func main() { os.Exit(run(os.Stdin, os.Stdout, os.Stderr, time.Now())) }

// Exit 0: supplied evidence supports the estimate; 2: blocked; 1: bad input.
// Exit 0 is not execution authorization and does not signify SWIM readiness.
func run(input io.Reader, output, diagnostics io.Writer, now time.Time) int {
	const limit = 8 << 20
	data, err := io.ReadAll(io.LimitReader(input, limit+1))
	if err != nil || len(data) > limit {
		fmt.Fprintln(diagnostics, "cannot read evidence: input must be <= 8 MiB")
		return 1
	}
	// Reject duplicate keys too: normal encoding/json silently keeps the last
	// value, which is unsuitable for reviewable policy/evidence documents.
	if err := rejectDuplicateKeys(data); err != nil {
		fmt.Fprintln(diagnostics, err)
		return 1
	}
	var in softwarelifecycle.PreparationPlanInput
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		fmt.Fprintln(diagnostics, "invalid evidence:", err)
		return 1
	}
	if decoder.Decode(new(any)) != io.EOF {
		fmt.Fprintln(diagnostics, "expected one JSON document")
		return 1
	}
	p := softwarelifecycle.PlanPreparation(in, now)
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(p); err != nil {
		fmt.Fprintln(diagnostics, "cannot write report:", err)
		return 1
	}
	if len(p.Blockers) != 0 {
		return 2
	}
	return 0
}

func rejectDuplicateKeys(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	var value func() error
	value = func() error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("duplicate or invalid JSON key")
				}
				seen[name] = true
				if err := value(); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("expected one JSON document")
	}
	return nil
}
