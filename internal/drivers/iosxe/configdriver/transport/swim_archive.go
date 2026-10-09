// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package transport

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var retiredArchivePath = regexp.MustCompile(`^flash:gNOI_iosxe_[0-9][A-Za-z0-9.]{0,127}\.bin$`)

// This optional XE capability reuses the configured device identity and SSH
// side-channel. It cannot read arbitrary paths or accept CLI from manifests.
// The caller must prove retirement and hold canonical mutation authority.
func (r *restconfTransport) ReadRetiredArchive(ctx context.Context, path string, size int64, dst io.Writer) error {
	if !retiredArchivePath.MatchString(path) || size <= 0 || size > 16<<30 || r.cfg.CLIHost == "" {
		return errors.New("invalid retired archive request")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	port := r.cfg.CLIPort
	if port == 0 {
		port = 22
	}
	address := net.JoinHostPort(r.cfg.CLIHost, strconv.Itoa(port))
	// Match the existing RESTCONF diagnostic side-channel's SSH trust policy.
	cfg := &ssh.ClientConfig{User: r.cfg.Username, Auth: []ssh.AuthMethod{ssh.Password(r.cfg.Password)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 15 * time.Second}
	conn, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	c, ch, req, err := ssh.NewClientConn(conn, address, cfg)
	if err != nil {
		return errors.New("archive SSH authentication/handshake failed")
	}
	remote := ssh.NewClient(c, ch, req)
	defer remote.Close()
	session, err := remote.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	in, err := session.StdinPipe()
	if err != nil {
		return err
	}
	out, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	if err = session.Start("scp -f " + path); err != nil {
		return err
	}
	if err = receiveRetiredArchive(in, out, path, size, dst); err != nil {
		return err
	}
	_ = in.Close()
	return session.Wait()
}

// Parse only the single-file SCP shape we requested. No recursive directories,
// timestamps, additional files, oversized headers, or remote error text.
func receiveRetiredArchive(ack io.Writer, src io.Reader, path string, size int64, dst io.Writer) error {
	if _, err := ack.Write([]byte{0}); err != nil {
		return err
	}
	r := bufio.NewReaderSize(src, 4096)
	header, err := r.ReadSlice('\n')
	if err != nil || len(header) > 512 {
		return errors.New("invalid SCP archive header")
	}
	fields := strings.Split(strings.TrimSuffix(string(header), "\n"), " ")
	if len(fields) != 3 || len(fields[0]) != 5 || fields[0][0] != 'C' {
		return errors.New("unexpected SCP entry")
	}
	if _, err = strconv.ParseUint(fields[0][1:], 8, 16); err != nil {
		return errors.New("invalid SCP mode")
	}
	n, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || n != size || fields[2] != strings.TrimPrefix(path, "flash:") {
		return errors.New("SCP archive identity/size changed")
	}
	if _, err = ack.Write([]byte{0}); err != nil {
		return err
	}
	if _, err = io.CopyN(dst, r, size); err != nil {
		return err
	}
	end, err := r.ReadByte()
	if err != nil || end != 0 {
		return errors.New("SCP archive transfer incomplete")
	}
	if _, err = ack.Write([]byte{0}); err != nil {
		return err
	}
	if _, err = r.ReadByte(); err != io.EOF {
		return errors.New("unexpected additional SCP data")
	}
	return nil
}

func (r *restconfTransport) RemoveRetiredArchive(ctx context.Context, path string) error {
	if !retiredArchivePath.MatchString(path) || r.cfg.CLIHost == "" {
		return errors.New("invalid retired archive removal")
	}
	command := "delete /force " + path
	results, err := runCommandsViaSSHContext(ctx, sshCLIConfig{Address: r.cfg.CLIHost, CLIPort: r.cfg.CLIPort, Username: r.cfg.Username, Password: r.cfg.Password, Timeout: 30 * time.Second}, []string{command})
	if err != nil {
		return err
	}
	if len(results) != 1 || results[0].Command != command || results[0].Err != "" || strings.Contains(results[0].Output, "%") || strings.Contains(results[0].Output, "[confirm]") {
		return fmt.Errorf("archive removal acknowledgement unknown")
	}
	// Success is verified separately by complete native inventory, never by
	// prompt arrival or empty CLI output.
	return nil
}
