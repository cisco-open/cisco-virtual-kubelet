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

package writers

import (
	"context"
	"fmt"
	"sort"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// DHCP Phase-1 writer.
//
// netascode shape:
//
//   dhcp:
//     pools:
//       - name: IOX
//         network: 192.168.10.0
//         prefix_length: 24
//         default_router: 192.168.10.1
//
// YANG path: /Cisco-IOS-XE-native:native/ip/dhcp/pool
// Key: name.
//
// DHCP is cataloged in families.yaml as a singleton family because the
// netascode block owns the whole /ip/dhcp container. The writer
// operates on the pool keyed list under it — which is the only leaf
// we manage in Phase 1. Other DHCP leaves (excluded-address, remember,
// conflict-logging) are left to future phases.

const (
	dhcpPoolListPath    = "/Cisco-IOS-XE-native:native/ip/dhcp/pool"
	dhcpPoolEnvelopeKey = "Cisco-IOS-XE-native:pool"
)

var dhcpPoolManagedLeaves = []string{
	"network",
	"prefix_length",
	"default_router",
}

type dhcpWriter struct{}

func init() { Override(dhcpWriter{}) }

func (dhcpWriter) Family() string      { return "dhcp" }
func (dhcpWriter) YANGPaths() []string { return []string{dhcpPoolListPath} }

func (dhcpWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	raw, err := c.Fetch(ctx, dhcpPoolListPath)
	if err != nil {
		if isRESTCONF404(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	body, err := unwrapYANGEnvelope(raw, dhcpPoolEnvelopeKey)
	if err != nil || body == nil {
		return []map[string]any{}, err
	}
	list, err := decodeYANGList(body)
	if err != nil {
		return nil, fmt.Errorf("dhcp: decode pool list: %w", err)
	}
	return list, nil
}

func (dhcpWriter) Diff(desired, observed any) ([]transport.Op, error) {
	desiredPools, err := coerceDHCPBlock(desired, "desired")
	if err != nil {
		return nil, err
	}
	observedPools, err := coerceList(observed, "observed")
	if err != nil {
		return nil, err
	}

	want := map[string]map[string]any{}
	for _, p := range desiredPools {
		name, ok := p["name"].(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("dhcp: desired pool missing name")
		}
		want[name] = p
	}
	got := map[string]map[string]any{}
	for _, p := range observedPools {
		if name, ok := p["name"].(string); ok {
			got[name] = p
		}
	}
	names := make([]string, 0, len(want))
	for n := range want {
		names = append(names, n)
	}
	sort.Strings(names)

	ops := make([]transport.Op, 0, len(names))
	for _, name := range names {
		desired := want[name]
		observed, inDevice := got[name]
		if inDevice && leavesEqual(desired, observed, dhcpPoolManagedLeaves) {
			continue
		}
		entry := projectManagedLeaves(desired, dhcpPoolManagedLeaves)
		entry["name"] = name
		body, err := wrapYANGPayload(dhcpPoolEnvelopeKey, []any{entry})
		if err != nil {
			return nil, err
		}
		ops = append(ops, transport.Op{
			Verb:     transport.VerbMerge,
			Path:     dhcpPoolListPath + "=" + name,
			PathSpec: pathSpecForKeyedListEntry(dhcpPoolListPath, "name", name),
			Body:     body,
		})
	}
	return ops, nil
}

func (dhcpWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}

// PruneDiff implements PruneCapable: a VerbDelete op for every
// pool present on the device but absent from the desired intent.
// Required because dhcp is one of the apphosting-prerequisite
// families (interface_virtual_port_group, dhcp, access_list_extended)
// and Wave 4A-fu's teardown semantics depend on every prereq family
// being prunable. The non-prune fast-path stays via Diff for the
// normal apply loop.
//
// The implementation mirrors keyedListWriter.PruneDiff: build the
// set of desired names, walk observed, emit VerbDelete for each
// entry not in the desired set. dhcp pools are keyed by `name`,
// matching the families.yaml entry.
func (dhcpWriter) PruneDiff(desired, observed any) ([]transport.Op, error) {
	desiredPools, err := coerceDHCPBlock(desired, "desired")
	if err != nil {
		return nil, err
	}
	observedPools, err := coerceList(observed, "observed")
	if err != nil {
		return nil, err
	}
	want := map[string]struct{}{}
	for _, p := range desiredPools {
		if name, ok := p["name"].(string); ok && name != "" {
			want[name] = struct{}{}
		}
	}
	// Sort observed names so the resulting op list is deterministic
	// — the engine's status writes are easier to diff under that.
	names := make([]string, 0, len(observedPools))
	for _, p := range observedPools {
		if name, ok := p["name"].(string); ok && name != "" {
			if _, kept := want[name]; !kept {
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	ops := make([]transport.Op, 0, len(names))
	for _, name := range names {
		ops = append(ops, transport.Op{
			Verb:     transport.VerbDelete,
			Path:     dhcpPoolListPath + "=" + name,
			PathSpec: pathSpecForKeyedListEntry(dhcpPoolListPath, "name", name),
		})
	}
	return ops, nil
}

// coerceDHCPBlock accepts either the nested netascode shape
// {"pools":[...]} or a bare list (e.g. from a narrow test fixture).
func coerceDHCPBlock(v any, origin string) ([]map[string]any, error) {
	if m, ok := v.(map[string]any); ok {
		if inner, ok := m["pools"]; ok {
			return coerceList(inner, origin+".pools")
		}
		return nil, nil
	}
	return coerceList(v, origin)
}
