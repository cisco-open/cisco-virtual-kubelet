// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// An input fingerprint is not a creator receipt, a native effect proof or a
// submission permit. It contains only public intent, resolved global IDs and
// version/source labels; no secret bytes or unkeyed hashes of secret bytes.
const discoveryCredentialInputPrefix = "discovery-credential-input:v1:"
const discoveryCredentialContinuationPrefix = "discovery-credential-input:v2:"
const discoveryCredentialScopePrefix = "discovery-credential-input:v3:"

// v4 explicitly represents an existing versioned owner's metadata-only job.
// Older formats still require projected versions; missing bindings cannot be
// reinterpreted as a metadata-only incarnation.
const discoveryCredentialMetadataScopePrefix = "discovery-credential-input:v4:"

func discoveryCredentialInputScopePrefix(resource string) string {
	for _, prefix := range []string{discoveryCredentialScopePrefix, discoveryCredentialMetadataScopePrefix} {
		if strings.HasPrefix(resource, prefix) {
			return prefix
		}
	}
	return ""
}

// Legacy replacement declarations retain a canonical base; v4 also retains
// its exact public scope, because no credential bindings select this subtype.
func discoveryCredentialRetirementNextMatches(next, input string) bool {
	if strings.HasPrefix(next, discoveryCredentialMetadataScopePrefix) {
		return discoveryCredentialInputPrototype(input) == next
	}
	base, _, ok := discoveryCredentialInputParts(input)
	return ok && next == base
}

// A replacement incarnation retains the same semantic input plus the exact
// completed predecessor retirement. Reusing an earlier semantic input cannot
// overwrite that incarnation's application evidence.
func discoveryCredentialInputParts(resource string) (base, context string, ok bool) {
	var parts []string
	prefix := discoveryCredentialInputScopePrefix(resource)
	switch {
	case prefix != "":
		parts = strings.Split(strings.TrimPrefix(resource, prefix), ":")
		if len(parts) != 6 {
			return "", "", false
		}
		mask, err := strconv.ParseUint(parts[3], 16, 64)
		if err != nil || strconv.FormatUint(mask, 16) != parts[3] || !validDiscoveryCredentialEffectMask(mask) || !lowerDiscoveryDigest(parts[4]) {
			return "", "", false
		}
		if parts[5] != "-" {
			if !lowerDiscoveryDigest(parts[5]) {
				return "", "", false
			}
			context = parts[5]
		}
		parts = parts[:3]
	case strings.HasPrefix(resource, discoveryCredentialInputPrefix):
		parts = strings.Split(strings.TrimPrefix(resource, discoveryCredentialInputPrefix), ":")
		if len(parts) != 3 {
			return "", "", false
		}
	case strings.HasPrefix(resource, discoveryCredentialContinuationPrefix):
		parts = strings.Split(strings.TrimPrefix(resource, discoveryCredentialContinuationPrefix), ":")
		if len(parts) != 4 || !lowerDiscoveryDigest(parts[3]) {
			return "", "", false
		}
		context = parts[3]
		parts = parts[:3]
	default:
		return "", "", false
	}
	for _, part := range parts {
		if !lowerDiscoveryDigest(part) {
			return "", "", false
		}
	}
	if context != "" && !lowerDiscoveryDigest(context) {
		return "", "", false
	}
	return discoveryCredentialInputPrefix + strings.Join(parts, ":"), context, true
}

