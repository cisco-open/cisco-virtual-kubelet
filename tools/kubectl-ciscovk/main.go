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
	"bufio"
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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Version, GitCommit, and BuildTime are populated by release builds with
// -ldflags. Development builds deliberately identify themselves as such
// instead of reporting a stale release version.
var (
	Version   = "devel"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

var commandContext = exec.CommandContext

const kubectlWaitDelay = 2 * time.Second

func main() {
	if code := runCLI(os.Args, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	invocation := "kubectl ciscovk"
	if len(args) > 0 {
		invocation = pluginInvocation(args[0])
	}
	if len(args) < 2 {
		usage(stderr, invocation)
		return 2
	}
	switch args[1] {
	case "exec":
		if err := runExecWithIO(args[2:], stdout, stderr); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
	case "-h", "--help", "help":
		usage(stderr, invocation)
	case "version":
		printVersion(stdout)
	default:
		fmt.Fprintf(stderr, "unknown subcommand %q\n\n", args[1])
		usage(stderr, invocation)
		return 2
	}
	return 0
}

func pluginInvocation(argv0 string) string {
	switch filepath.Base(argv0) {
	case "kubectl-cisco_vk", "kubectl-cisco-vk":
		return "kubectl cisco-vk"
	default:
		return "kubectl ciscovk"
	}
}

func usage(w io.Writer, invocation string) {
	text := `{{COMMAND}} — operator plugin for cisco-virtual-kubelet

Subcommands:
  exec <device> [-n <ns>] [flags] -- <show-command...>
    Run an IOS-XE operational ("show") command on the device's per-pod
    kubelet. Output is read-only — destructive commands (clear, reload,
    write erase) are NOT supported by this subcommand. See the
    device-operations RFC for those.

Examples:
  {{COMMAND}} exec cat9k-smoke -n cisco-vk-smoke -- show ip route
  {{COMMAND}} exec cat9k-smoke -- "show running-config | section interface"
  {{COMMAND}} exec cat9k-smoke --allow-secrets -- show running-config

Flags for exec:
  -n, --namespace <ns>     namespace of the per-device kubelet pod
  --allow-secrets          currently a no-op on the server; reserved for future SAR-gated path
  --truncate-bytes N       cap each command's output (default 64 KiB; 0 disables)
  --port N                 local port for port-forward (default: random free)
  --timeout DURATION       overall timeout (default 30s)
  --context NAME           kubeconfig context to use
  --kubeconfig PATH        path to the kubeconfig file
  --kubectl PATH           path to kubectl binary (default: from PATH)`
	fmt.Fprintln(w, strings.ReplaceAll(text, "{{COMMAND}}", invocation))
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "kubectl-ciscovk %s (commit=%s, built=%s)\n", Version, GitCommit, BuildTime)
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
	kubeContext  string
	kubeconfig   string
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
			n, err := parseNonNegativeInt(argv[i])
			if err != nil {
				return nil, fmt.Errorf("--truncate-bytes: %w", err)
			}
			f.truncateB = n
		case "--port":
			i++
			if i >= len(argv) {
				return nil, errors.New("--port requires a value")
			}
			n, err := parseNonNegativeInt(argv[i])
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
			if d <= 0 {
				return nil, errors.New("--timeout must be greater than zero")
			}
			f.timeout = d
		case "--kubectl":
			i++
			if i >= len(argv) {
				return nil, errors.New("--kubectl requires a path")
			}
			f.kubectlBin = argv[i]
		case "--context":
			i++
			if i >= len(argv) {
				return nil, errors.New("--context requires a name")
			}
			f.kubeContext = argv[i]
		case "--kubeconfig":
			i++
			if i >= len(argv) {
				return nil, errors.New("--kubeconfig requires a path")
			}
			f.kubeconfig = argv[i]
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

func parseNonNegativeInt(s string) (int, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	n, err := strconv.ParseUint(s, 10, 31)
	if err != nil {
		return 0, fmt.Errorf("not a non-negative integer: %q", s)
	}
	return int(n), nil
}

func validateExec(f *execFlags) error {
	if f.device == "" {
		return errors.New("missing device argument; usage: exec <device> -- <command>")
	}
	if len(f.commands) == 0 {
		return errors.New("missing command after `--`; example: exec cat9k-smoke -- show ip route")
	}
	if f.localPort > 65535 {
		return fmt.Errorf("--port must be between 0 and 65535, got %d", f.localPort)
	}
	// Defence-in-depth: refuse known-destructive commands explicitly.
	// The admin server is read-only by design (cli-exec, not
	// cli-config-data), but a typo on the device side is one fewer
	// failure mode if we reject here too. The device-operations
	// RFC's destructive paths land on a different CRD and code path.
	for _, cmd := range f.commands {
		head := strings.ToLower(strings.TrimSpace(cmd))
		if head == "" {
			return errors.New("command after `--` must not be empty")
		}
		for _, banned := range []string{"reload", "write erase", "delete flash:", "format flash:", "clear "} {
			if strings.HasPrefix(head, banned) {
				return fmt.Errorf("destructive command %q is not supported by `exec`; see device-operations-rfc.md for IOSXEMaintenance / IOSXEDeviceOp", cmd)
			}
		}
	}
	return nil
}

func runExec(argv []string) error {
	return runExecWithIO(argv, os.Stdout, os.Stderr)
}

func runExecWithIO(argv []string, stdout, stderr io.Writer) error {
	f, err := parseExecArgs(argv)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, cancelTO := context.WithTimeout(ctx, f.timeout)
	defer cancelTO()

	pfPort, kubectlCmd, err := startPortForward(ctx, f, stderr)
	if err != nil {
		return fmt.Errorf("port-forward: %w", err)
	}
	defer func() {
		stopProcess(kubectlCmd)
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
		Device     string `json:"device"`
		Transport  string `json:"transport"`
		CapturedAt string `json:"capturedAt"`
		Results    []struct {
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
	fmt.Fprintf(stdout, "# device=%s transport=%s captured=%s\n",
		parsed.Device, parsed.Transport, parsed.CapturedAt)

	if parsed.TransportError != "" {
		fmt.Fprintf(stderr, "# transport-error: %s\n", parsed.TransportError)
	}

	for i, r := range parsed.Results {
		if i > 0 || len(parsed.Results) > 1 {
			fmt.Fprintf(stdout, "\n# ─── %s ──────────────────────────────\n", r.Command)
		}
		if r.Err != "" {
			fmt.Fprintf(stderr, "# error: %s\n", r.Err)
			continue
		}
		fmt.Fprintln(stdout, r.Output)
		if r.Truncated {
			fmt.Fprintln(stderr, "# (output truncated by --truncate-bytes)")
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
func startPortForward(ctx context.Context, f *execFlags, liveStderr io.Writer) (int, *exec.Cmd, error) {
	kubectlPath, err := exec.LookPath(f.kubectlBin)
	if err != nil {
		return 0, nil, fmt.Errorf("kubectl executable %q not found: %w", f.kubectlBin, err)
	}

	port := f.localPort
	if port == 0 {
		port = 0 // kubectl picks; we discover by parsing its stdout
	}
	// kubectl port-forward takes a concrete pod NAME, not a label
	// selector. Resolve the pod first via `kubectl get pod -l
	// app.kubernetes.io/instance=<device> -o name`. The selector
	// is the canonical label the controller stamps on per-device
	// pods in the supported per-device deployment topology.
	getArgs := kubectlArgs(f, "get", "pod")
	if f.namespace != "" {
		getArgs = append(getArgs, "-n", f.namespace)
	}
	getArgs = append(getArgs,
		"-l", "app.kubernetes.io/instance="+f.device,
		"-o", "jsonpath={.items[0].metadata.name}")
	getCmd := commandContext(ctx, kubectlPath, getArgs...)
	getCmd.WaitDelay = kubectlWaitDelay
	var getStderr bytes.Buffer
	getCmd.Stderr = &getStderr
	podOut, err := getCmd.Output()
	if err != nil {
		return 0, nil, commandError(fmt.Sprintf("resolve pod for device %q", f.device), err, getStderr.String())
	}
	podName := strings.TrimSpace(string(podOut))
	if podName == "" {
		return 0, nil, fmt.Errorf("no pod found for device %q (label app.kubernetes.io/instance=%s)",
			f.device, f.device)
	}

	args := kubectlArgs(f, "port-forward")
	if f.namespace != "" {
		args = append(args, "-n", f.namespace)
	}
	args = append(args, podName, "--address=127.0.0.1")
	if port == 0 {
		args = append(args, ":8082")
	} else {
		args = append(args, fmt.Sprintf("%d:8082", port))
	}
	cmd := commandContext(ctx, kubectlPath, args...)
	// Bound Cmd.Wait even if a descendant inherits the port-forward pipes.
	cmd.WaitDelay = kubectlWaitDelay
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, nil, err
	}
	diagnostics := newDeferredDiagnostics(liveStderr)
	cmd.Stderr = diagnostics
	if err := cmd.Start(); err != nil {
		return 0, nil, err
	}
	// kubectl prints "Forwarding from 127.0.0.1:NNNNN -> 8082"
	// on stdout when ready. Parse the local port. Bound the wait
	// to context — if kubectl never speaks, we surface the issue
	// rather than hanging.
	type portForwardResult struct {
		port int
		err  error
	}
	resultCh := make(chan portForwardResult, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if p := parseForwardingPort(scanner.Text()); p > 0 {
				resultCh <- portForwardResult{port: p}
				_, _ = io.Copy(io.Discard, stdout) // drain until the process exits
				return
			}
		}
		if err := scanner.Err(); err != nil {
			resultCh <- portForwardResult{err: err}
			return
		}
		resultCh <- portForwardResult{err: io.EOF}
	}()
	select {
	case result := <-resultCh:
		if result.err == nil {
			diagnostics.startStreaming()
			return result.port, cmd, nil
		}
		stopProcess(cmd)
		return 0, nil, commandError("kubectl port-forward exited before binding", result.err, diagnostics.String())
	case <-ctx.Done():
		stopProcess(cmd)
		return 0, nil, commandError("kubectl port-forward", ctx.Err(), diagnostics.String())
	}
}

// deferredDiagnostics keeps startup errors available for the returned error
// without printing them twice. Once port-forward is ready, buffered warnings
// are flushed and later diagnostics stream to the caller in real time.
type deferredDiagnostics struct {
	mu        sync.Mutex
	dst       io.Writer
	buffer    bytes.Buffer
	streaming bool
}

func newDeferredDiagnostics(dst io.Writer) *deferredDiagnostics {
	if dst == nil {
		dst = io.Discard
	}
	return &deferredDiagnostics{dst: dst}
}

func (d *deferredDiagnostics) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.streaming {
		_, _ = d.dst.Write(p)
		return len(p), nil
	}
	return d.buffer.Write(p)
}

func (d *deferredDiagnostics) startStreaming() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.streaming {
		return
	}
	d.streaming = true
	if d.buffer.Len() > 0 {
		_, _ = d.dst.Write(d.buffer.Bytes())
		d.buffer.Reset()
	}
}

func (d *deferredDiagnostics) String() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.buffer.String()
}

func kubectlArgs(f *execFlags, args ...string) []string {
	global := make([]string, 0, 4+len(args))
	if f.kubeconfig != "" {
		global = append(global, "--kubeconfig", f.kubeconfig)
	}
	if f.kubeContext != "" {
		global = append(global, "--context", f.kubeContext)
	}
	return append(global, args...)
}

func commandError(action string, err error, stderr string) error {
	if detail := strings.TrimSpace(stderr); detail != "" {
		return fmt.Errorf("%s: %w: %s", action, err, detail)
	}
	return fmt.Errorf("%s: %w", action, err)
}

// stopProcess terminates the disposable kubectl port-forward subprocess and
// reaps it through exec.Cmd. Process.Kill is portable across supported
// platforms, unlike sending SIGTERM. startPortForward sets Cmd.WaitDelay so
// inherited pipes cannot leave this cleanup blocked indefinitely.
func stopProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
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
	client := &http.Client{Timeout: 750 * time.Millisecond}
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
		} else {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("healthz returned %s", resp.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	if lastErr == nil {
		lastErr = errors.New("timed out")
	}
	return lastErr
}
