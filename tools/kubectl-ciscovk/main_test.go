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

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ciscov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
)

func TestParseTopologyGraphArgs(t *testing.T) {
	f, err := parseTopologyGraphArgs([]string{
		"-n", "cvk-live", "-l", "topology.cisco.vk/managed=true", "--max-age", "90s", "-o", "json", "--require-complete",
		"--policy-configmap", "cisco-vk-system/cisco-vk-topology-policy",
		"--context", "lab", "--kubeconfig", "/tmp/lab.conf", "--kubectl", "/usr/bin/kubectl",
	})
	if err != nil {
		t.Fatalf("parseTopologyGraphArgs() error = %v", err)
	}
	if f.namespace != "cvk-live" || f.selector != "topology.cisco.vk/managed=true" || f.maxAge != 90*time.Second || f.output != "json" || !f.requireComplete ||
		f.policyConfigMap != "cisco-vk-system/cisco-vk-topology-policy" ||
		f.kubeContext != "lab" || f.kubeconfig != "/tmp/lab.conf" || f.kubectlBin != "/usr/bin/kubectl" {
		t.Fatalf("flags = %#v", f)
	}
	for _, argv := range [][]string{{"--max-age", "0s"}, {"-o", "yaml"}, {"--selector"}, {"--selector", "bad\nselector"}, {"--policy-configmap"}, {"--unknown"}} {
		if _, err := parseTopologyGraphArgs(argv); err == nil {
			t.Fatalf("parseTopologyGraphArgs(%q) succeeded", argv)
		}
	}
}

func TestTopologyGraphHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"kubectl-ciscovk", "topology", "graph", "--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("runCLI() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Flags for topology graph") || !strings.Contains(stderr.String(), "--require-complete") || !strings.Contains(stderr.String(), "--policy-configmap") {
		t.Fatalf("topology graph help = %q", stderr.String())
	}
}

func TestReadTopologyGraphPolicyRequiresProtectedConfigMap(t *testing.T) {
	previous := commandContext
	commandContext = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		helperArgs := append([]string{"-test.run=^TestGraphPolicyKubectlHelperProcess$", "--"}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], helperArgs...)
		cmd.Env = append(os.Environ(), "GO_WANT_CVK_GRAPH_POLICY_HELPER=1")
		return cmd
	}
	t.Cleanup(func() { commandContext = previous })

	document, proof, err := readTopologyGraphPolicy(context.Background(), &topologyGraphFlags{
		policyConfigMap: "cisco-vk-system/cisco-vk-topology-policy", kubectlBin: os.Args[0],
		kubeContext: "lab", kubeconfig: "/tmp/lab.conf",
	})
	if err != nil {
		t.Fatalf("readTopologyGraphPolicy() error = %v", err)
	}
	if proof == nil || proof.ConfigMap != "cisco-vk-system/cisco-vk-topology-policy" || proof.UID != "policy-uid" || !strings.HasPrefix(proof.ContentHash, "sha256:") {
		t.Fatalf("proof = %#v", proof)
	}
	if len(document.PeerMappings) != 1 || document.PeerMappings[0].PhysicalID != "serial-b" || len(document.DeclaredLinks) != 1 {
		t.Fatalf("document = %#v", document)
	}
	withoutPolicy, withoutProof, err := readTopologyGraphPolicy(context.Background(), &topologyGraphFlags{})
	if err != nil || withoutProof != nil || withoutPolicy.Version != "" {
		t.Fatalf("optional policy = %#v, %#v, %v", withoutPolicy, withoutProof, err)
	}
}

func TestGraphPolicyKubectlHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CVK_GRAPH_POLICY_HELPER") != "1" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 || separator == len(os.Args)-1 {
		fmt.Fprintln(os.Stderr, "missing helper arguments")
		os.Exit(2)
	}
	got := os.Args[separator+1:]
	want := []string{
		"--kubeconfig", "/tmp/lab.conf", "--context", "lab", "get", "configmap", "cisco-vk-topology-policy",
		"-n", "cisco-vk-system", "-o", "json",
	}
	if !slices.Equal(got, want) {
		fmt.Fprintf(os.Stderr, "unexpected graph policy kubectl arguments: %q\n", got)
		os.Exit(2)
	}
	fmt.Fprint(os.Stdout, `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"cisco-vk-topology-policy","namespace":"cisco-vk-system","uid":"policy-uid","resourceVersion":"42","annotations":{"topology.cisco.vk/managed-policy":"true","topology.cisco.vk/admission-contract-version":"v2"}},"data":{"graph.json":"{\"version\":\"v1\",\"peerMappings\":[{\"source\":\"cdp\",\"observedPeer\":\"C9K-2\",\"physicalID\":\"SERIAL-B\"}],\"declaredLinks\":[{\"local\":\"serial-a\",\"peer\":\"serial-b\",\"source\":\"cdp\",\"interface\":\"Gi1\",\"remoteInterface\":\"Gi2\"}]}"}}`)
	os.Exit(0)
}

func TestGraphFromDevicesUsesOnlyManagerAcceptedEvidence(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	acceptedAt := metav1.NewTime(now.Add(-time.Minute))
	devices := []ciscov1.CiscoDevice{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "leaf-a", Namespace: "cvk-live"},
			Spec:       ciscov1.DeviceSpec{PhysicalIdentity: "SERIAL-A"},
			Status: ciscov1.DeviceStatus{
				NodeIdentity: &ciscov1.DeviceNodeIdentityStatus{PhysicalIdentity: "serial-a"},
				HealthObservation: &ciscov1.DeviceHealthObservationStatus{
					Network: &ciscov1.DeviceNetworkObservationStatus{Complete: false, UnknownReason: "untrusted raw sample"},
					AcceptedNetwork: &ciscov1.DeviceNetworkObservationStatus{
						CollectionStartedAt: acceptedAt,
						ObservedAt:          acceptedAt,
						Complete:            true,
						Neighbors: []ciscov1.DeviceNetworkNeighborObservation{{
							Identity: "cdp-a-b", ID: "serial-b", Source: "cdp", Interface: "Gi1/0/1",
							RemoteInterface: "Gi1/0/2", State: "up",
						}},
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "leaf-b", Namespace: "cvk-live"},
			Spec:       ciscov1.DeviceSpec{PhysicalIdentity: "SERIAL-B"},
			Status: ciscov1.DeviceStatus{
				NodeIdentity: &ciscov1.DeviceNodeIdentityStatus{PhysicalIdentity: "serial-b"},
				HealthObservation: &ciscov1.DeviceHealthObservationStatus{AcceptedNetwork: &ciscov1.DeviceNetworkObservationStatus{
					CollectionStartedAt: acceptedAt, ObservedAt: acceptedAt, Complete: true,
				}},
			},
		},
	}
	graph, err := graphFromDevices(devices, now, 5*time.Minute)
	if err != nil {
		t.Fatalf("graphFromDevices() error = %v", err)
	}
	if len(graph.Nodes) != 2 || len(graph.Edges) != 1 || graph.Edges[0].Local != "serial-a" || graph.Edges[0].Peer != "serial-b" || graph.Edges[0].RemoteInterface != "Gi1/0/2" {
		t.Fatalf("graph = %#v", graph)
	}
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Message == "untrusted raw sample" {
			t.Fatalf("raw worker evidence entered graph diagnostics: %#v", graph.Diagnostics)
		}
	}
}

