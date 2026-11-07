// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"fmt"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/sdwan"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: cvk-sdwan-resolve <netascode.yaml>")
		os.Exit(2)
	}
	data, e := os.ReadFile(os.Args[1])
	if e != nil {
		fmt.Fprintln(os.Stderr, "cannot read NetAsCode input")
		os.Exit(1)
	}
	resolved, e := sdwan.Resolve(data)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	fmt.Println(string(resolved))
}
