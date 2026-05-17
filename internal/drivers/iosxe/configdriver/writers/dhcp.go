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
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"

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
// Key: name (baseline 17.18); overridden to "id" on < 17.18.
//
// DHCP is cataloged in families.yaml as a singleton family because the
// netascode block owns the whole /ip/dhcp container. The writer
// operates on the pool keyed list under it — which is the only leaf
// we manage in Phase 1.

const (
	dhcpParentPath        = "/Cisco-IOS-XE-native:native/ip/dhcp"
	dhcpParentEnvelopeKey = "Cisco-IOS-XE-native:dhcp"
	dhcpPoolEnvelopeKey   = "Cisco-IOS-XE-dhcp:pool"
)

var dhcpPoolManagedLeaves = []string{
	"network",
	"prefix_length",
	"default_router",
}

type dhcpWriter struct {
	resolver *OverrideResolver
}

func init() { Override(dhcpWriter{}) }

func (w dhcpWriter) Family() string      { return "dhcp" }
func (w dhcpWriter) YANGPaths() []string { return []string{dhcpParentPath} }

// withResolver implements resolverBindable so the registry can inject
// the per-device OverrideResolver.
func (w dhcpWriter) withResolver(r *OverrideResolver) SectionWriter {
	return dhcpWriter{resolver: r}
}

// resolvedEnvelopeKey returns the version-appropriate envelope key.
func (w dhcpWriter) resolvedEnvelopeKey() string {
	return ensureResolver(w.resolver).ResolvedEnvelopeKey("dhcp", dhcpPoolEnvelopeKey)
}

// resolvedKeyField returns the version-appropriate list key name.
func (w dhcpWriter) resolvedKeyField() string {
	return ensureResolver(w.resolver).ResolvedKeyField("dhcp", "name")
}

func (w dhcpWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	raw, err := c.Fetch(ctx, dhcpParentPath)
	if err != nil {
		if isRESTCONF404(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	body, err := unwrapYANGEnvelope(raw, dhcpParentEnvelopeKey)
	if err != nil || body == nil {
		return []map[string]any{}, err
	}
	var parent map[string]any
	if err := json.Unmarshal(body, &parent); err != nil {
		return nil, fmt.Errorf("dhcp: decode parent: %w", err)
	}
	pools, ok := parent[w.resolvedEnvelopeKey()]
	if !ok {
		pools, ok = parent["pool"]
	}
	if !ok {
		return []map[string]any{}, nil
	}
	poolBody, err := json.Marshal(pools)
	if err != nil {
		return nil, fmt.Errorf("dhcp: encode pool list: %w", err)
	}
	list, err := decodeYANGList(poolBody)
	if err != nil {
		return nil, fmt.Errorf("dhcp: decode pool list: %w", err)
	}
	// Apply fetch-side reverse transform so observed data matches
	// netascode canonical shape for Diff comparison.
	r := ensureResolver(w.resolver)
	for i, entry := range list {
		list[i] = r.AutoReverseObservedBody("dhcp", entry)
		list[i] = dhcpFetchTransformPre1718(list[i])
	}
	return list, nil
}

func (w dhcpWriter) Diff(desired, observed any) ([]transport.Op, error) {
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
		rawName, ok := p["name"]
		if !ok {
			return nil, fmt.Errorf("dhcp: desired pool missing name")
		}
		name := sprintPoolName(rawName)
		if name == "" {
			return nil, fmt.Errorf("dhcp: desired pool has empty name")
		}
		p["name"] = name // normalise to string for downstream consumers
		want[name] = p
	}
	got := map[string]map[string]any{}
	for _, p := range observedPools {
		if rawName, ok := p["name"]; ok {
			name := sprintPoolName(rawName)
			if name != "" {
				got[name] = p
			}
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

		// IOS-XE 17.x exposes DHCP pools under the /ip/dhcp parent
		// container with a Cisco-IOS-XE-dhcp:pool list keyed by id.
		// Sending to /ip/dhcp/pool is rejected by 17.18.03 on C9300.
		entry = dhcpBodyTransformPre1718(entry)

		body, err := wrapYANGPayload(dhcpParentEnvelopeKey, map[string]any{
			w.resolvedEnvelopeKey(): []any{entry},
		})
		if err != nil {
			return nil, err
		}
		ops = append(ops, transport.Op{
			Verb: transport.VerbMerge,
			Path: dhcpParentPath,
			Body: body,
		})
	}

	return ops, nil
}

func (w dhcpWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}

// KeysOf implements KeyExtractable.
func (w dhcpWriter) KeysOf(v any) []string {
	list, err := coerceDHCPBlock(v, "keysOf")
	if err != nil || len(list) == 0 {
		return nil
	}
	keys := make([]string, 0, len(list))
	for _, p := range list {
		if rawName, ok := p["name"]; ok {
			name := sprintPoolName(rawName)
			if name != "" {
				keys = append(keys, name)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// PruneDiff implements PruneCapable.
func (w dhcpWriter) PruneDiff(desired, observed any) ([]transport.Op, error) {
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
		if rawName, ok := p["name"]; ok {
			name := sprintPoolName(rawName)
			if name != "" {
				want[name] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(observedPools))
	for _, p := range observedPools {
		if rawName, ok := p["name"]; ok {
			name := sprintPoolName(rawName)
			if name != "" {
				if _, kept := want[name]; !kept {
					names = append(names, name)
				}
			}
		}
	}
	sort.Strings(names)

	ops := make([]transport.Op, 0, len(names))
	for _, name := range names {
		ops = append(ops, transport.Op{
			Verb:     transport.VerbDelete,
			Path:     dhcpParentPath + "/" + w.resolvedEnvelopeKey() + "=" + name,
			PathSpec: pathSpecForKeyedListEntry(dhcpParentPath+"/"+w.resolvedEnvelopeKey(), "id", name),
		})
	}
	return ops, nil
}

// sprintPoolName coerces a YAML-decoded pool name to a flat string.
// YAML 1.1 interprets names like "198_18_100_0" as integers (198181000)
// which may arrive as int or float64 depending on the unmarshal path.
// fmt.Sprint on float64 produces scientific notation (1.98181e+08) which
// is not a valid RESTCONF key.  This helper formats integers and
// integer-valued floats with %d to recover a usable string.
func sprintPoolName(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	case float64:
		if n == math.Trunc(n) && !math.IsInf(n, 0) {
			return strconv.FormatInt(int64(n), 10)
		}
		return fmt.Sprint(n)
	default:
		return fmt.Sprint(v)
	}
}

// coerceDHCPBlock accepts either the nested netascode shape
// {"pools":[...]} or a bare list.
func coerceDHCPBlock(v any, origin string) ([]map[string]any, error) {
	if m, ok := v.(map[string]any); ok {
		if inner, ok := m["pools"]; ok {
			return coerceList(inner, origin+".pools")
		}
		return nil, nil
	}
	return coerceList(v, origin)
}
