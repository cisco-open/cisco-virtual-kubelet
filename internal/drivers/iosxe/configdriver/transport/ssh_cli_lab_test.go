// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build lab

package transport

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh/knownhosts"
)

// This opt-in hardware test sends only fixed read-only commands. Credentials
// arrive on stdin, never command-line arguments, logs or a checked-in file.
// A pre-existing trusted known_hosts file is mandatory; no TOFU or insecure
// host-key fallback is used. Missing configuration fails rather than skips.
func TestLabSSHDiagnosticRejection(t *testing.T) {
	if os.Getenv("CVK_RUN_SSH_DIAGNOSTIC_LAB") != "1" {
		t.Fatal("set CVK_RUN_SSH_DIAGNOSTIC_LAB=1 and supply connection JSON on stdin")
	}
	var input struct {
		Address, Username, Password, KnownHostsFile string
	}
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal("invalid connection JSON on stdin")
	}
	if input.Address == "" || input.Username == "" || input.Password == "" || input.KnownHostsFile == "" {
		t.Fatal("address, username, password and known-hosts path are required")
	}
	hostKey, err := knownhosts.New(input.KnownHostsFile)
	if err != nil {
		t.Fatal("cannot load trusted known-hosts file")
	}
	commands := []string{"show version", "show cvk-readiness-nonexistent", "show app-hosting list"}
	results, err := runShowCommandsViaSSH(sshCLIConfig{
		Address: input.Address, Username: input.Username, Password: input.Password,
		Timeout: 30 * time.Second, HostKeyCallback: hostKey,
	}, commands)
	if err != nil {
		t.Fatalf("read-only diagnostic transport: %v", err)
	}
	if len(results) != len(commands) {
		t.Fatalf("got %d results for %d commands", len(results), len(commands))
	}
	if results[0].Err != "" || !strings.Contains(results[0].Output, "Cisco IOS XE Software") {
		t.Fatal("positive show-version control failed")
	}
	if results[1].Err != "IOS XE rejected diagnostic command syntax" {
		t.Fatal("unsupported diagnostic was not classified as a parser rejection")
	}
	if results[2].Err != "" || strings.TrimSpace(results[2].Output) == "" {
		t.Fatal("command after rejection did not return a usable result")
	}
	for i, result := range results {
		if result.Command != commands[i] {
			t.Fatal("diagnostic result lost command correlation")
		}
	}
	t.Log("PASS: show version accepted; unsupported syntax rejected; subsequent app inventory accepted; trusted SSH host key verified")
}
