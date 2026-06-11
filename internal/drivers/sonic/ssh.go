// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package sonic

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

type commandClient interface {
	Run(ctx context.Context, command string) (string, error)
	Close() error
}

type sshCommandClient struct {
	address string
	port    int
	config  *ssh.ClientConfig
	timeout time.Duration
}

func newSSHClient(spec *v1alpha1.DeviceSpec, password string) (*sshCommandClient, error) {
	if spec == nil {
		return nil, fmt.Errorf("sonic ssh: nil device spec")
	}
	if password == "" {
		password = spec.Password
	}
	if spec.Username == "" || password == "" {
		return nil, fmt.Errorf("sonic ssh: username and password are required")
	}
	port := defaultSONICSSHPort
	connectTimeout := defaultSONICSSHConnect
	commandTimeout := defaultSONICCommandTimeout
	if spec.SONIC != nil && spec.SONIC.Management != nil {
		m := spec.SONIC.Management
		if m.SSHPort > 0 {
			port = m.SSHPort
		}
		if m.ConnectTimeoutSeconds > 0 {
			connectTimeout = time.Duration(m.ConnectTimeoutSeconds) * time.Second
		}
		if m.CommandTimeoutSeconds > 0 {
			commandTimeout = time.Duration(m.CommandTimeoutSeconds) * time.Second
		}
	}
	return &sshCommandClient{
		address: spec.Address,
		port:    port,
		timeout: commandTimeout,
		config: &ssh.ClientConfig{
			User:            spec.Username,
			Auth:            []ssh.AuthMethod{ssh.Password(password)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(), // #nosec G106 - lab devices often rotate ephemeral keys; pinning can be added later.
			Timeout:         connectTimeout,
		},
	}, nil
}

func (c *sshCommandClient) target() string {
	host := c.address
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return net.JoinHostPort(host, strconv.Itoa(c.port))
}

func (c *sshCommandClient) Run(ctx context.Context, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("sonic ssh: command cannot be empty")
	}
	client, err := ssh.Dial("tcp", c.target(), c.config)
	if err != nil {
		return "", fmt.Errorf("sonic ssh dial %s: %w", c.target(), err)
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	runCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	type result struct {
		out []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		out, err := sess.CombinedOutput(command)
		ch <- result{out: out, err: err}
	}()
	select {
	case <-runCtx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		return "", runCtx.Err()
	case res := <-ch:
		if res.err != nil {
			return string(res.out), res.err
		}
		return string(res.out), nil
	}
}

func (c *sshCommandClient) Close() error { return nil }
