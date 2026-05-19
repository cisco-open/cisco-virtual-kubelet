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

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// singletonWriter is the generalised SectionWriter for netascode families
// that map to a single YANG container (no list key at the top level).
// Examples: cdp, lldp, ntp, logging, banner, snmp_server, aaa.
//
// Semantics:
//   - Fetch retrieves the container and strips envelopeKey.
//   - Diff compares managedLeaves; if any differ, emits one PATCH at
//     yangPath with a body containing only the managed leaves.
//   - Unmanaged leaves survive — the device keeps whatever it had for
//     them because PATCH merges rather than replaces.
//
// For families whose "singleton" container is actually a handful of
// independent sub-leaves that live at distinct paths (system's
// hostname, for example), use a hand-written writer instead of this
// helper — the single PATCH payload forces everything to share one
// containing YANG element.
type singletonWriter struct {
	family         string
	yangPath       string
	envelopeKey    string
	managedLeaves  []string
	yangBodyShape  func(flat map[string]any) map[string]any
	yangFetchShape func(yang map[string]any) map[string]any
	resolver       *OverrideResolver
}

func (w singletonWriter) Family() string { return w.family }
func (w singletonWriter) YANGPaths() []string {
	return []string{w.resolverForUse().ResolvedYANGPath(w.family, w.yangPath)}
}

func (w singletonWriter) withResolver(r *OverrideResolver) SectionWriter {
	w.resolver = r
	return w
}

func (w singletonWriter) resolverForUse() *OverrideResolver {
	return ensureResolver(w.resolver)
}

func (w singletonWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	resolver := w.resolverForUse()
	yangPath := resolver.ResolvedYANGPath(w.family, w.yangPath)
	envelopeKey := resolver.ResolvedEnvelopeKey(w.family, w.envelopeKey)
	raw, err := c.Fetch(ctx, yangPath)
	if err != nil {
		if isRESTCONF404(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	body, err := unwrapYANGEnvelope(raw, envelopeKey)
	if err != nil || body == nil {
		return map[string]any{}, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%s: decode container: %w", w.family, err)
	}
	// Apply the Fetch-side counterpart of the version override —
	// ReverseElementMap + DecodeEmptyLeaves — so leavesEqual can
	// compare a YANG-shape observed body against the netascode-shape
	// desired body. Skipped automatically when the override carries
	// a BodyTransform (those writers reverse manually).
	out = resolver.AutoReverseObservedBody(w.family, out)
	if w.yangFetchShape != nil {
		out = w.yangFetchShape(out)
	}
	return out, nil
}

func (w singletonWriter) Diff(desired, observed any) ([]transport.Op, error) {
	desiredMap, err := coerceMap(desired, w.family+".desired")
	if err != nil {
		return nil, err
	}
	observedMap, err := coerceMap(observed, w.family+".observed")
	if err != nil {
		return nil, err
	}
	if desiredMap == nil {
		return nil, nil
	}
	// Normalize desired through yangFetchShape so comparison uses the
	// same representation as the fetched observed state. Without this,
	// families whose yangFetchShape unwraps nested sub-containers (e.g.
	// banner: {login: {banner: "text"}} -> {login: "text"}) would
	// always detect drift when the source uses YANG-nested form.
	if w.yangFetchShape != nil && desiredMap != nil {
		desiredMap = w.yangFetchShape(desiredMap)
	}
	if observedMap == nil {
		observedMap = map[string]any{}
	}
	if leavesEqual(desiredMap, observedMap, w.managedLeaves) {
		return nil, nil
	}
	proj := projectManagedLeaves(desiredMap, w.managedLeaves)
	if w.yangBodyShape != nil {
		proj = w.yangBodyShape(proj)
	}
	// Apply version-conditional overrides (element renames,
	// empty-leaf encoding, body transforms).
	resolver := w.resolverForUse()
	if o, ok := resolver.GetOverride(w.family); ok {
		proj = ApplyOverrideToBody(proj, o)
	}
	envelopeKey := resolver.ResolvedEnvelopeKey(w.family, w.envelopeKey)
	body, err := wrapYANGPayload(envelopeKey, proj)
	if err != nil {
		return nil, err
	}
	return []transport.Op{{
		Verb: transport.VerbMerge,
		Path: resolver.ResolvedYANGPath(w.family, w.yangPath),
		Body: body,
	}}, nil
}

func (w singletonWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}
