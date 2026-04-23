// Copyright © 2026 Cisco Systems, Inc.
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

// Command cisco-vk-config-collect reads the current state of an IOS-XE
// device and emits a netascode-shaped YAML document describing the
// subset CVK's writers know how to manage. The output is the starting
// point for onboarding a brownfield device into GitOps-managed
// configuration:
//
//   cisco-vk-config-collect \
//     --address 192.0.2.10 --username admin --password PASS \
//     --device-name edge-01 \
//     --families vlan,vrf,interface_ethernet > devices/edge-01/data.nac.yaml
//
// The tool is nac-collect's equivalent for CVK: every family's Fetch
// is invoked, results are translated back into netascode shape using
// the writer's FamilySchema, and the whole tree is wrapped in the
// iosxe.devices[] envelope so it can be fed straight to an
// IOSXEConfig CR's spec.source.inline or to a ConfigMap.
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
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"
)

type exitCode int

const (
	exitOK       exitCode = 0
	exitBadFlags exitCode = 2
	exitBadInput exitCode = 3
	exitCollect  exitCode = 4
)

type flags struct {
	address      string
	port         int
	username     string
	password     string
	deviceName   string
	families     string
	scheme       string
	insecure     bool
	timeout      time.Duration
	out          string
	allFamilies  bool
	continueOnFE bool
}

func parseFlags(args []string, stderr io.Writer) (flags, error) {
	fs := flag.NewFlagSet("cisco-vk-config-collect", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var f flags
	fs.StringVar(&f.address, "address", "", "device management IP or hostname (required)")
	fs.IntVar(&f.port, "port", 443, "RESTCONF port")
	fs.StringVar(&f.username, "username", "", "RESTCONF username (required)")
	fs.StringVar(&f.password, "password", "",
		"RESTCONF password; use CVK_CONFIG_COLLECT_PASSWORD env var to avoid shell history")
	fs.StringVar(&f.deviceName, "device-name", "",
		"netascode device name written into the iosxe.devices[].name field (defaults to --address)")
	fs.StringVar(&f.families, "families", "",
		"comma-separated family list; empty means 'every registered family with an extractable schema' when --all is set")
	fs.BoolVar(&f.allFamilies, "all", false,
		"collect every registered family whose writer exposes a FamilySchema")
	fs.StringVar(&f.scheme, "scheme", "https",
		"URL scheme; 'http' disables TLS entirely (for test fixtures and non-production labs)")
	fs.BoolVar(&f.insecure, "insecure", false, "skip TLS certificate verification")
	fs.DurationVar(&f.timeout, "timeout", 30*time.Second, "per-family RESTCONF timeout")
	fs.StringVar(&f.out, "out", "-", "output file; '-' means stdout")
	fs.BoolVar(&f.continueOnFE, "continue-on-family-error", true,
		"continue with remaining families when one family's Fetch returns an error (default: true, so brownfield devices that lack a feature don't abort the whole collect)")

	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if f.password == "" {
		f.password = os.Getenv("CVK_CONFIG_COLLECT_PASSWORD")
	}
	if f.deviceName == "" {
		f.deviceName = f.address
	}
	return f, nil
}

func run(args []string, stdout, stderr io.Writer) exitCode {
	f, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitBadFlags
	}

	if f.address == "" || f.username == "" {
		fmt.Fprintln(stderr, "ERROR: --address and --username are required")
		return exitBadFlags
	}

	familyList, err := pickFamilies(f)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return exitBadFlags
	}

	cli, err := buildTransport(f)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: build transport: %v\n", err)
		return exitBadInput
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	configuration, anyFailed := collect(ctx, stderr, cli, familyList, f)

	envelope := map[string]any{
		"iosxe": map[string]any{
			"devices": []any{
				map[string]any{
					"name":          f.deviceName,
					"host":          f.address,
					"configuration": configuration,
				},
			},
		},
	}
	body, err := yaml.Marshal(envelope)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: marshal: %v\n", err)
		return exitCollect
	}

	if err := writeOut(stdout, f.out, body); err != nil {
		fmt.Fprintf(stderr, "ERROR: write: %v\n", err)
		return exitCollect
	}
	if f.out != "-" {
		fmt.Fprintf(stdout, "wrote %d bytes to %s\n", len(body), f.out)
	}

	if anyFailed && !f.continueOnFE {
		return exitCollect
	}
	return exitOK
}

// pickFamilies expands --families / --all into the concrete list the
// collect loop iterates. Unknown family names are flagged immediately
// so an operator doesn't discover a typo mid-collect.
func pickFamilies(f flags) ([]string, error) {
	known := writers.AllSchemas()
	if f.allFamilies {
		out := make([]string, 0, len(known))
		for name := range known {
			out = append(out, name)
		}
		sort.Strings(out)
		return out, nil
	}
	if f.families == "" {
		return nil, errors.New("supply --families or --all")
	}
	out := []string{}
	for _, part := range strings.Split(f.families, ",") {
		fam := strings.TrimSpace(part)
		if fam == "" {
			continue
		}
		if _, ok := known[fam]; !ok {
			return nil, fmt.Errorf("family %q is not registered (or has no writer schema)", fam)
		}
		out = append(out, fam)
	}
	sort.Strings(out)
	return out, nil
}

// buildTransport composes the RESTCONF client the writers consume.
// We do not share a session lock here — nothing else is contending
// for the device in a one-shot collect.
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

// collect runs Fetch for every selected family. Translation back to
// netascode shape uses the writer's FamilySchema: keyed_list entries
// are projected under the InnerKey name, singletons are emitted as
// the family's value directly.
func collect(
	ctx context.Context,
	stderr io.Writer,
	cli transport.Interface,
	families []string,
	f flags,
) (map[string]any, bool) {
	out := map[string]any{}
	anyFailed := false

	for _, family := range families {
		w := writers.Get(family)
		if w == nil {
			continue
		}
		schema, ok := writers.Schema(family)
		if !ok {
			continue
		}

		observed, err := w.Fetch(ctx, cli)
		if err != nil {
			anyFailed = true
			fmt.Fprintf(stderr, "warning: %s: %v\n", family, err)
			if !f.continueOnFE {
				return out, true
			}
			continue
		}
		val := reshape(observed, schema)
		if val == nil {
			continue
		}
		out[family] = val
	}
	return out, anyFailed
}

// reshape turns the writer's Fetch return value into netascode body
// shape per the FamilySchema. Keyed-list writers return a []any or
// []map[string]any; we wrap them under schema.InnerKey. Singletons
// return a map[string]any; we emit it verbatim. Unknown shapes are
// returned as-is (worst case the operator edits them).
func reshape(observed any, schema writers.FamilySchema) any {
	if observed == nil {
		return nil
	}
	if schema.Shape == "keyed_list" && schema.InnerKey != "" {
		switch list := observed.(type) {
		case []map[string]any:
			if len(list) == 0 {
				return nil
			}
			entries := make([]any, 0, len(list))
			for _, e := range list {
				entries = append(entries, e)
			}
			return map[string]any{schema.InnerKey: entries}
		case []any:
			if len(list) == 0 {
				return nil
			}
			return map[string]any{schema.InnerKey: list}
		}
	}
	if m, ok := observed.(map[string]any); ok {
		if len(m) == 0 {
			return nil
		}
		return m
	}
	return observed
}

func writeOut(stdout io.Writer, path string, body []byte) error {
	if path == "-" {
		_, err := stdout.Write(body)
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

func main() {
	os.Exit(int(run(os.Args[1:], os.Stdout, os.Stderr)))
}
