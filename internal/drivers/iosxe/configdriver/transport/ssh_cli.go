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

package transport

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// sshCLIConfig is the input to runShowCommandsViaSSH. It carries the
// device-side connection details and the per-command timeout. We
// reuse the NETCONF transport's stored credentials — the only
// transport-specific piece is the port (NETCONF=830, raw CLI=22).
type sshCLIConfig struct {
	// Address is the device hostname or IP. Required.
	Address string
	// CLIPort is the SSH port for raw IOS CLI; 22 is the IOS-XE
	// default. The configdriver's own NETCONF dial uses 830 — these
	// are independent SSH services on the same device.
	CLIPort int
	// Username, Password — same identity the configdriver uses for
	// NETCONF. The user must have privilege 15 to run the show
	// commands operators normally type.
	Username string
	Password string
	// HostKeyCallback mirrors NETCONFConfig.HostKeyCallback. nil
	// defaults to ssh.InsecureIgnoreHostKey() for lab use.
	HostKeyCallback ssh.HostKeyCallback
	// Timeout bounds the SSH dial AND each command's read-loop.
	// Defaults to 30s when zero.
	Timeout time.Duration
}

// runShowCommandsViaSSH opens an interactive SSH session to the
// device's CLI (typically port 22) and runs each command in turn,
// returning per-command stdout text. Per-command failures populate
// CommandResult.Err but do NOT abort the batch.
//
// Why this exists separately from the NETCONF cli-exec path:
// IOS-XE 17.18.x removed the legacy ConfD `<cli-exec>` element from
// its NETCONF agent (the read-side counterpart of `cli-config-data`)
// and the modelled `Cisco-IOS-XE-cli-rpc:config-ios-cli-rpc`'s
// `result` leaf only carries a status string ("RPC request
// successful"), not the textual show output. The device's SSH CLI
// is the only operationally-stable surface that returns the text
// operators expect from `show ip route`-style commands. Using SSH
// directly mirrors what `gnoi cli.exec` does on platforms that
// ship gNOI.
//
// Wire shape:
//   - SSH dial with PasswordAuth (HostKey ignored unless
//     HostKeyCallback is set, mirroring the NETCONF dial pattern).
//   - One session per batch (re-using the TCP connection across
//     commands). Within the session, set "terminal length 0" and
//     "terminal width 0" first so the device doesn't paginate.
//   - For each command: send the line, wait for the next prompt,
//     return everything between the command echo and the prompt.
//   - Final "exit" closes the session.
func runShowCommandsViaSSH(cfg sshCLIConfig, commands []string) ([]CommandResult, error) {
	if cfg.Address == "" {
		return nil, fmt.Errorf("ssh-cli: empty Address")
	}
	port := cfg.CLIPort
	if port == 0 {
		port = 22
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	hostKey := cfg.HostKeyCallback
	if hostKey == nil {
		hostKey = ssh.InsecureIgnoreHostKey()
	}

	clientCfg := &ssh.ClientConfig{
		User:            cfg.Username,
		Auth:            []ssh.AuthMethod{ssh.Password(cfg.Password)},
		HostKeyCallback: hostKey,
		Timeout:         timeout,
	}
	addr := net.JoinHostPort(cfg.Address, strconv.Itoa(port))
	client, err := ssh.Dial("tcp", addr, clientCfg)
	if err != nil {
		return nil, fmt.Errorf("ssh-cli: dial %s: %w", addr, err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ssh-cli: open session: %w", err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("ssh-cli: stdin: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ssh-cli: stdout: %w", err)
	}

	// Cisco devices serve their CLI off the default shell channel —
	// we ask for a vt100 PTY so the IOS line editor accepts our
	// input. The dimensions don't matter (we disable pagination
	// below), but the device rejects sessions without one.
	if err := session.RequestPty("vt100", 24, 200, ssh.TerminalModes{
		ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400,
	}); err != nil {
		return nil, fmt.Errorf("ssh-cli: request-pty: %w", err)
	}
	if err := session.Shell(); err != nil {
		return nil, fmt.Errorf("ssh-cli: shell: %w", err)
	}

	// Match only a prompt on the final line of the accumulated buffer.
	// Using multiline `$` here lets a stale prompt at the top of a PTY
	// transcript terminate the read before the command output arrives.
	promptRe := iosPromptAtEndRe

	rb := newSSHReadBuffer(stdout, timeout)

	// 1. Wait for the initial prompt before sending anything.
	if _, err := rb.readUntilPromptOrTimeout(promptRe); err != nil {
		return nil, fmt.Errorf("ssh-cli: initial prompt: %w", err)
	}

	// 2. Disable pagination — these commands produce no useful
	// output, so we eat their echoes silently.
	for _, setup := range []string{"terminal length 0", "terminal width 0"} {
		if _, err := io.WriteString(stdin, setup+"\n"); err != nil {
			return nil, fmt.Errorf("ssh-cli: send setup %q: %w", setup, err)
		}
		if _, err := rb.readUntilPromptOrTimeout(promptRe); err != nil {
			return nil, fmt.Errorf("ssh-cli: setup ack %q: %w", setup, err)
		}
	}

	// 3. Per-command loop.
	out := make([]CommandResult, 0, len(commands))
	for _, cmd := range commands {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}
		if _, err := io.WriteString(stdin, cmd+"\n"); err != nil {
			out = append(out, CommandResult{Command: cmd, Err: err.Error()})
			continue
		}
		body, err := rb.readUntilPromptOrTimeout(promptRe)
		if err != nil {
			out = append(out, CommandResult{Command: cmd, Err: err.Error()})
			continue
		}
		// Strip the echoed command (first line) and the trailing
		// prompt from `body`. IOS echoes the command back as the
		// first line of the response.
		out = append(out, CommandResult{Command: cmd, Output: trimEchoAndPrompt(body, cmd)})
	}

	// 4. Best-effort exit — close stdin then ignore session error.
	_, _ = io.WriteString(stdin, "exit\n")
	_ = stdin.Close()
	_ = session.Wait()
	return out, nil
}

// sshReadBuffer is a small wrapper over an io.Reader that drains
// asynchronously into an in-memory buffer. readUntilPromptOrTimeout
// blocks until either the regexp matches against the accumulated
// bytes or the timeout fires.
type sshReadBuffer struct {
	src     io.Reader
	timeout time.Duration
	buf     bytes.Buffer
	dataCh  chan []byte
	errCh   chan error
}

func newSSHReadBuffer(src io.Reader, timeout time.Duration) *sshReadBuffer {
	rb := &sshReadBuffer{
		src:     src,
		timeout: timeout,
		dataCh:  make(chan []byte, 16),
		errCh:   make(chan error, 1),
	}
	go func() {
		defer close(rb.dataCh)
		buf := make([]byte, 4096)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				rb.dataCh <- cp
			}
			if err != nil {
				rb.errCh <- err
				return
			}
		}
	}()
	return rb
}