func TestGraphProvenanceIsDeterministicAndAcceptedOnly(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 123, time.UTC)
	started := metav1.NewTime(now.Add(-time.Second))
	ended := metav1.NewTime(now)
	devices := []ciscov1.CiscoDevice{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "leaf-b", Namespace: "cvk-live", UID: "device-b", ResourceVersion: "12"},
			Spec:       ciscov1.DeviceSpec{PhysicalIdentity: "serial-b"},
			Status: ciscov1.DeviceStatus{HealthObservation: &ciscov1.DeviceHealthObservationStatus{
				Network: &ciscov1.DeviceNetworkObservationStatus{WorkerPodUID: "raw-must-not-appear", ProducerRevision: "raw"},
				AcceptedNetwork: &ciscov1.DeviceNetworkObservationStatus{
					WorkerPodUID: "accepted-pod", ProducerRevision: "sha256:accepted", SampleSequence: 7,
					CollectionStartedAt: started, CollectionEndedAt: ended, DeviceIdentityHash: "sha256:device",
				},
			}},
		},
		{ObjectMeta: metav1.ObjectMeta{Name: "leaf-a", Namespace: "cvk-live"}, Spec: ciscov1.DeviceSpec{PhysicalIdentity: "serial-a"}},
	}
	_, provenance := graphInputsFromDevices(devices)
	if len(provenance) != 2 || provenance[0].PhysicalID != "serial-a" || provenance[1].WorkerPodUID != "accepted-pod" {
		t.Fatalf("provenance = %#v", provenance)
	}
	if strings.Contains(string(mustJSON(t, provenance)), "raw-must-not-appear") {
		t.Fatalf("raw worker sample entered provenance: %#v", provenance)
	}
	wantHash := hashTopologyGraphProvenance(provenance)
	sort.Slice(devices, func(i, j int) bool { return devices[i].Name < devices[j].Name })
	_, reordered := graphInputsFromDevices(devices)
	if got := hashTopologyGraphProvenance(reordered); got != wantHash {
		t.Fatalf("provenance hash changed after input reorder: got %s want %s", got, wantHash)
	}
	policy := &topologyGraphPolicyProof{ConfigMap: "system/policy", UID: "uid-1", ResourceVersion: "42", ContentHash: "sha256:policy"}
	withPolicy := hashTopologyGraphProvenance(reordered, policy)
	if withPolicy == wantHash || withPolicy != hashTopologyGraphProvenance(provenance, policy) {
		t.Fatalf("policy provenance was absent or nondeterministic: base=%s withPolicy=%s", wantHash, withPolicy)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestGraphFromDevicesFailsClosedWithoutAcceptedEvidence(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	devices := []ciscov1.CiscoDevice{{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-a", Namespace: "cvk-live"},
		Spec:       ciscov1.DeviceSpec{PhysicalIdentity: "serial-a"},
		Status: ciscov1.DeviceStatus{HealthObservation: &ciscov1.DeviceHealthObservationStatus{
			Network: &ciscov1.DeviceNetworkObservationStatus{Complete: true, ObservedAt: metav1.NewTime(now)},
		}},
	}}
	graph, err := graphFromDevices(devices, now, 5*time.Minute)
	if err != nil {
		t.Fatalf("graphFromDevices() error = %v", err)
	}
	if graph.Complete {
		t.Fatalf("unaccepted worker sample produced complete graph: %#v", graph)
	}
	want := map[string]bool{"ObservationTimeMissing": false, "IncompleteObservation": false}
	for _, diagnostic := range graph.Diagnostics {
		if _, ok := want[diagnostic.Code]; ok {
			want[diagnostic.Code] = true
		}
	}
	for code, found := range want {
		if !found {
			t.Fatalf("missing %s diagnostic: %#v", code, graph.Diagnostics)
		}
	}
}

func TestGraphFromDevicesReportsUnboundDeviceWithoutHidingFleet(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	graph, err := graphFromDevices([]ciscov1.CiscoDevice{{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: "cvk-live"},
	}}, now, 5*time.Minute)
	if err != nil {
		t.Fatalf("graphFromDevices() error = %v", err)
	}
	if graph.Complete || len(graph.Nodes) != 1 || graph.Nodes[0] != "unbound:cvk-live/legacy" {
		t.Fatalf("graph = %#v", graph)
	}
	found := false
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "IncompleteObservation" && strings.Contains(diagnostic.Message, "no manager-bound physical identity") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing unbound-device diagnostic: %#v", graph.Diagnostics)
	}
}

