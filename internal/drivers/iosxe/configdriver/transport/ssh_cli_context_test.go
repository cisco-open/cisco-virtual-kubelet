// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package transport

import (
	"context"
	"net"
	"testing"
	"time"
)

// A device can accept TCP while its SSH service is wedged. Cleanup must return
// within its deadline without dispatching a command or opening another session.
func TestSSHCommandHandshakeIsBounded(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			accepted := make(chan net.Conn, 1)
			go func() {
				c, err := listener.Accept()
				if err == nil {
					accepted <- c
				}
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			timeout := 10 * time.Second
			if mode == "timeout" {
				timeout = 150 * time.Millisecond
			}
			done := make(chan error, 1)
			go func() {
				_, err := runCommandsViaSSHContext(ctx, sshCLIConfig{Address: "127.0.0.1", CLIPort: listener.Addr().(*net.TCPAddr).Port, Timeout: timeout}, []string{"show version"})
				done <- err
			}()
			select {
			case conn := <-accepted:
				defer conn.Close()
			case <-time.After(3 * time.Second):
				t.Fatal("no connection")
			}
			if mode == "cancel" {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("silent peer accepted")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("SSH handshake ignored cancellation/timeout")
			}
		})
	}
}
