// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package transport

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// LoadKnownHostsCallback returns an ssh.HostKeyCallback backed by a
// known_hosts file at `path`. Production NETCONF deployments wire
// this into NETCONFConfig.HostKeyCallback so the SSH dial pins
// against a known set of device public keys instead of accepting
// any presented key (the lab default).
//
// Format is the standard OpenSSH known_hosts file (one entry per
// line: `host[,host2 …] keytype base64-key`). Operators ship the
// file via a Kubernetes Secret or ConfigMap volume mounted into the
// cisco-vk pod.
//
// Multi-host file is supported — the same callback works for an
// entire device fleet if the operator centralizes their known_hosts.
//
// Returns an explicit error on missing / unreadable file; callers
// should fail fast rather than silently fall back to
// InsecureIgnoreHostKey, which defeats the purpose of pinning.
//
// Wave 10 release-readiness fix (2026-04-28). Closes the documented
// "NETCONF host-key default unsafe" gap with a copy-paste-ready
// helper.
func LoadKnownHostsCallback(path string) (ssh.HostKeyCallback, error) {
	if path == "" {
		return nil, errors.New("LoadKnownHostsCallback: path is empty")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("LoadKnownHostsCallback: stat %s: %w", path, err)
	}
	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("LoadKnownHostsCallback: parse %s: %w", path, err)
	}
	return cb, nil
}
