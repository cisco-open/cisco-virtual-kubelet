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

// kubectl-ciscovk is the operator-facing kubectl plugin that
// surfaces domain-aware views over the cisco-vk CRDs and a low-
// latency `exec` path for ad-hoc IOS-XE show commands.
//
// Subcommands implemented in Phase C:
//
//	kubectl ciscovk exec <device> [-n <ns>] [--allow-secrets]
//	    [--truncate-bytes N] -- <show command...>
//
// The exec subcommand runs `kubectl port-forward` as a subprocess
// to tunnel to the per-device-pod's admin endpoint, then POSTs the
// command list. The plugin terminates port-forward when the request
// completes.
//
// Future subcommands (diagnostics-RFC §13.6 + roadmap):
//
//	diff   — netascode-shape diff between desired + observed
//	explain — netascode field reference for a family
//	replay — interactive picker over IOSXEConfigApplyLog entries
//	health — fleet-wide rollup
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "exec":
		if err := runExec(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage()
	case "version":
		fmt.Println("kubectl-ciscovk v0.1.0")
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `kubectl ciscovk — operator plugin for cisco-virtual-kubelet

Subcommands:
  exec <device> [-n <ns>] [flags] -- <show-command...>
    Run an IOS-XE operational ("show") command on the device's per-pod
    kubelet. Output is read-only — destructive commands (clear, reload,
    write erase) are NOT supported by this subcommand. See the
    device-operations RFC for those.

Examples:
  kubectl ciscovk exec cat9k-smoke -n cisco-vk-smoke -- show ip route
  kubectl ciscovk exec cat9k-smoke -- "show running-config | section interface"
  kubectl ciscovk exec cat9k-smoke --allow-secrets -- show running-config

Flags for exec:
  -n, --namespace <ns>     namespace of the per-device kubelet pod
  --allow-secrets          disable default secret-redaction filter
  --truncate-bytes N       cap each command's output (default 64 KiB; 0 disables)
  --port N                 local port for port-forward (default: random free)
  --timeout DURATION       overall timeout (default 30s)
  --kubectl PATH           path to kubectl binary (default: from PATH)`)
}

// execFlags is the parsed argv for the `exec` subcommand. Hand-
// rolled instead of pulling in a flag library so the plugin stays
// dependency-free at build time.
type execFlags struct {
	device       string
	namespace    string
	allowSecrets bool
	truncateB    int
	localPort    int
	timeout      time.Duration
	kubectlBin   string
	commands     []string
}

func parseExecArgs(argv []string) (*execFlags, error) {
	f := &execFlags{
		truncateB:  64 * 1024,
		timeout:    30 * time.Second,
		kubectlBin: "kubectl",
	}
	i := 0
	if len(argv) > 0 && !strings.HasPrefix(argv[0], "-") {
		f.device = argv[0]
		i = 1
	}
	for ; i < len(argv); i++ {
		a := argv[i]
		switch a {
		case "--":
			f.commands = append(f.commands, strings.Join(argv[i+1:], " "))
			return f, validateExec(f)
		case "-n", "--namespace":
			i++
			if i >= len(argv) {
				return nil, errors.New("-n/--namespace requires a value")
			}
			f.namespace = argv[i]
		case "--allow-secrets":
			f.allowSecrets = true
		case "--truncate-bytes":
			i++
			if i >= len(argv) {
				return nil, errors.New("--truncate-bytes requires a value")
			}
			n, err := parsePositiveInt(argv[i])
			if err != nil {
				return nil, fmt.Errorf("--truncate-bytes: %w", err)
			}
			f.truncateB = n
		case "--port":
			i++
			if i >= len(argv) {
				return nil, errors.New("--port requires a value")
			}
			n, err := parsePositiveInt(argv[i])
			if err != nil {
				return nil, fmt.Errorf("--port: %w", err)
			}
			f.localPort = n
		case "--timeout":
			i++
			if i >= len(argv) {
				return nil, errors.New("--timeout requires a value")
			}
			d, err := time.ParseDuration(argv[i])
			if err != nil {
				return nil, fmt.Errorf("--timeout: %w", err)
			}
			f.timeout = d
		case "--kubectl":
			i++
			if i >= len(argv) {
				return nil, errors.New("--kubectl requires a path")
			}
			f.kubectlBin = argv[i]
		default:
			if f.device == "" {
				f.device = a
				continue
			}
			return nil, fmt.Errorf("unknown flag %q (commands must follow `--`)", a)
		}
	}
	return f, validateExec(f)
}

