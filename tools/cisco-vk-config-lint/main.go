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

// Command cisco-vk-config-lint is a live drift reporter. It
// connects to an IOS-XE device, reads every family the driver
// knows, and compares against the IOSXEConfig CRs the caller
// supplies. Two dimensions are reported:
//
//   - Managed drift: families claimed by a CR whose device state
//     has diverged from the declared intent — "what CVK would
//     change on the next reconcile".
//
//   - Device orphans: registered families with non-empty device
//     state that no CR claims — "what is on the device that CVK
//     will not touch".
//
// This replaces the tool's earlier role as an offline YAML
// validator. Static schema validation (YAML shape, family-set
// membership, per-leaf type checks) is deliberately delegated to
// upstream nac-validate.
//
// Typical usage:
//
//	# CI gate: exit non-zero if anything would change on next reconcile
//	cisco-vk-config-lint \
//	  --address 192.0.2.10 --username cisco-vk --password-env DEV_PASSWORD \
//	  --device-name edge-01 \
//	  --exit-on-drift \
//	  ./manifests/edge-01/
//
//	# Ad-hoc orphan hunt before switching driftPolicy=revert
//	cisco-vk-config-lint \
//	  --address 192.0.2.10 --username cisco-vk --password-env DEV_PASSWORD \
//	  --device-name edge-01 \
//	  --mode=orphans \
//	  ./manifests/edge-01/
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

type exitCode int

const (
	exitOK       exitCode = 0
	exitBadFlags exitCode = 2
	exitBadInput exitCode = 3
	exitFindings exitCode = 4 // drift or orphans when --exit-on-drift is set
	exitInternal exitCode = 5
)

type reportMode string

const (
	modeFull    reportMode = "full"
	modeDrift   reportMode = "drift"
	modeOrphans reportMode = "orphans"
)

type flags struct {
	// Device connection.
	address     string
	port        int
	username    string
	password    string
	passwordEnv string
	scheme      string
	insecure    bool
	timeout     time.Duration

	// Target CR set — file mode.
	deviceName string
	crPaths    []string

	// Target CR set — cluster mode.
	fromCluster   bool
	kubeconfig    string
	namespace     string
	allNamespaces bool

	// What to report.
	mode    reportMode
	ignored string

	// Output.
	output      string // "human" | "json"
	exitOnDrift bool

	// Offline plan — no device contact. Mutually exclusive with the
	// connected modes' --address/--username/etc. requirements.
	offline bool
}

