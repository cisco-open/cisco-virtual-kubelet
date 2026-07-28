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
	"net/url"
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

// NXAPIMutationError identifies the first failed operation in a
// non-transactional NX-OS mutation sequence. OperationIndex is zero-based and
// AppliedOperations counts only operations that completed before the failure.
// The request body is deliberately never retained.
type NXAPIMutationError struct {
	OperationIndex    int
	AppliedOperations int
	Verb              transport.Verb
	Path              string

	cause error
}

func (e *NXAPIMutationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := fmt.Sprintf(
		"nxos rest mutate: operation index=%d failed after applied=%d verb=%s",
		e.OperationIndex,
		e.AppliedOperations,
		safeDMEValue(string(e.Verb), maxDMEMethodLength),
	)
	if e.Path != "" {
		message += fmt.Sprintf(" path=%q", e.Path)
	}
	if e.cause != nil {
		message += ": " + safeDMEValue(e.cause.Error(), maxNXAPIErrorLength)
	}
	return message
}

func (e *NXAPIMutationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

type redactedNXAPIError struct {
	message string
	cause   error
}

func (e *redactedNXAPIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.message
}

func (e *redactedNXAPIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
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
		raw, err := t.dmeGetWithRetry(ctx, nxosschema.DNSystem, url.Values{
			"rsp-subtree":       []string{"full"},
			"rsp-subtree-class": []string{"ethpmEntity,ethpmInst"},
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(parseDMESystem(raw))
	case nxosschema.PathFeature:
		raw, err := t.dmeGetClassesWithFallback(ctx, nxosschema.DNSystem, nxosschema.FeatureDMEClasses())
		if err != nil {
			return nil, err
		}
		return json.Marshal(parseDMEFeatures(raw))
	case nxosschema.PathFeatureSet:
		raw, err := t.dmeGetClassesWithFallback(ctx, nxosschema.DNSystem, nxosschema.FeatureSetDMEClasses())
		if err != nil {
			return nil, err
		}
		return json.Marshal(parseDMEFeatureSets(raw))
	case nxosschema.PathVLANBrief:
		raw, err := t.dmeGetWithRetry(ctx, nxosschema.DNBridgeDomain, url.Values{
			"query-target":         []string{"children"},
			"target-subtree-class": []string{"l2BD"},
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"vlans": parseDMEVLANs(raw)})
	case nxosschema.PathInterfaceEthernet:
		raw, err := t.dmeGetWithRetry(ctx, nxosschema.DNInterfaceEntity, url.Values{
			"query-target":         []string{"children"},
			"target-subtree-class": []string{"l1PhysIf"},
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"interfaces": parseDMEEthernetInterfaces(raw)})
	default:
		return nil, fmt.Errorf("nxos rest fetch: unsupported path %q", path)
	}
}

func (t *nxapiConfigTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	return "", transport.ErrUnsupported
}

func (t *nxapiConfigTransport) Mutate(ctx context.Context, tx transport.TxHandle, ops []transport.Op) error {
	if tx != "" {
		return transport.ErrUnsupported
	}
	for index, op := range ops {
		if err := t.mutateOne(ctx, op); err != nil {
			return &NXAPIMutationError{
				OperationIndex:    index,
				AppliedOperations: index,
				Verb:              op.Verb,
				Path:              safeDMEValue(op.Path, maxDMEDNLength),
				cause:             err,
			}
		}
	}
	return nil
}

func (t *nxapiConfigTransport) mutateOne(ctx context.Context, op transport.Op) error {
	switch op.Verb {
	case transport.VerbCLI:
		if strings.TrimSpace(string(op.Body)) == "" {
			return fmt.Errorf("nxos rest mutate: empty CLI operation")
		}
		return t.mutateCLI(ctx, op.Body)
	case transport.VerbMerge:
		if strings.TrimSpace(op.Path) == "" {
			return fmt.Errorf("nxos rest mutate: empty DME DN for %s", op.Verb)
		}
		if err := t.client.dmePost(ctx, op.Path, op.Body); err != nil {
			return redactNXAPIError(err)
		}
		return nil
	case transport.VerbReplace:
		if strings.TrimSpace(op.Path) == "" {
			return fmt.Errorf("nxos rest mutate: empty DME DN for %s", op.Verb)
		}
		if err := t.client.dmePut(ctx, op.Path, op.Body); err != nil {
			return redactNXAPIError(err)
		}
		return nil
	case transport.VerbDelete:
		if strings.TrimSpace(op.Path) == "" {
			return fmt.Errorf("nxos rest mutate: empty DME DN for %s", op.Verb)
		}
		if err := t.client.dmeDelete(ctx, op.Path); err != nil {
			return redactNXAPIError(err)
		}
		return nil
	default:
		return fmt.Errorf("nxos rest mutate: unsupported verb %s", op.Verb)
	}
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

func (t *nxapiConfigTransport) dmeGetWithRetry(ctx context.Context, dn string, query url.Values) ([]byte, error) {
	var raw []byte
	err := transport.RetryIdempotent(ctx, t.retry, func() error {
		var err error
		raw, err = t.client.dmeGet(ctx, dn, query)
		if err != nil {
			err = redactNXAPIError(err)
		}
		return err
	})
	return raw, err
}

func (t *nxapiConfigTransport) dmeGetClassesWithFallback(ctx context.Context, dn string, classes []string) ([]byte, error) {
	remaining := append([]string(nil), classes...)
	for len(remaining) > 0 {
		raw, err := t.dmeGetWithRetry(ctx, dn, dmeSubtreeClassQuery(remaining))
		if err == nil {
			return raw, nil
		}
		unknown := unknownDMEClass(err)
		if unknown == "" {
			return t.dmeGetClassesIndividually(ctx, dn, remaining)
		}
		next := removeDMEClass(remaining, unknown)
		if len(next) == len(remaining) {
			return t.dmeGetClassesIndividually(ctx, dn, remaining)
		}
		log.G(ctx).WithField("class", unknown).Debug("nxos config driver: skipping unsupported DME class")
		remaining = next
	}
	return []byte(`{"imdata":[]}`), nil
}

func (t *nxapiConfigTransport) dmeGetClassesIndividually(ctx context.Context, dn string, classes []string) ([]byte, error) {
	env := dmeEnvelope{}
	for _, class := range classes {
		raw, err := t.dmeGetWithRetry(ctx, dn, dmeSubtreeClassQuery([]string{class}))
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unknown class") {
				log.G(ctx).WithField("class", class).Debug("nxos config driver: skipping unsupported DME class")
				continue
			}
			return nil, err
		}
		var partial dmeEnvelope
		if err := json.Unmarshal(raw, &partial); err != nil {
			return nil, fmt.Errorf("nxos dme decode %s: %w", class, err)
		}
		env.IMData = append(env.IMData, partial.IMData...)
	}
	env.TotalCount = strconv.Itoa(len(env.IMData))
	return json.Marshal(env)
}

func dmeSubtreeClassQuery(classes []string) url.Values {
	return url.Values{
		"rsp-subtree":       []string{"full"},
		"rsp-subtree-class": []string{strings.Join(classes, ",")},
	}
}

func unknownDMEClass(err error) string {
	if err == nil {
		return ""
	}
	matches := regexp.MustCompile(`(?i)unknown class\s+([A-Za-z0-9_]+)`).FindStringSubmatch(err.Error())
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func removeDMEClass(classes []string, remove string) []string {
	out := make([]string, 0, len(classes))
	for _, class := range classes {
		if strings.EqualFold(class, remove) {
			continue
		}
		out = append(out, class)
	}
	return out
}

func (t *nxapiConfigTransport) mutateCLI(ctx context.Context, body []byte) error {
	cmds, err := cliCommands(body)
	if err != nil {
		return err
	}
	if len(cmds) == 0 {
		return nil
	}
	if !strings.EqualFold(cmds[0], "configure terminal") {
		cmds = append([]string{"configure terminal"}, cmds...)
	}
	if _, err := t.client.conf(ctx, cmds...); err != nil {
		return redactNXAPIError(err)
	}
	return nil
}

func redactNXAPIError(err error) error {
	if err == nil {
		return nil
	}
	return &redactedNXAPIError{
		message: safeDMEValue(err.Error(), maxNXAPIErrorLength),
		cause:   err,
	}
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

func parseDMESystemHostname(raw []byte) string {
	return stringLeafFromDMESystem(parseDMESystem(raw), "hostname")
}

func parseDMESystem(raw []byte) map[string]any {
	system := map[string]any{}
	for _, attrs := range collectDMEClassAttrs(raw, "topSystem") {
		if name := stringAttr(attrs, "name"); name != "" {
			system["hostname"] = name
			break
		}
	}
	for _, attrs := range collectDMEClassAttrs(raw, "ethpmInst") {
		if mtu, err := strconv.Atoi(stringAttr(attrs, "systemJumboMtu")); err == nil && mtu > 0 {
			system["mtu"] = mtu
			break
		}
	}
	return system
}

func parseDMEFeatures(raw []byte) map[string]any {
	out := map[string]any{}
	for _, mapping := range nxosschema.FeatureDMEMappings() {
		for _, attrs := range collectDMEClassAttrs(raw, mapping.Class) {
			if state := stringAttr(attrs, "adminSt"); state != "" {
				out[mapping.Field] = normalizeDMEAdminState(state)
				break
			}
		}
	}
	return out
}

func parseDMEFeatureSets(raw []byte) map[string]any {
	out := map[string]any{}
	allowed := map[string]struct{}{}
	for _, field := range nxosschema.FeatureSetFields() {
		allowed[field] = struct{}{}
	}
	for _, attrs := range collectDMEClassAttrs(raw, "fsetFeatureSet") {
		name := stringAttr(attrs, "name")
		if name == "" {
			name = featureSetNameFromRN(stringAttr(attrs, "rn"))
		}
		if _, ok := allowed[name]; !ok {
			continue
		}
		if state := stringAttr(attrs, "adminSt"); state != "" {
			out[name] = normalizeDMEAdminState(state)
		}
	}
	return out
}

func normalizeDMEAdminState(state string) any {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "enabled":
		return true
	case "disabled":
		return false
	default:
		return strings.TrimSpace(state)
	}
}

func featureSetNameFromRN(rn string) string {
	rn = strings.TrimSpace(rn)
	if strings.HasPrefix(rn, "fset-[") && strings.HasSuffix(rn, "]") {
		return strings.TrimSuffix(strings.TrimPrefix(rn, "fset-["), "]")
	}
	return rn
}

func stringLeafFromDMESystem(system map[string]any, key string) string {
	if v, ok := system[key].(string); ok {
		return v
	}
	return ""
}

func parseDMEVLANs(raw []byte) []map[string]any {
	var vlans []map[string]any
	for _, attrs := range collectDMEClassAttrs(raw, "l2BD") {
		id, ok := vlanIDFromEncap(stringAttr(attrs, "fabEncap"))
		if !ok {
			continue
		}
		item := map[string]any{"id": id}
		if name := stringAttr(attrs, "name"); name != "" {
			item["name"] = name
		}
		vlans = append(vlans, item)
	}
	return vlans
}

func vlanIDFromEncap(encap string) (int, bool) {
	encap = strings.TrimSpace(strings.ToLower(encap))
	if encap == "" {
		return 0, false
	}
	encap = strings.TrimPrefix(encap, "vlan-")
	id, err := strconv.Atoi(encap)
	return id, err == nil
}

func parseDMEEthernetInterfaces(raw []byte) []map[string]any {
	var interfaces []map[string]any
	for _, attrs := range collectDMEClassAttrs(raw, "l1PhysIf") {
		name, ok := ethernetNameFromDMEID(stringAttr(attrs, "id"))
		if !ok {
			continue
		}
		item := map[string]any{"type": "Ethernet", "name": name}
		if desc := stringAttr(attrs, "descr"); desc != "" {
			item["description"] = desc
		}
		switch strings.ToLower(stringAttr(attrs, "adminSt")) {
		case "down":
			item["shutdown"] = true
		case "up":
			item["shutdown"] = false
		}
		if layer := stringAttr(attrs, "layer"); layer != "" {
			item["layer"] = layer
		}
		if flags := stringAttr(attrs, "userCfgdFlags"); flags != "" {
			item["user_configured_flags"] = flags
		}
		if mtu, err := strconv.Atoi(stringAttr(attrs, "mtu")); err == nil && mtu > 0 {
			item["mtu"] = mtu
		}
		interfaces = append(interfaces, item)
	}
	return interfaces
}

func ethernetNameFromDMEID(id string) (string, bool) {
	id = strings.TrimSpace(id)
	lower := strings.ToLower(id)
	switch {
	case strings.HasPrefix(lower, "ethernet"):
		return strings.TrimSpace(id[len("ethernet"):]), true
	case strings.HasPrefix(lower, "eth"):
		return strings.TrimSpace(id[len("eth"):]), true
	default:
		return "", false
	}
}
