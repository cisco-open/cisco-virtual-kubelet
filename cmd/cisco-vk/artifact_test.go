// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/appartifact"
)

func TestVerifyLocalArtifactPackage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "package.tar")
	data := []byte("exact prepared package bytes")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	expected := appartifact.Package{Digest: appartifact.Digest(data), SizeBytes: int64(len(data))}
	if err := verifyLocalArtifactPackage(path, expected); err != nil {
		t.Fatal(err)
	}
	data[0] ^= 1
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyLocalArtifactPackage(path, expected); err == nil {
		t.Fatal("accepted same-size tampered package")
	}
	expected.SizeBytes++
	if err := verifyLocalArtifactPackage(path, expected); err == nil {
		t.Fatal("accepted wrong package size")
	}
	if err := verifyLocalArtifactPackage(filepath.Dir(path), expected); err == nil {
		t.Fatal("accepted directory")
	}
}

func TestArtifactVerifyRequiresExplicitTrustAndIdentity(t *testing.T) {
	command := newArtifactVerifyCommand()
	command.SetArgs(nil)
	if err := command.Execute(); err == nil {
		t.Fatal("accepted missing explicit trust and identity flags")
	}
	command = newArtifactVerifyCommand()
	command.SetArgs([]string{"extra"})
	if err := command.Execute(); err == nil {
		t.Fatal("accepted positional argument")
	}
}

func TestArtifactCAFileRejectsInvalidInput(t *testing.T) {
	if config, err := loadArtifactTLSConfig(""); config != nil || err != nil {
		t.Fatalf("omitted CA file should use system roots: %v", err)
	}
	path := filepath.Join(t.TempDir(), "invalid-ca.pem")
	if err := os.WriteFile(path, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadArtifactTLSConfig(path); err == nil {
		t.Fatal("accepted invalid CA file")
	}
}
