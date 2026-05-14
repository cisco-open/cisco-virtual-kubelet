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

// BGP community-list Phase-3 writer.
//
// Cisco-IOS-XE-native splits community-lists into
// standard/expanded sub-containers under ip/community-list; Phase-3
// manages the whole container as a singleton because the two
// sub-lists share keys at the parent level.
//
// On IOS-XE < 17.18 the YANG model is completely different:
//
//   standard uses deprecated grouping:
//     standard[name] → permit → permit-list: [<numeric>]
//                    → deny   → deny-list:   [<numeric>]
//
//   expanded uses deprecated grouping:
//     expanded[name] → extended-grouping → extended_grouping:
//                      [{action: "permit"/"deny", string: "regexp"}]
//
// On >= 17.18 the model uses community-list-entry:
//     standard[name] → community-list-entry: [{action, community}]
//     expanded[name] → community-list-entry: [{action, regexp}]

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

const (
	communityListFamily      = "ip_community_list"
	communityListYANGPath    = "/Cisco-IOS-XE-native:native/ip/community-list"
	communityListEnvelopeKey = "Cisco-IOS-XE-bgp:community-list"
)

var communityListManagedLeaves = []string{"standard", "expanded", "no-advertise"}

func init() {
	Override(communityListWriter{})
}

type communityListWriter struct {
	resolver *OverrideResolver
}

func (w communityListWriter) Family() string      { return communityListFamily }
func (w communityListWriter) YANGPaths() []string { return []string{communityListYANGPath} }

func (w communityListWriter) withResolver(r *OverrideResolver) SectionWriter {
	w.resolver = r
	return w
}

func (w communityListWriter) resolverForUse() *OverrideResolver {
	return ensureResolver(w.resolver)
}

func (w communityListWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	sw := singletonWriter{
		family:        communityListFamily,
		yangPath:      communityListYANGPath,
		envelopeKey:   communityListEnvelopeKey,
		managedLeaves: communityListManagedLeaves,
		resolver:      w.resolverForUse(),
	}
	observed, err := sw.Fetch(ctx, c)
	if err != nil {
		return nil, err
	}
	if w.resolverForUse().IsLegacyVersion(communityListFamily) {
		m, ok := observed.(map[string]any)
		if ok {
			observed = communityListFromYANG1716(m)
		}
	}
	return observed, nil
}

func (w communityListWriter) Diff(desired, observed any) ([]transport.Op, error) {
	desiredMap, err := coerceMap(desired, communityListFamily+".desired")
	if err != nil {
		return nil, err
	}
	observedMap, err := coerceMap(observed, communityListFamily+".observed")
	if err != nil {
		return nil, err
	}
	if desiredMap == nil {
		return nil, nil
	}
	if observedMap == nil {
		observedMap = map[string]any{}
	}
	if leavesEqual(desiredMap, observedMap, communityListManagedLeaves) {
		return nil, nil
	}
	proj := projectManagedLeaves(desiredMap, communityListManagedLeaves)
	if w.resolverForUse().IsLegacyVersion(communityListFamily) {
		proj = communityListToYANG1716(proj)
	}
	if o, ok := w.resolverForUse().GetOverride(communityListFamily); ok {
		proj = ApplyOverrideToBody(proj, o)
	}
	body, err := wrapYANGPayload(communityListEnvelopeKey, proj)
	if err != nil {
		return nil, err
	}
	return []transport.Op{{
		Verb: transport.VerbMerge,
		Path: communityListYANGPath,
		Body: body,
	}}, nil
}

func (w communityListWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}

// ── 17.16 body transform (desired → YANG) ──────────────────────

// communityListToYANG1716 converts the netascode community-list body
// to the 17.16 deprecated YANG shape.
func communityListToYANG1716(body map[string]any) map[string]any {
	out := make(map[string]any, len(body))
	for k, v := range body {
		switch k {
		case "standard":
			out["standard"] = stdCommunityToYANG1716(v)
		case "expanded":
			out["expanded"] = expCommunityToYANG1716(v)
		default:
			out[k] = v
		}
	}
	return out
}

// stdCommunityToYANG1716 converts standard community list entries.
// Input:  [{name, community-list-entry: [{action, community}]}]
// Output: [{name, permit: {permit-list: [n]}, deny: {deny-list: [n]}}]
func stdCommunityToYANG1716(v any) []any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		result := map[string]any{}
		if n, ok := entry["name"]; ok {
			result["name"] = n
		}
		entries, _ := entry["community-list-entry"].([]any)
		var permitList, denyList []any
		for _, e := range entries {
			rule, ok := e.(map[string]any)
			if !ok {
				continue
			}
			action, _ := rule["action"].(string)
			community, _ := rule["community"].(string)
			num := communityStringToNum(community)
			switch action {
			case "permit":
				permitList = append(permitList, num)
			case "deny":
				denyList = append(denyList, num)
			}
		}
		if len(permitList) > 0 {
			result["permit"] = map[string]any{"permit-list": permitList}
		}
		if len(denyList) > 0 {
			result["deny"] = map[string]any{"deny-list": denyList}
		}
		out = append(out, result)
	}
	return out
}

