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

package nxos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
	log "github.com/virtual-kubelet/virtual-kubelet/log"
)

type nxapiConfigTransport struct {
	client *nxapiClient
	retry  transport.RetryPolicy
}

type NXAPIConfigTransportOptions struct {
	HTTPClient     *http.Client
	SessionLock    *sync.Mutex
	DefaultTimeout time.Duration
	RetryPolicy    transport.RetryPolicy
}

func newNXAPIConfigTransport(spec *ciskov1.DeviceSpec) (transport.Interface, error) {
	return newNXAPIConfigTransportWithOptions(spec, NXAPIConfigTransportOptions{})
}

func newNXAPIConfigTransportWithOptions(spec *ciskov1.DeviceSpec, opts NXAPIConfigTransportOptions) (transport.Interface, error) {
	c, err := newNXAPIClientWithOptions(spec, nxapiClientOptions{
		HTTPClient:     opts.HTTPClient,
		SessionLock:    opts.SessionLock,
		DefaultTimeout: opts.DefaultTimeout,
	})
	if err != nil {
		return nil, err
	}
	return &nxapiConfigTransport{client: c, retry: opts.RetryPolicy}, nil
}

func (t *nxapiConfigTransport) Capabilities() transport.Capabilities {
	return transport.Capabilities{
		Kind:                    transport.KindNXAPI,
		SupportsWritableRunning: true,
		SupportsDiagnosticExec:  true,
		SupportsSaveStartup:     true,
	}
}

func (t *nxapiConfigTransport) Fetch(ctx context.Context, path string) ([]byte, error) {
	switch path {
	case nxosschema.PathSystemHostname:
		out, err := t.showWithRetry(ctx, "show hostname")
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"hostname": parseNXOSHostname(out)})
	case nxosschema.PathVLANBrief:
		out, err := t.showWithRetry(ctx, "show vlan brief")
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"vlans": parseNXOSVLANBrief(out)})
	case nxosschema.PathInterfaceEthernet:
		out, err := t.showWithRetry(ctx, `show running-config | section "^interface Ethernet"`)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"interfaces": parseNXOSEthernetRunning(out)})
	default:
		return nil, fmt.Errorf("nxos nxapi fetch: unsupported path %q", path)
	}
}

func (t *nxapiConfigTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	return "", transport.ErrUnsupported
}

func (t *nxapiConfigTransport) Mutate(ctx context.Context, _ transport.TxHandle, ops []transport.Op) error {
	for _, op := range ops {
		if op.Verb != transport.VerbCLI {
			return fmt.Errorf("nxos nxapi mutate: unsupported verb %s", op.Verb)
		}
		cmds, err := cliCommands(op.Body)
		if err != nil {
			return err
		}
		if len(cmds) == 0 {
			continue
		}
		if !strings.EqualFold(cmds[0], "configure terminal") {
			cmds = append([]string{"configure terminal"}, cmds...)
		}
		if _, err := t.client.conf(ctx, cmds...); err != nil {
			return redactNXAPIError(err)
		}
	}
	return nil
}

func (t *nxapiConfigTransport) Commit(_ context.Context, tx transport.TxHandle) error {
	if tx == "" {
		return nil
	}
	return transport.ErrUnsupported
}

func (t *nxapiConfigTransport) Discard(_ context.Context, tx transport.TxHandle) error {
	if tx == "" {
		return nil
	}
	return transport.ErrUnsupported
}

func (t *nxapiConfigTransport) SaveStartup(ctx context.Context) error {
	if _, err := t.client.exec(ctx, "cli_conf", "copy running-config startup-config"); err != nil {
		return redactNXAPIError(err)
	}
	return nil
}

func (t *nxapiConfigTransport) Close() error { return nil }

func (t *nxapiConfigTransport) DiagnosticExec(ctx context.Context, commands []string) ([]transport.CommandResult, error) {
	out := make([]transport.CommandResult, 0, len(commands))
	for _, cmd := range commands {
		body, err := t.showWithRetry(ctx, cmd)
		res := transport.CommandResult{Command: cmd, Output: body}
		if err != nil {
			res.Err = transport.RedactCredentials(err.Error())
		}
		out = append(out, res)
	}
	return out, nil
}

