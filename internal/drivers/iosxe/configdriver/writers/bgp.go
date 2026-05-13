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

// BGP Phase-2 writer.
//
// netascode shape (narrow, Phase-2 subset):
//
//   bgp:
//     asn: "65000"
//     router_id: 10.255.255.1
//     log_neighbor_changes: true
//     neighbors:
//       - id: 192.0.2.2
//         remote_as: "65001"
//         description: peer-1
//         address_families:
//           ipv4_unicast:
//             activate: true
//
// YANG: /Cisco-IOS-XE-native:native/router/Cisco-IOS-XE-bgp:router-bgp
//
// BGP is the single most complex family in Cisco-IOS-XE YANG —
// attempting to model every leaf would produce thousands of lines of
// writer code and very few Phase-2 users would exercise them. The
// Phase-2 writer treats the container as a singleton with a managed-
// leaf set covering the knobs most commonly set via netascode. Deeper
// neighbor/address-family shaping is the work of Phase-3, when real
// operator repos will inform the priority order.
//
// The Key label 'singleton' in families.yaml reflects this: the YANG
// subtree itself is keyed by an AS number, but netascode expresses
// only a single BGP instance per device so the writer treats the
// entire router-bgp container as the managed scope.
//
// Create-before-patch: when the BGP process doesn't exist on the
// device, Fetch returns an empty map (404). Diff emits VerbReplace
// (PUT) to create the resource. Subsequent reconcile cycles use
// VerbMerge (PATCH) for partial updates.
// Caught against C8000V 17.16.01a: PATCH to nonexistent
// router-bgp → 404.

const (
	// 17.18+ YANG shape: router-bgp is a container under /router.
	bgpYANGPath    = "/Cisco-IOS-XE-native:native/router/Cisco-IOS-XE-bgp:router-bgp"
	bgpEnvelopeKey = "Cisco-IOS-XE-bgp:router-bgp"

	// 17.16 YANG shape: bgp is a keyed list under /router.
	bgpYANGPathLegacy    = "/Cisco-IOS-XE-native:native/router/Cisco-IOS-XE-bgp:bgp"
	bgpEnvelopeKeyLegacy = "Cisco-IOS-XE-bgp:bgp"
)

var bgpManagedLeaves = []string{
	"id",
	"bgp",
	"neighbor",
	"address-family",
	"redistribute",
}

func init() {
	Override(bgpWriter{})
}

type bgpWriter struct{}

func (bgpWriter) Family() string { return "bgp" }
func (bgpWriter) YANGPaths() []string {
	return []string{ResolvedYANGPath("bgp", bgpYANGPath)}
}

func (w bgpWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	if IsLegacyVersion("bgp") {
		return w.fetchLegacy(ctx, c)
	}
	sw := singletonWriter{
		family:        "bgp",
		yangPath:      bgpYANGPath,
		envelopeKey:   bgpEnvelopeKey,
		managedLeaves: bgpManagedLeaves,
	}
	return sw.Fetch(ctx, c)
}

// fetchLegacy handles the 17.16 YANG shape where BGP is a keyed list
// under /router/Cisco-IOS-XE-bgp:bgp.
func (w bgpWriter) fetchLegacy(ctx context.Context, c transport.Interface) (any, error) {
	raw, err := c.Fetch(ctx, bgpYANGPathLegacy)
	if err != nil {
		if isRESTCONF404(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	body, err := unwrapYANGEnvelope(raw, bgpEnvelopeKeyLegacy)
	if err != nil || body == nil {
		return map[string]any{}, err
	}
	// bgp is a list — take the first entry.
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("bgp: decode list: %w", err)
	}
	if len(list) == 0 {
		return map[string]any{}, nil
	}
	return list[0], nil
}

func (w bgpWriter) Diff(desired, observed any) ([]transport.Op, error) {
	desiredMap, err := coerceMap(desired, "bgp.desired")
	if err != nil {
		return nil, err
	}
	observedMap, err := coerceMap(observed, "bgp.observed")
	if err != nil {
		return nil, err
	}
	if desiredMap == nil {
		return nil, nil
	}
	if observedMap == nil {
		observedMap = map[string]any{}
	}
	if leavesEqual(desiredMap, observedMap, bgpManagedLeaves) {
		return nil, nil
	}
	if IsLegacyVersion("bgp") {
		return w.diffLegacy(desiredMap, observedMap)
	}
	proj := projectManagedLeaves(desiredMap, bgpManagedLeaves)
	body, err := wrapYANGPayload(bgpEnvelopeKey, proj)
	if err != nil {
		return nil, err
	}
	// Use PUT (create) when BGP doesn't exist yet; PATCH otherwise.
	verb := transport.VerbMerge
	if len(observedMap) == 0 {
		verb = transport.VerbReplace
	}
	return []transport.Op{{
		Verb: verb,
		Path: bgpYANGPath,
		Body: body,
	}}, nil
}

// diffLegacy builds a MERGE op for the 17.16 YANG shape.
// BGP is a keyed list: MERGE to /router with {router: {bgp: [{...}]}}.
func (w bgpWriter) diffLegacy(desired, observed map[string]any) ([]transport.Op, error) {
	proj := projectManagedLeaves(desired, bgpManagedLeaves)
	// Wrap as list entry inside the /router container.
	bgpList := []any{proj}
	router := map[string]any{bgpEnvelopeKeyLegacy: bgpList}
	body, err := json.Marshal(map[string]any{"Cisco-IOS-XE-native:router": router})
	if err != nil {
		return nil, fmt.Errorf("bgp: marshal: %w", err)
	}
	return []transport.Op{{
		Verb: transport.VerbMerge,
		Path: "/Cisco-IOS-XE-native:native/router",
		Body: body,
	}}, nil
}

func (w bgpWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}
