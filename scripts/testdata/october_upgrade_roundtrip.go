// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

// Built with the pinned October go.mod and API source, never current types.
// This reproduces the released typed client's lossy JSON round trip without
// giving that process cluster credentials or any device access.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

func main() {
	var upgrade ops.IOSXESoftwareUpgrade
	if err := json.NewDecoder(os.Stdin).Decode(&upgrade); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	upgrade.Status.Message = "released writer round-trip probe"
	if err := json.NewEncoder(os.Stdout).Encode(&upgrade); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