// Return a context-free prototype while retaining any pre-submission scope.
func discoveryCredentialInputPrototype(resource string) string {
	base, _, ok := discoveryCredentialInputParts(resource)
	if !ok {
		return ""
	}
	if prefix := discoveryCredentialInputScopePrefix(resource); prefix != "" {
		parts := strings.Split(strings.TrimPrefix(resource, prefix), ":")
		parts[5] = "-"
		return prefix + strings.Join(parts, ":")
	}
	return base
}
func discoveryCredentialRetainedEffect(resource string) (discoveryCredentialPublicEffect, bool) {
	prefix := discoveryCredentialInputScopePrefix(resource)
	if prefix == "" {
		return discoveryCredentialPublicEffect{}, false
	}
	if _, _, ok := discoveryCredentialInputParts(resource); !ok {
		return discoveryCredentialPublicEffect{}, false
	}
	parts := strings.Split(strings.TrimPrefix(resource, prefix), ":")
	mask, _ := strconv.ParseUint(parts[3], 16, 64)
	return discoveryCredentialPublicEffect{mask: mask, digest: parts[4]}, true
}
func discoveryCredentialScopedInput(base string, effect discoveryCredentialPublicEffect) string {
	return discoveryCredentialScopedInputAs(base, effect, discoveryCredentialScopePrefix)
}
func discoveryCredentialScopedInputAs(base string, effect discoveryCredentialPublicEffect, prefix string) string {
	if prefix != discoveryCredentialScopePrefix && prefix != discoveryCredentialMetadataScopePrefix {
		return ""
	}
	canonical, context, ok := discoveryCredentialInputParts(base)
	if !ok || canonical != base || context != "" || !validDiscoveryCredentialEffectMask(effect.mask) || !lowerDiscoveryDigest(effect.digest) {
		return ""
	}
	return prefix + strings.TrimPrefix(base, discoveryCredentialInputPrefix) + ":" + strconv.FormatUint(effect.mask, 16) + ":" + effect.digest + ":-"
}
func discoveryCredentialContinuation(base, context string) string {
	canonical, prior, ok := discoveryCredentialInputParts(base)
	if !ok || prior != "" || !lowerDiscoveryDigest(context) {
		return ""
	}
	if discoveryCredentialInputScopePrefix(base) != "" {
		return strings.TrimSuffix(base, "-") + context
	}
	if canonical != base {
		return ""
	}
	return discoveryCredentialContinuationPrefix + strings.TrimPrefix(base, discoveryCredentialInputPrefix) + ":" + context
}

type discoveryCredentialVersionBinding struct {
	Field                        string
	Version                      int64
	Alias, SecretName, SecretKey string
}

func (p *discoveryCredentialProjection) bindRequest(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot, index int, globals map[string]map[string]globalCredentialMetadata) (*discoveryCredentialRequest, string, error) {
	request, err := p.requestFields(nc, c, source, index)
	if err != nil {
		return nil, "", err
	}
	want := p.intents[index]
	// Use the existing name/ambiguity/UUID resolver; do not introduce a second
	// lookup, convert names to fake IDs or silently drop mixed global references.
	public, err := resolveDiscoveryCredentials(want.base, globals)
	if err != nil {
		return nil, "", err
	}
	if len(want.base.credentials) == 0 {
		delete(public, "globalCredentialIdList")
	} else {
		request.fields["globalCredentialIdList"] = public["globalCredentialIdList"]
	}
	for k, v := range want.metadata {
		public[k] = v
	}
	bindings := []discoveryCredentialVersionBinding{}
	for _, version := range request.versions {
		field := ""
		for _, leaf := range want.credentials {
			if leaf.path == version.Path {
				if field != "" {
					return nil, "", errConfigurationSecret
				}
				field = leaf.field
			}
		}
		if field == "" {
			return nil, "", errConfigurationSecret
		}
		bindings = append(bindings, discoveryCredentialVersionBinding{Field: field, Version: version.Version, Alias: version.SourceAlias, SecretName: version.SecretName, SecretKey: version.SecretKey})
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Field < bindings[j].Field })
	for i := range bindings {
		if i > 0 && bindings[i].Field == bindings[i-1].Field {
			return nil, "", errConfigurationSecret
		}
	}
	resource := discoveryCredentialInputPrefix + discoveryPublicDigest(want.base.name) + ":" + featureIdentityDigest(public) + ":" + featureIdentityDigest(bindings)
	if p.tracks(want) {
		effect, err := discoveryCredentialEffect(public)
		if err != nil {
			return nil, "", err
		}
		prefix := discoveryCredentialScopePrefix
		if len(want.credentials) == 0 {
			prefix = discoveryCredentialMetadataScopePrefix
		}
		resource = discoveryCredentialScopedInputAs(resource, effect, prefix)
		if resource == "" {
			return nil, "", errConfigurationTransition
		}
	}
	if len(resource) > 512 {
		return nil, "", errConfigurationTransition
	}
	// The ordinary discovery fingerprint must never see credential-bearing data.
	// Return a fresh complete body only after the separate public identity exists.
	raw, err := json.Marshal(request.fields)
	if err != nil {
		return nil, "", errConfigurationSecret
	}
	var frozen map[string]any
	if json.Unmarshal(raw, &frozen) != nil {
		return nil, "", errConfigurationSecret
	}
	request.fields = frozen
	// Retain a detached allowlisted view for task-bound metadata verification.
	// Never derive this by deleting password-looking keys from the full body.
	raw, err = json.Marshal(public)
	if err != nil || json.Unmarshal(raw, &request.public) != nil {
		return nil, "", errConfigurationSecret
	}
	return request, resource, nil
}