func (rb *sshReadBuffer) readUntilPromptOrTimeout(promptRe *regexp.Regexp) (string, error) {
	deadline := time.NewTimer(rb.timeout)
	defer deadline.Stop()
	for {
		// Check existing buffer first — the prompt may already be
		// in there from a prior read.
		if loc := promptRe.FindIndex(rb.buf.Bytes()); loc != nil {
			snapshot := rb.buf.String()
			rb.buf.Reset()
			// Anything beyond the prompt match goes back into the
			// buffer for the next call.
			rb.buf.WriteString(snapshot[loc[1]:])
			return snapshot[:loc[1]], nil
		}
		select {
		case chunk, ok := <-rb.dataCh:
			if !ok {
				// stream closed before prompt; drain errCh
				if err, ok2 := <-rb.errCh; ok2 && err != io.EOF {
					return rb.buf.String(), err
				}
				return rb.buf.String(), io.EOF
			}
			rb.buf.Write(chunk)
		case <-deadline.C:
			return rb.buf.String(), fmt.Errorf("ssh-cli: timed out waiting for prompt")
		}
	}
}

// trimEchoAndPrompt strips the command-echo line (IOS sends the
// command back before the output) and any trailing prompt from
// `body`. Returns just the output text.
func trimEchoAndPrompt(body, cmd string) string {
	// IOS uses CRLF line endings; normalise so split-on-newline
	// works the same way operators expect when reading the JSON.
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")

	// First line is the echo of the command we sent. Match exactly
	// (after trim) so a partial-prefix doesn't accidentally eat
	// real output.
	if i := strings.Index(body, "\n"); i >= 0 {
		first := strings.TrimSpace(body[:i])
		if first == cmd {
			body = body[i+1:]
		}
	}

	// Trailing prompt: drop the last non-empty line if it looks
	// like a Cisco prompt.
	lines := strings.Split(body, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if isCiscoPrompt(t) {
			lines = lines[:i]
		}
		break
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

var ciscoPromptRe = regexp.MustCompile(`^[A-Za-z0-9._-]+(?:\([^)]+\))?[#>]\s*$`)
var iosPromptAtEndRe = regexp.MustCompile(`(?:^|\r?\n)[A-Za-z0-9._-]+(?:\([^)]+\))?[#>][ \t\r\n]*$`)

func isCiscoPrompt(line string) bool { return ciscoPromptRe.MatchString(line) }
