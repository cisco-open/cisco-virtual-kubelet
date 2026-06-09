// Copyright (c) 2026 Cisco Systems Inc.
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

package iosxr

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"golang.org/x/crypto/ssh"
)

const (
	defaultXRSSHPort               = 22
	defaultXRConnectTimeoutSeconds = 20
	defaultXRCommandTimeoutSeconds = 180
)

var iosxrPromptRE = regexp.MustCompile(`(?m)(?:RP/\d+/RP\d+/CPU\d+:)?[A-Za-z0-9_.:-]+(?:\([^)]+\))?[#>]\s*$`)

type xrSSHClient struct {
	address        string
	port           int
	username       string
	password       string
	connectTimeout time.Duration
	commandTimeout time.Duration
	mu             sync.Mutex
}

func newSSHClient(spec *v1alpha1.DeviceSpec) (*xrSSHClient, error) {
	if spec == nil {
		return nil, fmt.Errorf("iosxr ssh: nil device spec")
	}
	if spec.Address == "" {
		return nil, fmt.Errorf("iosxr ssh: device address is required")
	}
	if spec.Username == "" {
		return nil, fmt.Errorf("iosxr ssh: username is required")
	}
	if spec.Password == "" {
		return nil, fmt.Errorf("iosxr ssh: password is required")
	}
	return &xrSSHClient{
		address:        spec.Address,
		port:           xrSSHPort(spec),
		username:       spec.Username,
		password:       spec.Password,
		connectTimeout: xrConnectTimeout(spec),
		commandTimeout: xrCommandTimeout(spec),
	}, nil
}

func (c *xrSSHClient) Run(ctx context.Context, command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("iosxr ssh: command cannot be empty")
	}
	return c.runShell(ctx, []string{command})
}

func (c *xrSSHClient) Configure(ctx context.Context, commands ...string) (string, error) {
	var clean []string
	for _, command := range commands {
		if command = strings.TrimSpace(command); command != "" {
			clean = append(clean, command)
		}
	}
	if len(clean) == 0 {
		return "", fmt.Errorf("iosxr ssh: configuration commands cannot be empty")
	}
	sequence := append([]string{"configure"}, clean...)
	sequence = append(sequence, "commit", "end")
	return c.runShell(ctx, sequence)
}

