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
// Key: name (baseline 17.18); overridden to "id" on < 17.18.
//
// DHCP is cataloged in families.yaml as a singleton family because the
// netascode block owns the whole /ip/dhcp container. The writer
// operates on the pool keyed list under it — which is the only leaf
// we manage in Phase 1.

const (
	dhcpPoolListPath    = "/Cisco-IOS-XE-native:native/ip/dhcp/pool"
	dhcpPoolEnvelopeKey = "Cisco-IOS-XE-native:pool"
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
func (w dhcpWriter) YANGPaths() []string { return []string{dhcpPoolListPath} }

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
	raw, err := c.Fetch(ctx, dhcpPoolListPath)
	if err != nil {
		if isRESTCONF404(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	body, err := unwrapYANGEnvelope(raw, w.resolvedEnvelopeKey())
	if err != nil || body == nil {
		return []map[string]any{}, err
	}
	list, err := decodeYANGList(body)
	if err != nil {
		return nil, fmt.Errorf("dhcp: decode pool list: %w", err)
	}
	// Apply fetch-side reverse transform so observed data matches
	// netascode canonical shape for Diff comparison.
	r := ensureResolver(w.resolver)
	for i, entry := range list {
		list[i] = r.AutoReverseObservedBody("dhcp", entry)
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

	keyField := w.resolvedKeyField()

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

	r := ensureResolver(w.resolver)
	override, _ := r.GetOverride("dhcp")

	ops := make([]transport.Op, 0, len(names))
	for _, name := range names {
		desired := want[name]
		observed, inDevice := got[name]
		if inDevice && leavesEqual(desired, observed, dhcpPoolManagedLeaves) {
			continue
		}
		entry := projectManagedLeaves(desired, dhcpPoolManagedLeaves)
		entry["name"] = name

		// Apply version-conditional body transforms.
		entry = ApplyOverrideToBody(entry, override)

		// Determine the key value for the RESTCONF path. After
		// BodyTransform, the key field may have been renamed
		// (e.g. "name" → "id").
		pathKey := name
		if override != nil && override.KeyFieldOverride != "" {
			if v, ok := entry[override.KeyFieldOverride]; ok {
				pathKey = fmt.Sprint(v)
			}
		}

		body, err := wrapYANGPayload(w.resolvedEnvelopeKey(), []any{entry})
		if err != nil {
			return nil, err
		}
		ops = append(ops, transport.Op{
			Verb:     transport.VerbMerge,
			Path:     dhcpPoolListPath + "=" + pathKey,
			PathSpec: pathSpecForKeyedListEntry(dhcpPoolListPath, keyField, pathKey),
			Body:     body,
		})
	}

	// If parent creation is needed, prepend a parent-create op.
	if override != nil && override.NeedParentCreation && len(ops) > 0 {
		parentOp := transport.Op{
			Verb: transport.VerbMerge,
			Path: override.ParentPath,
			Body: override.ParentBody,
		}
		ops = append([]transport.Op{parentOp}, ops...)
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
		if name, ok := p["name"].(string); ok && name != "" {
			keys = append(keys, name)
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
		if name, ok := p["name"].(string); ok && name != "" {
			want[name] = struct{}{}
		}
	}
	names := make([]string, 0, len(observedPools))
	for _, p := range observedPools {
		if name, ok := p["name"].(string); ok && name != "" {
			if _, kept := want[name]; !kept {
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)

	keyField := w.resolvedKeyField()
	ops := make([]transport.Op, 0, len(names))
	for _, name := range names {
		ops = append(ops, transport.Op{
			Verb:     transport.VerbDelete,
			Path:     dhcpPoolListPath + "=" + name,
			PathSpec: pathSpecForKeyedListEntry(dhcpPoolListPath, keyField, name),
		})
	}
	return ops, nil
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