// expCommunityToYANG1716 converts expanded community list entries.
// Input:  [{name, community-list-entry: [{action, regexp}]}]
// Output: [{name, extended-grouping: {extended_grouping: [{action, string}]}}]
func expCommunityToYANG1716(v any) []any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		result := map[string]any{}
		if n, ok := entry["name"]; ok {
			result["name"] = n
		}
		entries, _ := entry["community-list-entry"].([]any)
		var inner []any
		for _, e := range entries {
			rule, ok := e.(map[string]any)
			if !ok {
				continue
			}
			yang := map[string]any{}
			if a, ok := rule["action"]; ok {
				yang["action"] = a
			}
			if r, ok := rule["regexp"]; ok {
				yang["string"] = r
			}
			inner = append(inner, yang)
		}
		if len(inner) > 0 {
			result["extended-grouping"] = map[string]any{
				"extended_grouping": inner,
			}
		}
		out = append(out, result)
	}
	return out
}

// ── 17.16 fetch transform (YANG → netascode) ───────────────────

// communityListFromYANG1716 converts the 17.16 deprecated YANG shape
// back to the netascode community-list-entry shape.
func communityListFromYANG1716(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch k {
		case "standard":
			out["standard"] = stdCommunityFromYANG1716(v)
		case "expanded":
			out["expanded"] = expCommunityFromYANG1716(v)
		default:
			out[k] = v
		}
	}
	return out
}

// stdCommunityFromYANG1716 reverses stdCommunityToYANG1716.
func stdCommunityFromYANG1716(v any) []any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		result := map[string]any{}
		if n, ok := entry["name"]; ok {
			result["name"] = n
		}
		var cle []any
		if permit, ok := entry["permit"].(map[string]any); ok {
			if pl, ok := permit["permit-list"].([]any); ok {
				for _, n := range pl {
					cle = append(cle, map[string]any{
						"action":    "permit",
						"community": communityNumToString(n),
					})
				}
			}
		}
		if deny, ok := entry["deny"].(map[string]any); ok {
			if dl, ok := deny["deny-list"].([]any); ok {
				for _, n := range dl {
					cle = append(cle, map[string]any{
						"action":    "deny",
						"community": communityNumToString(n),
					})
				}
			}
		}
		if len(cle) > 0 {
			result["community-list-entry"] = cle
		}
		out = append(out, result)
	}
	return out
}

// expCommunityFromYANG1716 reverses expCommunityToYANG1716.
func expCommunityFromYANG1716(v any) []any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		result := map[string]any{}
		if n, ok := entry["name"]; ok {
			result["name"] = n
		}
		var cle []any
		if eg, ok := entry["extended-grouping"].(map[string]any); ok {
			if inner, ok := eg["extended_grouping"].([]any); ok {
				for _, e := range inner {
					rule, ok := e.(map[string]any)
					if !ok {
						continue
					}
					item := map[string]any{}
					if a, ok := rule["action"]; ok {
						item["action"] = a
					}
					if s, ok := rule["string"]; ok {
						item["regexp"] = s
					}
					cle = append(cle, item)
				}
			}
		}
		if len(cle) > 0 {
			result["community-list-entry"] = cle
		}
		out = append(out, result)
	}
	return out
}

// ── community string ↔ numeric conversion ───────────────────────

// communityStringToNum converts "ASN:NN" → uint32 (ASN<<16 | NN).
// Well-known names pass through as strings.
func communityStringToNum(s string) any {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return s
	}
	high, err1 := strconv.ParseUint(parts[0], 10, 16)
	low, err2 := strconv.ParseUint(parts[1], 10, 16)
	if err1 != nil || err2 != nil {
		return s
	}
	return float64(high<<16 | low)
}

// communityNumToString converts a numeric community back to "ASN:NN".
func communityNumToString(v any) string {
	var n uint64
	switch tv := v.(type) {
	case float64:
		n = uint64(math.Round(tv))
	case int:
		n = uint64(tv)
	case int64:
		n = uint64(tv)
	default:
		return fmt.Sprintf("%v", v)
	}
	high := n >> 16
	low := n & 0xFFFF
	return fmt.Sprintf("%d:%d", high, low)
}
