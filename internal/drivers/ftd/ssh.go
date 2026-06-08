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

package ftd

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"golang.org/x/crypto/ssh"
)

const (
	defaultFTDSSHPort               = 22
	defaultFTDConnectTimeoutSeconds = 15
	defaultFTDCommandTimeoutSeconds = 45
)

type sshClient struct {
	address        string
	port           int
	username       string
	password       string
	connectTimeout time.Duration
	commandTimeout time.Duration
}

func newSSHClient(spec *v1alpha1.DeviceSpec) (*sshClient, error) {
	if spec == nil {
		return nil, fmt.Errorf("ftd ssh: nil device spec")
	}
	if spec.Address == "" {
		return nil, fmt.Errorf("ftd ssh: device address is required")
	}
	if spec.Username == "" {
		return nil, fmt.Errorf("ftd ssh: username is required")
	}
	if spec.Password == "" {
		return nil, fmt.Errorf("ftd ssh: password is required")
	}
	return &sshClient{
		address:        spec.Address,
		port:           ftdSSHPort(spec),
		username:       spec.Username,
		password:       spec.Password,
		connectTimeout: ftdConnectTimeout(spec),
		commandTimeout: ftdCommandTimeout(spec),
	}, nil
}

func (c *sshClient) Run(ctx context.Context, command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("ftd ssh: command cannot be empty")
	}
	ctx, cancel := context.WithTimeout(ctx, c.commandTimeout)
	defer cancel()

	client, err := c.dial(ctx)
	if err != nil {
		return "", err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ftd ssh %s: create session: %w", c.endpoint(), err)
	}
	defer session.Close()

	type runResult struct {
		output []byte
		err    error
	}
	done := make(chan runResult, 1)
	go func() {
		output, runErr := session.CombinedOutput(command)
		done <- runResult{output: output, err: runErr}
	}()

	select {
	case <-ctx.Done():
		_ = session.Close()
		_ = client.Close()
		return "", fmt.Errorf("ftd ssh %s command %q timed out: %w", c.endpoint(), command, ctx.Err())
	case result := <-done:
		out := string(result.output)
		if result.err != nil {
			return out, fmt.Errorf("ftd ssh %s command %q: %w: %s", c.endpoint(), command, result.err, strings.TrimSpace(out))
		}
		return out, nil
	}
}

func (c *sshClient) dial(ctx context.Context) (*ssh.Client, error) {
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
		return nil, fmt.Errorf("ftd ssh %s: dial: %w", addr, err)
	}
	deadline := time.Now().Add(c.connectTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ftd ssh %s: handshake: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(sshConn, chans, reqs), nil
}

func (c *sshClient) endpoint() string {
	return net.JoinHostPort(c.address, strconv.Itoa(c.port))
}

func ftdSSHPort(spec *v1alpha1.DeviceSpec) int {
	if spec != nil && spec.FTD != nil && spec.FTD.Management != nil && spec.FTD.Management.SSHPort > 0 {
		return spec.FTD.Management.SSHPort
	}
	if spec != nil && spec.Port > 0 {
		return spec.Port
	}
	return defaultFTDSSHPort
}

func ftdConnectTimeout(spec *v1alpha1.DeviceSpec) time.Duration {
	seconds := defaultFTDConnectTimeoutSeconds
	if spec != nil && spec.FTD != nil && spec.FTD.Management != nil && spec.FTD.Management.ConnectTimeoutSeconds > 0 {
		seconds = spec.FTD.Management.ConnectTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}

func ftdCommandTimeout(spec *v1alpha1.DeviceSpec) time.Duration {
	seconds := defaultFTDCommandTimeoutSeconds
	if spec != nil && spec.FTD != nil && spec.FTD.Management != nil && spec.FTD.Management.CommandTimeoutSeconds > 0 {
		seconds = spec.FTD.Management.CommandTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}