func (t *nxapiConfigTransport) showWithRetry(ctx context.Context, cmd string) (string, error) {
	var out string
	err := transport.RetryIdempotent(ctx, t.retry, func() error {
		var err error
		out, err = t.client.show(ctx, cmd)
		if err != nil {
			err = redactNXAPIError(err)
		}
		return err
	})
	return out, err
}

func redactNXAPIError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", transport.RedactCredentials(err.Error()))
}

func FetchDeviceVersion(ctx context.Context, t transport.Interface) string {
	if t == nil {
		return ""
	}
	execer, ok := t.(transport.DiagnosticExecer)
	if !ok {
		return ""
	}
	results, err := execer.DiagnosticExec(ctx, []string{"show version"})
	if err != nil {
		log.G(ctx).WithError(err).Warn("nxos config driver: could not fetch device version")
		return ""
	}
	for _, res := range results {
		if res.Err != "" {
			log.G(ctx).WithField("command", res.Command).WithField("error", res.Err).
				Warn("nxos config driver: show version returned an error")
			continue
		}
		if ver := parseNXOSVersion(res.Output); ver != "" {
			log.G(ctx).WithField("version", ver).Info("nxos config driver: fetched device version")
			return ver
		}
	}
	log.G(ctx).Warn("nxos config driver: empty version response")
	return ""
}

func cliCommands(raw []byte) ([]string, error) {
	body := strings.TrimSpace(string(raw))
	if body == "" {
		return nil, nil
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		body = encoded
	}
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "\x00") {
			return nil, fmt.Errorf("nxos cli op contains NUL byte")
		}
		out = append(out, line)
	}
	return out, nil
}

var hostnameLineRE = regexp.MustCompile(`(?im)^\s*hostname\s+(\S+)\s*$`)
var nxosVersionRE = regexp.MustCompile(`(?im)(?:NXOS: version|system:\s+version|kickstart:\s+version)\s+([^\s,]+)`)

func parseNXOSHostname(out string) string {
	if m := hostnameLineRE.FindStringSubmatch(out); len(m) > 1 {
		return m[1]
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" || trimmed == "{}" {
		return ""
	}
	if fields := strings.Fields(trimmed); len(fields) == 1 {
		return fields[0]
	}
	return ""
}

func parseNXOSVersion(out string) string {
	if m := nxosVersionRE.FindStringSubmatch(out); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func parseNXOSVLANBrief(out string) []map[string]any {
	var vlans []map[string]any
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		id, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		statusIdx := 2
		for i := 2; i < len(fields); i++ {
			if isNXOSVLANStatus(fields[i]) {
				statusIdx = i
				break
			}
		}
		name := strings.Join(fields[1:statusIdx], " ")
		vlans = append(vlans, map[string]any{
			"id":     id,
			"name":   name,
			"status": fields[statusIdx],
		})
	}
	return vlans
}

func isNXOSVLANStatus(field string) bool {
	switch lower := strings.ToLower(strings.TrimSpace(field)); lower {
	case "active", "suspend", "suspended", "shutdown", "act/unsup", "act/lshut", "act/ishut":
		return true
	default:
		return strings.HasPrefix(lower, "act/")
	}
}

func parseNXOSEthernetRunning(out string) []map[string]any {
	var interfaces []map[string]any
	var cur map[string]any
	flush := func() {
		if cur != nil {
			interfaces = append(interfaces, cur)
		}
		cur = nil
	}
	for _, rawLine := range strings.Split(out, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || line == "!" {
			continue
		}
		if strings.HasPrefix(line, "interface Ethernet") {
			flush()
			name := strings.TrimPrefix(line, "interface Ethernet")
			cur = map[string]any{"type": "Ethernet", "name": strings.TrimSpace(name)}
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "description "):
			cur["description"] = strings.TrimSpace(strings.TrimPrefix(line, "description "))
		case line == "shutdown":
			cur["shutdown"] = true
		case line == "no shutdown":
			cur["shutdown"] = false
		}
	}
	flush()
	return interfaces
}