func (c *xrSSHClient) runShell(ctx context.Context, commands []string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var out string
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		out, err = c.runShellOnce(ctx, commands)
		if err == nil || !isTransientSSHError(err) {
			return out, err
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	return out, err
}

func (c *xrSSHClient) runShellOnce(ctx context.Context, commands []string) (string, error) {
	timeout := c.commandTimeout * time.Duration(len(commands)+2)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client, err := c.dial(ctx)
	if err != nil {
		return "", err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("iosxr ssh %s: create session: %w", c.endpoint(), err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("iosxr ssh %s: stdin pipe: %w", c.endpoint(), err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("iosxr ssh %s: stdout pipe: %w", c.endpoint(), err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("iosxr ssh %s: stderr pipe: %w", c.endpoint(), err)
	}

	var buf bytes.Buffer
	var mu sync.Mutex
	writer := lockedWriter{buf: &buf, mu: &mu}
	go func() { _, _ = io.Copy(writer, stdout) }()
	go func() { _, _ = io.Copy(writer, stderr) }()

	if err := session.RequestPty("vt100", 80, 240, ssh.TerminalModes{ssh.ECHO: 1}); err != nil {
		return "", fmt.Errorf("iosxr ssh %s: request pty: %w", c.endpoint(), err)
	}
	if err := session.Shell(); err != nil {
		return "", fmt.Errorf("iosxr ssh %s: start shell: %w", c.endpoint(), err)
	}
	if err := waitForIOSXRPrompt(ctx, &buf, &mu, 0); err != nil {
		return cleanIOSXROutput(snapshotBuffer(&buf, &mu)), fmt.Errorf("iosxr ssh %s: initial prompt: %w", c.endpoint(), err)
	}

	sequence := append([]string{"terminal length 0"}, commands...)
	for _, command := range sequence {
		start := snapshotLen(&buf, &mu)
		if _, err := fmt.Fprintln(stdin, command); err != nil {
			return cleanIOSXROutput(snapshotBuffer(&buf, &mu)), fmt.Errorf("iosxr ssh %s command %q: send: %w", c.endpoint(), command, err)
		}
		if err := waitForIOSXRPrompt(ctx, &buf, &mu, start); err != nil {
			return cleanIOSXROutput(snapshotBuffer(&buf, &mu)), fmt.Errorf("iosxr ssh %s command %q: %w", c.endpoint(), command, err)
		}
	}
	_, _ = fmt.Fprintln(stdin, "exit")
	_ = session.Wait()

	out := cleanIOSXROutput(snapshotBuffer(&buf, &mu))
	if err := detectIOSXRCLIError(commands[len(commands)-1], out); err != nil {
		return out, err
	}
	return out, nil
}

func isTransientSSHError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "handshake failed: eof") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "i/o timeout")
}

func (c *xrSSHClient) dial(ctx context.Context) (*ssh.Client, error) {
	addr := c.endpoint()
	config := &ssh.ClientConfig{
		User: c.username,
		Auth: []ssh.AuthMethod{
			ssh.Password(c.password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // #nosec G106 - lab devices may not have stable host keys.
		Timeout:         c.connectTimeout,
	}
	dialer := &net.Dialer{Timeout: c.connectTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("iosxr ssh %s: dial: %w", addr, err)
	}
	deadline := time.Now().Add(c.connectTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("iosxr ssh %s: handshake: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(sshConn, chans, reqs), nil
}

func (c *xrSSHClient) endpoint() string {
	return net.JoinHostPort(c.address, strconv.Itoa(c.port))
}

type lockedWriter struct {
	buf *bytes.Buffer
	mu  *sync.Mutex
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func waitForIOSXRPrompt(ctx context.Context, buf *bytes.Buffer, mu *sync.Mutex, start int) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		out := cleanIOSXROutput(snapshotBufferFrom(buf, mu, start))
		if iosxrPromptRE.MatchString(out) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func snapshotLen(buf *bytes.Buffer, mu *sync.Mutex) int {
	mu.Lock()
	defer mu.Unlock()
	return buf.Len()
}

func snapshotBuffer(buf *bytes.Buffer, mu *sync.Mutex) string {
	return snapshotBufferFrom(buf, mu, 0)
}

func snapshotBufferFrom(buf *bytes.Buffer, mu *sync.Mutex, start int) string {
	mu.Lock()
	defer mu.Unlock()
	b := buf.Bytes()
	if start < 0 || start > len(b) {
		start = 0
	}
	return string(b[start:])
}

func cleanIOSXROutput(s string) string {
	s = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`).ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\b' || r == 0x7f {
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

func detectIOSXRCLIError(command, out string) error {
	lower := strings.ToLower(out)
	switch {
	case strings.Contains(out, "% Invalid input detected"):
		return fmt.Errorf("iosxr cli command %q failed: invalid input", command)
	case strings.Contains(lower, "failed to start operation"):
		return fmt.Errorf("iosxr cli command %q failed: %s", command, firstMatchingLine(out, "Failed to start operation"))
	default:
		return nil
	}
}

func firstMatchingLine(out, needle string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			return strings.TrimSpace(line)
		}
	}
	return strings.TrimSpace(out)
}

func xrSSHPort(spec *v1alpha1.DeviceSpec) int {
	if spec != nil && spec.XR != nil && spec.XR.Management != nil && spec.XR.Management.SSHPort > 0 {
		return spec.XR.Management.SSHPort
	}
	if spec != nil && spec.Port > 0 {
		return spec.Port
	}
	return defaultXRSSHPort
}

func xrConnectTimeout(spec *v1alpha1.DeviceSpec) time.Duration {
	seconds := defaultXRConnectTimeoutSeconds
	if spec != nil && spec.XR != nil && spec.XR.Management != nil && spec.XR.Management.ConnectTimeoutSeconds > 0 {
		seconds = spec.XR.Management.ConnectTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}

func xrCommandTimeout(spec *v1alpha1.DeviceSpec) time.Duration {
	seconds := defaultXRCommandTimeoutSeconds
	if spec != nil && spec.XR != nil && spec.XR.Management != nil && spec.XR.Management.CommandTimeoutSeconds > 0 {
		seconds = spec.XR.Management.CommandTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}