func parsePositiveInt(s string) (int, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a positive integer: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func validateExec(f *execFlags) error {
	if f.device == "" {
		return errors.New("missing device argument; usage: kubectl ciscovk exec <device> -- <command>")
	}
	if len(f.commands) == 0 {
		return errors.New("missing command after `--`; example: kubectl ciscovk exec cat9k-smoke -- show ip route")
	}
	// Defence-in-depth: refuse known-destructive commands explicitly.
	// The admin server is read-only by design (cli-exec, not
	// cli-config-data), but a typo on the device side is one fewer
	// failure mode if we reject here too. The device-operations
	// RFC's destructive paths land on a different CRD and code path.
	for _, cmd := range f.commands {
		head := strings.ToLower(strings.TrimSpace(cmd))
		for _, banned := range []string{"reload", "write erase", "delete flash:", "format flash:", "clear "} {
			if strings.HasPrefix(head, banned) {
				return fmt.Errorf("destructive command %q is not supported by `exec`; see device-operations-rfc.md for IOSXEMaintenance / IOSXEDeviceOp", cmd)
			}
		}
	}
	return nil
}

func runExec(argv []string) error {
	f, err := parseExecArgs(argv)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, cancelTO := context.WithTimeout(ctx, f.timeout)
	defer cancelTO()

	pfPort, kubectlCmd, err := startPortForward(ctx, f)
	if err != nil {
		return fmt.Errorf("port-forward: %w", err)
	}
	defer func() {
		if kubectlCmd != nil && kubectlCmd.Process != nil {
			_ = kubectlCmd.Process.Signal(syscall.SIGTERM)
			_, _ = kubectlCmd.Process.Wait()
		}
	}()

	// The /healthz endpoint is the canary for "kubectl port-forward
	// has finished its initial setup AND the admin server has its
	// transport". Poll it briefly before the real POST.
	if err := waitForHealthz(ctx, pfPort); err != nil {
		return fmt.Errorf("admin endpoint not ready: %w", err)
	}

	body, err := json.Marshal(map[string]any{
		"commands":      f.commands,
		"allowSecrets":  f.allowSecrets,
		"truncateBytes": f.truncateB,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/v1/exec", pfPort), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: f.timeout}).Do(req)
	if err != nil {
		return fmt.Errorf("POST /v1/exec: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("admin endpoint %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	var parsed struct {
		Device         string `json:"device"`
		Transport      string `json:"transport"`
		CapturedAt     string `json:"capturedAt"`
		Results        []struct {
			Command   string `json:"command"`
			Output    string `json:"output,omitempty"`
			Err       string `json:"err,omitempty"`
			Truncated bool   `json:"truncated,omitempty"`
			Redacted  bool   `json:"redacted,omitempty"`
		} `json:"results"`
		TransportError string `json:"transportError,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	// Header line for fleet log clarity. Operators piping the output
	// to grep / less appreciate the device + transport context.
	fmt.Printf("# device=%s transport=%s captured=%s\n",
		parsed.Device, parsed.Transport, parsed.CapturedAt)

	if parsed.TransportError != "" {
		fmt.Fprintf(os.Stderr, "# transport-error: %s\n", parsed.TransportError)
	}

	for i, r := range parsed.Results {
		if i > 0 || len(parsed.Results) > 1 {
			fmt.Printf("\n# ─── %s ──────────────────────────────\n", r.Command)
		}
		if r.Err != "" {
			fmt.Fprintf(os.Stderr, "# error: %s\n", r.Err)
			continue
		}
		fmt.Println(r.Output)
		if r.Truncated {
			fmt.Fprintln(os.Stderr, "# (output truncated by --truncate-bytes)")
		}
	}
	return nil
}

// startPortForward launches `kubectl port-forward` as a subprocess
// targeting the device's per-pod kubelet. Returns the local port
// chosen plus the cmd handle so the caller can clean up.
//
// We use kubectl's pod-label selector (app.kubernetes.io/instance=
// <device>) so the plugin doesn't have to know the deployment-
// generated pod name.
func startPortForward(ctx context.Context, f *execFlags) (int, *exec.Cmd, error) {
	port := f.localPort
	if port == 0 {
		port = 0 // kubectl picks; we discover by parsing its stderr
	}
	args := []string{"port-forward"}
	if f.namespace != "" {
		args = append(args, "-n", f.namespace)
	}
	// The admin server binds to :8082; kubectl maps localPort:8082.
	args = append(args, "pod",
		"--address=127.0.0.1",
		fmt.Sprintf("-l=app.kubernetes.io/instance=%s", f.device))
	if port == 0 {
		args = append(args, ":8082")
	} else {
		args = append(args, fmt.Sprintf("%d:8082", port))
	}
	cmd := exec.CommandContext(ctx, f.kubectlBin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return 0, nil, err
	}
	// kubectl prints "Forwarding from 127.0.0.1:NNNNN -> 8082"
	// on stdout when ready. Parse the local port. Bound the wait
	// to context — if kubectl never speaks, we surface the issue
	// rather than hanging.
	resolved := make(chan int, 1)
	errCh := make(chan error, 1)
	go func() {
		defer close(resolved)
		defer close(errCh)
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				if p := parseForwardingPort(string(buf[:n])); p > 0 {
					resolved <- p
					_, _ = io.Copy(io.Discard, stdout) // drain rest
					return
				}
			}
			if err != nil {
				errCh <- err
				return
			}
		}
	}()
	select {
	case p := <-resolved:
		return p, cmd, nil
	case err := <-errCh:
		_ = cmd.Process.Kill()
		return 0, nil, fmt.Errorf("kubectl port-forward exited before binding: %v", err)
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return 0, nil, ctx.Err()
	}
}

// parseForwardingPort extracts NNNNN from kubectl's "Forwarding
// from 127.0.0.1:NNNNN -> 8082" line.
func parseForwardingPort(s string) int {
	// Look for "127.0.0.1:" followed by digits. Quick, no regex.
	for i := 0; i+10 < len(s); i++ {
		if !strings.HasPrefix(s[i:], "127.0.0.1:") {
			continue
		}
		j := i + len("127.0.0.1:")
		port := 0
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			port = port*10 + int(s[j]-'0')
			j++
		}
		if port > 0 {
			return port
		}
	}
	return 0
}

func waitForHealthz(ctx context.Context, port int) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		resp, err := http.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(150 * time.Millisecond)
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
		lastErr = fmt.Errorf("healthz returned %s", resp.Status)
		time.Sleep(150 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = errors.New("timed out")
	}
	return lastErr
}