func TestGraphDeviceIdentityCanonicalizesAndFailsClosed(t *testing.T) {
	device := &ciscov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-a", Namespace: "cvk-live"},
		Spec:       ciscov1.DeviceSpec{PhysicalIdentity: "SERIAL-SPEC"},
		Status: ciscov1.DeviceStatus{NodeIdentity: &ciscov1.DeviceNodeIdentityStatus{
			PhysicalIdentity: "SERIAL-BOUND",
		}},
	}
	if got, reason := graphDeviceIdentity(device); got != "serial-bound" || reason != "" {
		t.Fatalf("graphDeviceIdentity() = %q, %q", got, reason)
	}
	device.Status.NodeIdentity.PhysicalIdentity = "invalid identity"
	if got, reason := graphDeviceIdentity(device); !strings.HasPrefix(got, "unbound:") || !strings.Contains(reason, "manager-bound") {
		t.Fatalf("invalid bound identity fell back to spec: %q, %q", got, reason)
	}
	device.Status.NodeIdentity = nil
	if got, reason := graphDeviceIdentity(device); got != "serial-spec" || reason != "" {
		t.Fatalf("canonical spec fallback = %q, %q", got, reason)
	}
}

func TestUnboundGraphIdentityIsBounded(t *testing.T) {
	device := &ciscov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{
		Namespace: strings.Repeat("n", 63),
		Name:      strings.Repeat("d", 253),
	}}
	got := unboundGraphIdentity(device)
	if len(got) > topology.MaxGraphFieldLength || !strings.HasPrefix(got, "unbound:sha256:") {
		t.Fatalf("unboundGraphIdentity() = %q (%d bytes)", got, len(got))
	}
}

func TestReadCiscoDevicesUsesScopedKubectlJSON(t *testing.T) {
	previous := commandContext
	commandContext = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		helperArgs := append([]string{"-test.run=^TestGraphKubectlHelperProcess$", "--"}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], helperArgs...)
		cmd.Env = append(os.Environ(), "GO_WANT_CVK_GRAPH_KUBECTL_HELPER=1")
		return cmd
	}
	t.Cleanup(func() { commandContext = previous })

	devices, err := readCiscoDevices(context.Background(), &topologyGraphFlags{
		namespace: "cvk-live", kubectlBin: os.Args[0], kubeContext: "lab", kubeconfig: "/tmp/lab.conf",
	})
	if err != nil {
		t.Fatalf("readCiscoDevices() error = %v", err)
	}
	if len(devices) != 1 || devices[0].Name != "leaf-a" {
		t.Fatalf("devices = %#v", devices)
	}
}

func TestGraphKubectlHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CVK_GRAPH_KUBECTL_HELPER") != "1" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 || separator == len(os.Args)-1 {
		fmt.Fprintln(os.Stderr, "missing helper arguments")
		os.Exit(2)
	}
	got := os.Args[separator+1:]
	want := []string{
		"--kubeconfig", "/tmp/lab.conf", "--context", "lab", "get", "ciscodevices.cisco.vk",
		"-n", "cvk-live", "-o", "json",
	}
	if !slices.Equal(got, want) {
		fmt.Fprintf(os.Stderr, "unexpected graph kubectl arguments: %q\n", got)
		os.Exit(2)
	}
	fmt.Fprint(os.Stdout, `{"apiVersion":"cisco.vk/v1alpha1","kind":"CiscoDeviceList","items":[{"apiVersion":"cisco.vk/v1alpha1","kind":"CiscoDevice","metadata":{"name":"leaf-a","namespace":"cvk-live"},"spec":{"physicalIdentity":"serial-a","address":"192.0.2.10","driver":"XE"}}]}`)
	os.Exit(0)
}