func parseFlags(args []string, stderr io.Writer) (flags, error) {
	fs := flag.NewFlagSet("cisco-vk-config-lint", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var f flags
	fs.StringVar(&f.address, "address", "", "device management IP or hostname (required)")
	fs.IntVar(&f.port, "port", 443, "RESTCONF port")
	fs.StringVar(&f.username, "username", "", "RESTCONF username (required)")
	fs.StringVar(&f.password, "password", "",
		"RESTCONF password; prefer --password-env to keep secrets out of process-listings")
	fs.StringVar(&f.passwordEnv, "password-env", "CVK_CONFIG_LINT_PASSWORD",
		"environment variable holding the RESTCONF password when --password is not set")
	fs.StringVar(&f.scheme, "scheme", "https",
		"URL scheme; 'http' disables TLS entirely (for test fixtures only)")
	fs.BoolVar(&f.insecure, "insecure", false, "skip TLS certificate verification")
	fs.DurationVar(&f.timeout, "timeout", 30*time.Second, "per-family RESTCONF timeout")

	fs.StringVar(&f.deviceName, "device-name", "",
		"CiscoDevice name the loaded IOSXEConfig CRs must target (required)")

	fs.BoolVar(&f.fromCluster, "from-cluster", false,
		"read IOSXEConfig CRs from a Kubernetes cluster instead of local YAML paths")
	fs.StringVar(&f.kubeconfig, "kubeconfig", "",
		"path to kubeconfig (cluster mode only; falls back to $KUBECONFIG, in-cluster, then $HOME/.kube/config)")
	fs.StringVar(&f.namespace, "namespace", "",
		"namespace to read IOSXEConfigs from in cluster mode; defaults to the kubeconfig context's namespace")
	fs.BoolVar(&f.allNamespaces, "all-namespaces", false,
		"read IOSXEConfigs across every namespace in cluster mode; overrides --namespace")

	var modeStr string
	fs.StringVar(&modeStr, "mode", "full",
		"report dimensions: 'drift' (managed only), 'orphans' (unmanaged only), or 'full'")
	fs.StringVar(&f.ignored, "ignore-families", "",
		"comma-separated family names to skip (useful when a family is intentionally out of CVK scope on this device)")

	fs.StringVar(&f.output, "output", "human", "'human' or 'json'")
	fs.BoolVar(&f.exitOnDrift, "exit-on-drift", false,
		"exit with code 4 when any managed drift or orphans are found — for CI gating")
	fs.BoolVar(&f.offline, "offline", false,
		"skip device contact; emit the ops the engine would push if the device were empty (orphan detection is unavailable in this mode)")

	if err := fs.Parse(args); err != nil {
		return f, err
	}
	f.crPaths = fs.Args()

	switch reportMode(modeStr) {
	case modeFull, modeDrift, modeOrphans:
		f.mode = reportMode(modeStr)
	default:
		return f, fmt.Errorf("invalid --mode %q (want drift|orphans|full)", modeStr)
	}
	if f.output != "human" && f.output != "json" {
		return f, fmt.Errorf("invalid --output %q (want human|json)", f.output)
	}
	if f.password == "" {
		f.password = os.Getenv(f.passwordEnv)
	}
	return f, nil
}

func run(args []string, stdout, stderr io.Writer) exitCode {
	f, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return exitBadFlags
	}

	if f.offline {
		// Offline plan needs a device-name (so we know which CRs
		// to load) but skips the connection bits entirely.
		if f.deviceName == "" {
			fmt.Fprintln(stderr, "ERROR: --device-name is required in --offline mode")
			return exitBadFlags
		}
		if f.mode == modeOrphans {
			fmt.Fprintln(stderr, "ERROR: --mode=orphans requires a device read; remove --offline or change mode")
			return exitBadFlags
		}
	} else if f.address == "" || f.username == "" || f.deviceName == "" {
		fmt.Fprintln(stderr, "ERROR: --address, --username, and --device-name are required")
		return exitBadFlags
	}
	if f.fromCluster && len(f.crPaths) > 0 {
		fmt.Fprintln(stderr, "ERROR: positional CR paths and --from-cluster are mutually exclusive")
		return exitBadFlags
	}
	if !f.fromCluster && len(f.crPaths) == 0 {
		fmt.Fprintln(stderr, "ERROR: supply at least one IOSXEConfig YAML path or directory, or pass --from-cluster")
		return exitBadFlags
	}

	ctx, cancel := context.WithCancel(context.Background()) // ctxlint:allow CLI process root
	defer cancel()

	// Discover and load CRs. An empty match set is not an error —
	// it means every non-empty family on the device will appear as
	// an orphan, which is exactly what an operator asking "what am
	// I about to manage?" wants to see.
	var crs []loadedCR
	if f.fromCluster {
		cfg, err := resolveKubeconfig(f.kubeconfig)
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: kubeconfig: %v\n", err)
			return exitBadInput
		}
		crs, err = loadCRsFromCluster(ctx, cfg, f.namespace, f.allNamespaces, f.deviceName, nil)
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: load CRs from cluster: %v\n", err)
			return exitBadInput
		}
	} else {
		files, err := discoverCRFiles(f.crPaths)
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: walk CR paths: %v\n", err)
			return exitBadInput
		}
		crs, err = loadCRsFromFiles(files, f.deviceName)
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: load CRs: %v\n", err)
			return exitBadInput
		}
	}

	inputs := buildDriftInputs(f.deviceName, crs)
	ignored := parseIgnored(f.ignored)

	var report Report
	if f.offline {
		report = computeOfflinePlan(inputs, ignored)
	} else {
		t, err := buildTransport(f)
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: build transport: %v\n", err)
			return exitInternal
		}
		report = computeReport(ctx, t, inputs, ignored)
	}

	// Filter by mode — --mode is purely presentational, not
	// computational. Every dimension is still checked; the filter
	// only decides what the operator sees. This keeps an
	// --exit-on-drift gate consistent regardless of --mode.
	presented := filterReport(report, f.mode)

	switch f.output {
	case "json":
		if err := renderJSON(stdout, presented); err != nil {
			fmt.Fprintf(stderr, "ERROR: render json: %v\n", err)
			return exitInternal
		}
	default:
		renderHuman(stdout, presented)
	}

	if f.exitOnDrift && report.HasFindings() {
		return exitFindings
	}
	return exitOK
}

// buildTransport composes the RESTCONF transport. A lint run is a
// one-shot so no session lock is needed; apphosting isn't competing
// for the device.
func buildTransport(f flags) (transport.Interface, error) {
	scheme := f.scheme
	if scheme == "" {
		scheme = "https"
	}
	httpClient := &http.Client{Timeout: f.timeout}
	if scheme == "https" && f.insecure {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	baseURL := fmt.Sprintf("%s://%s:%d/restconf/data", scheme, f.address, f.port)
	return transport.NewRESTCONF(transport.RESTCONFConfig{
		BaseURL:    baseURL,
		HTTPClient: httpClient,
		Username:   f.username,
		Password:   f.password,
	})
}

// parseIgnored splits a comma-separated list into a lookup set.
// Whitespace-only entries and duplicates are tolerated.
func parseIgnored(csv string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, part := range strings.Split(csv, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		out[p] = struct{}{}
	}
	return out
}

// filterReport returns a copy of r with the sections relevant to mode.
// Errors are retained in every mode — a Fetch failure is always worth
// surfacing. Summary / device / managedFamilies are preserved so JSON
// consumers see a consistent envelope shape.
func filterReport(r Report, mode reportMode) Report {
	out := Report{
		Device:          r.Device,
		ManagedFamilies: r.ManagedFamilies,
		Errors:          r.Errors,
	}
	switch mode {
	case modeDrift:
		out.ManagedDrift = r.ManagedDrift
	case modeOrphans:
		out.Orphans = r.Orphans
	default: // modeFull
		out.ManagedDrift = r.ManagedDrift
		out.Orphans = r.Orphans
	}
	return out
}

func main() {
	os.Exit(int(run(os.Args[1:], os.Stdout, os.Stderr)))
}
