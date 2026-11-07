//go:build linux || darwin

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/appartifact"
	"golang.org/x/sys/unix"
)

func TestArtifactFilesRejectNamedPipesBeforeOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	for _, run := range []func() error{
		func() error { _, err := readArtifactPublicKey(path); return err },
		func() error { _, err := loadArtifactTLSConfig(path); return err },
		func() error {
			return verifyLocalArtifactPackage(path, appartifact.Package{Digest: appartifact.Digest([]byte("x")), SizeBytes: 1})
		},
	} {
		done := make(chan error, 1)
		go func() { done <- run() }()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("accepted named pipe")
			}
		case <-time.After(time.Second):
			t.Fatal("blocked opening named pipe")
		}
	}
}