func TestParseExecArgs(t *testing.T) {
	cases := []struct {
		name       string
		argv       []string
		wantErr    string
		wantDev    string
		wantNS     string
		wantAllow  bool
		wantTrunc  int
		wantCtx    string
		wantConfig string
		wantCmds   []string
	}{
		{
			name:      "minimal",
			argv:      []string{"cat9k-smoke", "--", "show", "ip", "route"},
			wantDev:   "cat9k-smoke",
			wantCmds:  []string{"show ip route"},
			wantTrunc: 64 * 1024,
		},
		{
			name:      "with namespace",
			argv:      []string{"cat9k-smoke", "-n", "cisco-vk-smoke", "--", "show", "version"},
			wantDev:   "cat9k-smoke",
			wantNS:    "cisco-vk-smoke",
			wantCmds:  []string{"show version"},
			wantTrunc: 64 * 1024,
		},
		{
			name:      "allow secrets + truncate",
			argv:      []string{"cat9k-smoke", "--allow-secrets", "--truncate-bytes", "1024", "--", "show", "running-config"},
			wantDev:   "cat9k-smoke",
			wantAllow: true,
			wantTrunc: 1024,
			wantCmds:  []string{"show running-config"},
		},
		{
			name:       "explicit kube target",
			argv:       []string{"cat9k-smoke", "--context", "lab", "--kubeconfig", "/tmp/lab.conf", "--", "show", "version"},
			wantDev:    "cat9k-smoke",
			wantCtx:    "lab",
			wantConfig: "/tmp/lab.conf",
			wantTrunc:  64 * 1024,
			wantCmds:   []string{"show version"},
		},
		{
			name:    "no command",
			argv:    []string{"cat9k-smoke"},
			wantErr: "missing command",
		},
		{
			name:    "no device",
			argv:    []string{"--", "show", "version"},
			wantErr: "missing device",
		},
		{
			name:    "rejects reload",
			argv:    []string{"cat9k-smoke", "--", "reload"},
			wantErr: "destructive command",
		},
		{
			name:    "rejects clear",
			argv:    []string{"cat9k-smoke", "--", "clear", "ip", "ospf", "process"},
			wantErr: "destructive command",
		},
		{
			name:    "rejects write erase",
			argv:    []string{"cat9k-smoke", "--", "write", "erase"},
			wantErr: "destructive command",
		},
		{
			name:    "context requires value",
			argv:    []string{"cat9k-smoke", "--context"},
			wantErr: "--context requires a name",
		},
		{
			name:    "kubeconfig requires value",
			argv:    []string{"cat9k-smoke", "--kubeconfig"},
			wantErr: "--kubeconfig requires a path",
		},
		{
			name:    "rejects empty command",
			argv:    []string{"cat9k-smoke", "--"},
			wantErr: "must not be empty",
		},
		{
			name:    "rejects non-positive timeout",
			argv:    []string{"cat9k-smoke", "--timeout", "0s", "--", "show version"},
			wantErr: "greater than zero",
		},
		{
			name:    "rejects invalid port",
			argv:    []string{"cat9k-smoke", "--port", "65536", "--", "show version"},
			wantErr: "between 0 and 65535",
		},
		{
			name:    "rejects overflowing integer",
			argv:    []string{"cat9k-smoke", "--truncate-bytes", "999999999999999999999", "--", "show version"},
			wantErr: "non-negative integer",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := parseExecArgs(tc.argv)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err=%v want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if f.device != tc.wantDev {
				t.Errorf("device=%q want %q", f.device, tc.wantDev)
			}
			if f.namespace != tc.wantNS {
				t.Errorf("namespace=%q want %q", f.namespace, tc.wantNS)
			}
			if f.allowSecrets != tc.wantAllow {
				t.Errorf("allowSecrets=%v want %v", f.allowSecrets, tc.wantAllow)
			}
			if f.truncateB != tc.wantTrunc {
				t.Errorf("truncateB=%d want %d", f.truncateB, tc.wantTrunc)
			}
			if f.kubeContext != tc.wantCtx {
				t.Errorf("kubeContext=%q want %q", f.kubeContext, tc.wantCtx)
			}
			if f.kubeconfig != tc.wantConfig {
				t.Errorf("kubeconfig=%q want %q", f.kubeconfig, tc.wantConfig)
			}
			if !equalStrings(f.commands, tc.wantCmds) {
				t.Errorf("commands=%v want %v", f.commands, tc.wantCmds)
			}
		})
	}
}

