// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

// Diagnostic probe for finding #6(a) — the from-pod NETCONF dial
// fails immediately with `read-empty: EOF` while the same code from
// a side-by-side probe pod (same image base, SA, node, namespace,
// network namespace) succeeds. The earlier evidence rules out the
// network and the container image. This file ships an in-process
// probe so the operator can correlate cisco-vk-side dial failures
// with the timing of apphosting + VK background activity.
//
// Wired into cmd/cisco-vk/run.go behind CONFIG_NETCONF_PROBE.
// Fires every 30 seconds for 15 minutes with the device's NETCONF
// port (TCP/830) as target. Pure observation — does not feed into
// the configdriver path.

import (
	"context"
	"net"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	"golang.org/x/crypto/ssh"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// runNETCONFProbe periodically attempts a NETCONF SSH dial against
// the device named in spec. Each attempt prints a single log line
// describing the outcome (raw TCP banner peek + Go ssh.Dial result).
// Returns when ctx is cancelled or the deadline expires.
func runNETCONFProbe(ctx context.Context, spec *ciskov1.DeviceSpec, password string) {
	if spec == nil || spec.Address == "" {
		log.G(ctx).Warn("netconf-probe: empty device spec; skipping")
		return
	}
	addr := net.JoinHostPort(spec.Address, "830")
	user := spec.Username
	deadline := time.Now().Add(15 * time.Minute)
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()

	log.G(ctx).Infof("netconf-probe: starting; addr=%s user=%s deadline=15m", addr, user)
	// First fire happens immediately so the operator can compare with
	// the configdriver's startup-time dial outcome on the same line.
	probeOnce(ctx, addr, user, password)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			log.G(ctx).Info("netconf-probe: context cancelled; stopping")
			return
		case <-tick.C:
		}
		probeOnce(ctx, addr, user, password)
	}
	log.G(ctx).Info("netconf-probe: deadline reached; stopping")
}

func probeOnce(ctx context.Context, addr, user, password string) {
	// Phase 1: raw TCP read so we can distinguish "device sends EOF"
	// from "device sends garbage" from "Go ssh client mis-parses".
	rawSummary := "skipped"
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		rawSummary = "tcp-dial-failed: " + err.Error()
	} else {
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		buf := make([]byte, 64)
		n, rerr := c.Read(buf)
		c.Close()
		if n == 0 {
			if rerr != nil {
				rawSummary = "read-empty: " + rerr.Error()
			} else {
				rawSummary = "read-empty: peer-closed"
			}
		} else {
			rawSummary = printableASCII(buf[:n])
		}
	}

	// Phase 2: full ssh.Dial — same code path the configdriver uses.
	conf := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	sshSummary := ""
	sc, serr := ssh.Dial("tcp", addr, conf)
	if serr != nil {
		sshSummary = "FAIL: " + serr.Error()
	} else {
		sshSummary = "OK ver=" + string(sc.ServerVersion())
		sc.Close()
	}
	log.G(ctx).Infof("netconf-probe: tick raw=%q ssh=%s", rawSummary, sshSummary)
}

func printableASCII(b []byte) string {
	out := make([]byte, 0, len(b))
	for _, x := range b {
		if x >= 0x20 && x <= 0x7e {
			out = append(out, x)
		} else if x == '\r' {
			out = append(out, '\\', 'r')
		} else if x == '\n' {
			out = append(out, '\\', 'n')
		} else {
			out = append(out, '.')
		}
	}
	return string(out)
}
