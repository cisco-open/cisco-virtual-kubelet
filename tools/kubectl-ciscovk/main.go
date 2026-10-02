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
//	kubectl ciscovk topology graph [-n <ns>] [--max-age DURATION]
//	    [-o table|json] [--require-complete]
//
// The topology graph is a read-only diagnostic built exclusively from the
// manager-owned acceptedNetwork snapshots on CiscoDevice status. It cannot
// change topology policy or authorize disruption.
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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	ciscov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
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
	case "topology":
		if len(args) >= 3 && (args[2] == "-h" || args[2] == "--help" || args[2] == "help") {
			usage(stderr, invocation)
			return 0
		}
		if len(args) < 3 || args[2] != "graph" {
			fmt.Fprintln(stderr, "error: topology requires the graph subcommand")
			return 2
		}
		for _, arg := range args[3:] {
			if arg == "-h" || arg == "--help" {
				usage(stderr, invocation)
				return 0
			}
		}
		if err := runTopologyGraphWithIO(args[3:], stdout); err != nil {
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

  topology graph [-n <ns>] [flags]
    Build a bounded read-only graph from manager-accepted CiscoDevice network
    observations. The result is diagnostic only and never grants rollout
    authority. All namespaces are read by default so duplicate physical
    identities cannot be hidden by namespace boundaries.

Examples:
  {{COMMAND}} exec cat9k-smoke -n cisco-vk-smoke -- show ip route
  {{COMMAND}} exec cat9k-smoke -- "show running-config | section interface"
  {{COMMAND}} exec cat9k-smoke --allow-secrets -- show running-config
  {{COMMAND}} topology graph --max-age 5m
  {{COMMAND}} topology graph -n cvk-live -o json --require-complete

Flags for exec:
  -n, --namespace <ns>     namespace of the per-device kubelet pod
  --allow-secrets          currently a no-op on the server; reserved for future SAR-gated path
  --truncate-bytes N       cap each command's output (default 64 KiB; 0 disables)
  --port N                 local port for port-forward (default: random free)
  --timeout DURATION       overall timeout (default 30s)
  --context NAME           kubeconfig context to use
  --kubeconfig PATH        path to the kubeconfig file
  --kubectl PATH           path to kubectl binary (default: from PATH)`
	text += `

Flags for topology graph:
  -n, --namespace <ns>     limit the graph to one namespace (default: all)
  --max-age DURATION       maximum accepted collection age (default 5m)
  -o, --output FORMAT      table or json (default table)
  --require-complete       return an error when the graph is incomplete
  --context NAME           kubeconfig context to use
  --kubeconfig PATH        path to the kubeconfig file
  --kubectl PATH           path to kubectl binary (default: from PATH)`
	fmt.Fprintln(w, strings.ReplaceAll(text, "{{COMMAND}}", invocation))
}

type topologyGraphFlags struct {
	namespace       string
	maxAge          time.Duration
	output          string
	requireComplete bool
	kubectlBin      string
	kubeContext     string
	kubeconfig      string
}

func parseTopologyGraphArgs(argv []string) (*topologyGraphFlags, error) {
	f := &topologyGraphFlags{maxAge: 5 * time.Minute, output: "table", kubectlBin: "kubectl"}
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "-n", "--namespace":
			i++
			if i >= len(argv) || strings.TrimSpace(argv[i]) == "" {
				return nil, errors.New("-n/--namespace requires a value")
			}
			f.namespace = argv[i]
		case "--max-age":
			i++
			if i >= len(argv) {
				return nil, errors.New("--max-age requires a value")
			}
			d, err := time.ParseDuration(argv[i])
			if err != nil || d <= 0 {
				return nil, fmt.Errorf("--max-age must be a positive duration: %q", argv[i])
			}
			f.maxAge = d
		case "-o", "--output":
			i++
			if i >= len(argv) {
				return nil, errors.New("-o/--output requires a value")
			}
			f.output = strings.ToLower(argv[i])
			if f.output != "table" && f.output != "json" {
				return nil, fmt.Errorf("unsupported topology graph output %q; use table or json", argv[i])
			}
		case "--require-complete":
			f.requireComplete = true
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
		case "--kubectl":
			i++
			if i >= len(argv) {
				return nil, errors.New("--kubectl requires a path")
			}
			f.kubectlBin = argv[i]
		default:
			return nil, fmt.Errorf("unknown topology graph flag %q", argv[i])
		}
	}
	return f, nil
}

type topologyGraphDocument struct {
	Complete       bool                       `json:"complete"`
	EvidenceHash   string                     `json:"evidenceHash"`
	ProvenanceHash string                     `json:"provenanceHash"`
	Observations   []topologyGraphProvenance  `json:"observations"`
	Nodes          []string                   `json:"nodes"`
	Edges          []topology.GraphEdge       `json:"edges"`
	Diagnostics    []topology.GraphDiagnostic `json:"diagnostics"`
}

type topologyGraphProvenance struct {
	Device              string `json:"device"`
	DeviceUID           string `json:"deviceUID,omitempty"`
	PhysicalID          string `json:"physicalID"`
	Accepted            bool   `json:"accepted"`
	WorkerPodUID        string `json:"workerPodUID,omitempty"`
	ProducerRevision    string `json:"producerRevision,omitempty"`
	SampleSequence      uint64 `json:"sampleSequence,omitempty"`
	CollectionStartedAt string `json:"collectionStartedAt,omitempty"`
	CollectionEndedAt   string `json:"collectionEndedAt,omitempty"`
	DeviceIdentityHash  string `json:"deviceIdentityHash,omitempty"`
}

func runTopologyGraphWithIO(argv []string, stdout io.Writer) error {
	f, err := parseTopologyGraphArgs(argv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	devices, err := readCiscoDevices(ctx, f)
	if err != nil {
		return err
	}
	observations, provenance := graphInputsFromDevices(devices)
	graph, err := topology.BuildGraph(observations, topology.GraphPolicy{Now: time.Now().UTC(), MaxObservationAge: f.maxAge})
	if err != nil {
		return err
	}
	provenanceHash := hashTopologyGraphProvenance(provenance)
	if f.output == "json" {
		document := topologyGraphDocument{
			Complete: graph.Complete, EvidenceHash: graph.EvidenceHash, ProvenanceHash: provenanceHash,
			Observations: provenance, Nodes: graph.Nodes, Edges: graph.Edges, Diagnostics: graph.Diagnostics,
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(document); err != nil {
			return fmt.Errorf("write topology graph: %w", err)
		}
	} else {
		writeTopologyGraphTable(stdout, graph, provenanceHash)
	}
	if f.requireComplete && !graph.Complete {
		return errors.New("topology graph is incomplete; inspect diagnostics")
	}
	return nil
}

func readCiscoDevices(ctx context.Context, f *topologyGraphFlags) ([]ciscov1.CiscoDevice, error) {
	kubectlPath, err := exec.LookPath(f.kubectlBin)
	if err != nil {
		return nil, fmt.Errorf("find kubectl: %w", err)
	}
	args := kubectlGlobalArgs(f.kubeconfig, f.kubeContext)
	args = append(args, "get", "ciscodevices.cisco.vk")
	if f.namespace == "" {
		args = append(args, "--all-namespaces")
	} else {
		args = append(args, "-n", f.namespace)
	}
	args = append(args, "-o", "json")
	cmd := commandContext(ctx, kubectlPath, args...)
	var output, diagnostics bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &diagnostics
	if err := cmd.Run(); err != nil {
		return nil, commandError("read CiscoDevices", err, diagnostics.String())
	}
	var list ciscov1.CiscoDeviceList
	if err := json.Unmarshal(output.Bytes(), &list); err != nil {
		return nil, fmt.Errorf("decode CiscoDevices: %w", err)
	}
	if len(list.Items) == 0 {
		return nil, errors.New("no CiscoDevices found in the selected scope")
	}
	return list.Items, nil
}

func graphFromDevices(devices []ciscov1.CiscoDevice, now time.Time, maxAge time.Duration) (topology.Graph, error) {
	observations, _ := graphInputsFromDevices(devices)
	return topology.BuildGraph(observations, topology.GraphPolicy{Now: now, MaxObservationAge: maxAge})
}

func graphInputsFromDevices(devices []ciscov1.CiscoDevice) ([]topology.GraphObservation, []topologyGraphProvenance) {
	observations := make([]topology.GraphObservation, 0, len(devices))
	provenance := make([]topologyGraphProvenance, 0, len(devices))
	for i := range devices {
		device := &devices[i]
		physicalID, identityReason := graphDeviceIdentity(device)
		proof := topologyGraphProvenance{
			Device: device.Namespace + "/" + device.Name, DeviceUID: string(device.UID),
			PhysicalID: physicalID,
		}
		observation := topology.GraphObservation{
			PhysicalID:    physicalID,
			Complete:      false,
			UnknownReason: "manager has not accepted a network observation",
		}
		if identityReason != "" {
			observation.UnknownReason = identityReason
		}
		if device.Status.HealthObservation != nil && device.Status.HealthObservation.AcceptedNetwork != nil {
			accepted := device.Status.HealthObservation.AcceptedNetwork
			proof.Accepted = true
			proof.WorkerPodUID = accepted.WorkerPodUID
			proof.ProducerRevision = accepted.ProducerRevision
			proof.SampleSequence = accepted.SampleSequence
			proof.CollectionStartedAt = timestampString(accepted.CollectionStartedAt.Time)
			proof.CollectionEndedAt = timestampString(accepted.CollectionEndedAt.Time)
			proof.DeviceIdentityHash = accepted.DeviceIdentityHash
			observation.Complete = accepted.Complete
			observation.UnknownReason = accepted.UnknownReason
			observation.ObservedAt = accepted.CollectionStartedAt.Time
			if observation.ObservedAt.IsZero() {
				observation.ObservedAt = accepted.ObservedAt.Time
			}
			observation.Neighbors = make([]topology.GraphNeighbor, 0, len(accepted.Neighbors))
			for _, neighbor := range accepted.Neighbors {
				observation.Neighbors = append(observation.Neighbors, topology.GraphNeighbor{
					Identity: neighbor.Identity, PeerID: neighbor.ID, Source: neighbor.Source,
					Interface: neighbor.Interface, RemoteInterface: neighbor.RemoteInterface,
					RoutingDomain: neighbor.RoutingDomain, State: neighbor.State,
				})
			}
		}
		observations = append(observations, observation)
		provenance = append(provenance, proof)
	}
	sort.Slice(provenance, func(i, j int) bool {
		if provenance[i].PhysicalID != provenance[j].PhysicalID {
			return provenance[i].PhysicalID < provenance[j].PhysicalID
		}
		return provenance[i].Device < provenance[j].Device
	})
	return observations, provenance
}

func timestampString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func hashTopologyGraphProvenance(provenance []topologyGraphProvenance) string {
	encoded, _ := json.Marshal(provenance)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest)
}

func graphDeviceIdentity(device *ciscov1.CiscoDevice) (string, string) {
	if device.Status.NodeIdentity != nil && strings.TrimSpace(device.Status.NodeIdentity.PhysicalIdentity) != "" {
		physicalID, err := topology.CanonicalPhysicalIdentity(device.Status.NodeIdentity.PhysicalIdentity)
		if err != nil {
			return unboundGraphIdentity(device), "CiscoDevice manager-bound physical identity is invalid"
		}
		return physicalID, ""
	}
	if strings.TrimSpace(device.Spec.PhysicalIdentity) != "" {
		physicalID, err := topology.CanonicalPhysicalIdentity(device.Spec.PhysicalIdentity)
		if err != nil {
			return unboundGraphIdentity(device), "CiscoDevice operator-declared physical identity is invalid"
		}
		return physicalID, ""
	}
	return unboundGraphIdentity(device), "CiscoDevice has no manager-bound physical identity"
}

func unboundGraphIdentity(device *ciscov1.CiscoDevice) string {
	object := device.Namespace + "/" + device.Name
	identity := "unbound:" + object
	if len(identity) <= topology.MaxGraphFieldLength {
		return identity
	}
	digest := sha256.Sum256([]byte(object))
	return fmt.Sprintf("unbound:sha256:%x", digest)
}

func writeTopologyGraphTable(w io.Writer, graph topology.Graph, provenanceHash string) {
	fmt.Fprintf(w, "Topology graph: complete=%t nodes=%d edges=%d diagnostics=%d evidence=%s provenance=%s\n",
		graph.Complete, len(graph.Nodes), len(graph.Edges), len(graph.Diagnostics), graph.EvidenceHash, provenanceHash)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if len(graph.Edges) > 0 {
		fmt.Fprintln(tw, "\nLOCAL\tPEER\tSOURCE\tINTERFACE\tDOMAIN\tSTATE")
		for _, edge := range graph.Edges {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", edge.Local, edge.Peer, edge.Source, edge.Interface, edge.RoutingDomain, edge.State)
		}
	}
	if len(graph.Diagnostics) > 0 {
		fmt.Fprintln(tw, "\nSEVERITY\tCODE\tLOCAL\tPEER\tMESSAGE")
		for _, diagnostic := range graph.Diagnostics {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", diagnostic.Severity, diagnostic.Code, diagnostic.Local, diagnostic.Peer, diagnostic.Message)
		}
	}
	_ = tw.Flush()
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
	return append(kubectlGlobalArgs(f.kubeconfig, f.kubeContext), args...)
}

func kubectlGlobalArgs(kubeconfig, kubeContext string) []string {
	global := make([]string, 0, 4)
	if kubeconfig != "" {
		global = append(global, "--kubeconfig", kubeconfig)
	}
	if kubeContext != "" {
		global = append(global, "--context", kubeContext)
	}
	return global
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