func TestPrintVersion(t *testing.T) {
	oldVersion, oldCommit, oldBuildTime := Version, GitCommit, BuildTime
	Version, GitCommit, BuildTime = "v2026.9.0", "abc1234", "2026-09-01T07:00:00Z"
	t.Cleanup(func() {
		Version, GitCommit, BuildTime = oldVersion, oldCommit, oldBuildTime
	})

	var got bytes.Buffer
	printVersion(&got)
	want := "kubectl-ciscovk v2026.9.0 (commit=abc1234, built=2026-09-01T07:00:00Z)\n"
	if got.String() != want {
		t.Fatalf("version output = %q, want %q", got.String(), want)
	}
}

func TestRunCLIUsesInstallSpecificInvocationInHelp(t *testing.T) {
	tests := []struct {
		name    string
		argv0   string
		command string
	}{
		{name: "Krew alias", argv0: "/tmp/kubectl-cisco_vk", command: "kubectl cisco-vk"},
		{name: "release binary", argv0: "/tmp/kubectl-ciscovk", command: "kubectl ciscovk"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runCLI([]string{test.argv0, "--help"}, &stdout, &stderr); code != 0 {
				t.Fatalf("runCLI() code = %d, want 0", code)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if !strings.Contains(stderr.String(), test.command+" exec") {
				t.Fatalf("help = %q, want invocation %q", stderr.String(), test.command)
			}
		})
	}
}

func TestKubectlArgs(t *testing.T) {
	f := &execFlags{kubeconfig: "/tmp/lab.conf", kubeContext: "lab"}
	got := kubectlArgs(f, "get", "pod")
	want := []string{"--kubeconfig", "/tmp/lab.conf", "--context", "lab", "get", "pod"}
	if !slices.Equal(got, want) {
		t.Fatalf("kubectlArgs() = %q, want %q", got, want)
	}
}

func TestDeferredDiagnostics(t *testing.T) {
	var live bytes.Buffer
	diagnostics := newDeferredDiagnostics(&live)
	if _, err := diagnostics.Write([]byte("startup warning\n")); err != nil {
		t.Fatal(err)
	}
	if live.Len() != 0 {
		t.Fatalf("startup diagnostics streamed before readiness: %q", live.String())
	}
	if got := diagnostics.String(); got != "startup warning\n" {
		t.Fatalf("buffered diagnostics = %q", got)
	}

	diagnostics.startStreaming()
	if _, err := diagnostics.Write([]byte("lost connection\n")); err != nil {
		t.Fatal(err)
	}
	if got, want := live.String(), "startup warning\nlost connection\n"; got != want {
		t.Fatalf("live diagnostics = %q, want %q", got, want)
	}
	if diagnostics.String() != "" {
		t.Fatalf("startup buffer was not released after streaming")
	}
}

