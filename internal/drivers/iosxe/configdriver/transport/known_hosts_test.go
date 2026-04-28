// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package transport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestLoadKnownHostsCallback_EmptyPath(t *testing.T) {
	t.Parallel()
	_, err := LoadKnownHostsCallback("")
	if err == nil {
		t.Fatal("expected error on empty path")
	}
	if !strings.Contains(err.Error(), "path is empty") {
		t.Errorf("error message missing context: %v", err)
	}
}

func TestLoadKnownHostsCallback_MissingFile(t *testing.T) {
	t.Parallel()
	_, err := LoadKnownHostsCallback("/nonexistent/known_hosts")
	if err == nil {
		t.Fatal("expected error on missing file")
	}
}

func TestLoadKnownHostsCallback_ValidFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")

	// Generate a valid OpenSSH host key inline so the test doesn't
	// depend on a hardcoded base64 string that may not pass the
	// strict knownhosts parser. ECDSA P-256 chosen for compactness.
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("ssh public key: %v", err)
	}
	authorized := ssh.MarshalAuthorizedKey(pub) // includes trailing \n
	contents := fmt.Sprintf("198.51.100.103 %s", string(authorized))
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cb, err := LoadKnownHostsCallback(path)
	if err != nil {
		t.Fatalf("LoadKnownHostsCallback: %v", err)
	}
	if cb == nil {
		t.Fatal("got nil callback")
	}
}

func TestLoadKnownHostsCallback_MalformedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	// Garbage bytes — not a valid known_hosts entry.
	if err := os.WriteFile(path, []byte("not-a-known-hosts-file\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadKnownHostsCallback(path)
	if err == nil {
		t.Fatal("expected error on malformed file")
	}
}