func TestRunExecWithFakeKubectl(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/v1/exec":
			var request struct {
				Commands     []string `json:"commands"`
				AllowSecrets bool     `json:"allowSecrets"`
				TruncateB    int      `json:"truncateBytes"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if !slices.Equal(request.Commands, []string{"show version"}) || request.AllowSecrets || request.TruncateB != 4096 {
				http.Error(w, fmt.Sprintf("unexpected request: %+v", request), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"device":"cat9k-smoke","transport":"gnoi","capturedAt":"2026-09-01T07:00:00Z","results":[{"command":"show version","output":"Cisco IOS XE"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	installFakeKubectl(t, "success", portText)

	var stdout, stderr bytes.Buffer
	err = runExecWithIO([]string{
		"cat9k-smoke",
		"-n", "cvk-system",
		"--context", "lab",
		"--kubeconfig", "/tmp/lab.conf",
		"--kubectl", os.Args[0],
		"--truncate-bytes", "4096",
		"--", "show", "version",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runExecWithIO() error = %v", err)
	}
	if want := "# device=cat9k-smoke transport=gnoi captured=2026-09-01T07:00:00Z\nCisco IOS XE\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestStartPortForwardReportsKubectlFailure(t *testing.T) {
	installFakeKubectl(t, "failure", "")
	f := &execFlags{
		device:      "cat9k-smoke",
		namespace:   "cvk-system",
		kubectlBin:  os.Args[0],
		kubeContext: "lab",
		kubeconfig:  "/tmp/lab.conf",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := startPortForward(ctx, f, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "simulated port-forward failure") {
		t.Fatalf("startPortForward() error = %v, want helper stderr", err)
	}
}

func installFakeKubectl(t *testing.T, mode, port string) {
	t.Helper()
	previous := commandContext
	commandContext = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		helperArgs := append([]string{"-test.run=^TestKubectlHelperProcess$", "--"}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], helperArgs...)
		cmd.Env = append(os.Environ(),
			"GO_WANT_CVK_KUBECTL_HELPER=1",
			"CVK_KUBECTL_HELPER_MODE="+mode,
			"CVK_KUBECTL_HELPER_PORT="+port,
		)
		return cmd
	}
	t.Cleanup(func() { commandContext = previous })
}

func TestKubectlHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CVK_KUBECTL_HELPER") != "1" {
		return
	}
	args := os.Args
	separator := slices.Index(args, "--")
	if separator < 0 || separator == len(args)-1 {
		fmt.Fprintln(os.Stderr, "missing helper arguments")
		os.Exit(2)
	}
	args = args[separator+1:]

	global := []string{"--kubeconfig", "/tmp/lab.conf", "--context", "lab"}
	if len(args) < len(global) || !slices.Equal(args[:len(global)], global) {
		fmt.Fprintf(os.Stderr, "unexpected global arguments: %q\n", args)
		os.Exit(2)
	}
	args = args[len(global):]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "missing kubectl command")
		os.Exit(2)
	}

	switch args[0] {
	case "get":
		want := []string{"get", "pod", "-n", "cvk-system", "-l", "app.kubernetes.io/instance=cat9k-smoke", "-o", "jsonpath={.items[0].metadata.name}"}
		if !slices.Equal(args, want) {
			fmt.Fprintf(os.Stderr, "unexpected get arguments: %q\n", args)
			os.Exit(2)
		}
		fmt.Fprint(os.Stdout, "cat9k-smoke-0")
		os.Exit(0)
	case "port-forward":
		want := []string{"port-forward", "-n", "cvk-system", "cat9k-smoke-0", "--address=127.0.0.1", ":8082"}
		if !slices.Equal(args, want) {
			fmt.Fprintf(os.Stderr, "unexpected port-forward arguments: %q\n", args)
			os.Exit(2)
		}
		if os.Getenv("CVK_KUBECTL_HELPER_MODE") == "failure" {
			fmt.Fprintln(os.Stderr, "simulated port-forward failure")
			os.Exit(3)
		}
		port, err := strconv.Atoi(os.Getenv("CVK_KUBECTL_HELPER_PORT"))
		if err != nil || port == 0 {
			fmt.Fprintln(os.Stderr, "invalid helper port")
			os.Exit(2)
		}
		fmt.Fprintf(os.Stdout, "Forwarding from 127.0.0.1:%d -> 8082\n", port)
		for {
			time.Sleep(time.Hour)
		}
	default:
		fmt.Fprintf(os.Stderr, "unexpected kubectl command: %q\n", args)
		os.Exit(2)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestParseForwardingPort(t *testing.T) {
	cases := map[string]int{
		"":      0,
		"hello": 0,
		"Forwarding from 127.0.0.1:51234 -> 8082": 51234,
		"Forwarding from [::1]:51235 -> 8082":     0, // we only match IPv4
		"127.0.0.1:9999":                          9999,
		"Forwarding from 127.0.0.1:0 -> 8082":     0, // 0 is a sentinel
	}
	for in, want := range cases {
		got := parseForwardingPort(in)
		if got != want {
			t.Errorf("parseForwardingPort(%q)=%d want %d", in, got, want)
		}
	}
}
