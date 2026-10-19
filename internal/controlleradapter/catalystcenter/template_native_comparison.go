// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const templateNativeComparisonScope = "/projects"

func templateNativeComparisonConfig(config *configapi.NetworkControllerConfig) bool {
	return config != nil && len(config.Spec.ManagedSections) == 1 && config.Spec.ManagedSections[0] == "templates" && len(config.Spec.SecretRefs) == 1 && config.Spec.SecretRefs[0].Section == "templates" && config.Spec.SecretRefs[0].Path == templateNativeComparisonScope && config.Spec.SecretRefs[0].Use == "PrivateComparisonKey"
}
func (a *adapter) compilePrivateTemplateDefinitions(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	var zero configurationApplyPlan
	if !templateNativeComparisonConfig(config) || source == nil {
		return zero, errConfigurationSecret
	}
	// Parse every definition/file before opening an operational key projection.
	plan, err := a.client.compileConfigurationApply(source.raw, config.Spec.ManagedSections, source.templateFiles)
	if err != nil {
		return zero, err
	}
	comparison, err := a.resolveTemplateComparison(nc, config, source, "templates", templateNativeComparisonScope, "cvk-template-native-key-v1")
	if err != nil {
		return zero, err
	}
	source.templateComparison = comparison
	return plan, nil
}
func templateCreatorComparisonKey(resource string) string {
	if _, ok := templateCreateKey(resource); !ok {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(resource, templateCreatePrefix), ":")
	if len(parts) == 5 {
		return parts[4]
	}
	return ""
}
func templateNativeMAC(comparison *templateInputComparison, identity string, snapshot any) (string, error) {
	if comparison == nil || len(comparison.key) != 32 || !validTemplateComparisonDigest(comparison.keyID) {
		return "", errConfigurationSecret
	}
	key, ok := templateCreateKey(identity)
	if !ok {
		return "", errConfigurationTransition
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return "", errInvalidResponse
	}
	mac := hmac.New(sha256.New, comparison.key)
	mac.Write([]byte("cvk-template-native-state-v1\x00" + key + "\x00"))
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil)), nil
}
func templateCreatorResultResource(resource, id, mac string) string {
	key, ok := templateCreateKey(resource)
	if !ok || !canonicalTemplateUUID(id) {
		return ""
	}
	result := templateCreatedPrefix + key + ":" + id
	comparisonKey := templateCreatorComparisonKey(resource)
	if comparisonKey != "" {
		if !validTemplateComparisonDigest(mac) {
			return ""
		}
		result += ":" + templateComparisonTag + ":" + comparisonKey + ":" + mac
	} else if mac != "" {
		return ""
	}
	return result
}
func templateCreatorBaseline(r cvk.NetworkControllerConfigurationActionApplication) (string, string, error) {
	if _, err := templateCreatedTarget(r); err != nil {
		return "", "", err
	}
	key := templateCreatorComparisonKey(r.Operation.Resource)
	if key == "" {
		return "", "", nil
	}
	parts := strings.Split(r.Resource, ":")
	return key, parts[len(parts)-1], nil
}

// Read-only comparison of a captured historical footprint. Callers must still
// establish creator authority, current references and a latest-applied baseline
// before planning any destructive action; a matching MAC alone grants no permit.
func matchTemplateCreatorBaseline(comparison *templateInputComparison, receipt cvk.NetworkControllerConfigurationActionApplication, snapshot any) (bool, error) {
	key, expected, err := templateCreatorBaseline(receipt)
	if err != nil {
		return false, err
	}
	if key == "" {
		return false, errConfigurationOwned
	}
	if comparison == nil || comparison.keyID != key {
		return false, errConfigurationSecret
	}
	actual, err := templateNativeMAC(comparison, receipt.Operation.Resource, snapshot)
	if err != nil {
		return false, err
	}
	return hmac.Equal([]byte(expected), []byte(actual)), nil
}

// Capture full native creation state, including unknown fields and version or
// child references returned by the native detail API. This is historical creator
// evidence, not a latest-applied baseline or authority for destructive removal.
func (e *templateOwnedExecutor) captureCreatorBaseline(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, id string) (string, error) {
	return e.captureTemplateBaseline(ctx, op, id, false)
}

func (e *templateOwnedExecutor) captureTemplateBaseline(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, id string, history bool) (string, error) {
	expected := templateCreatorComparisonKey(op.Resource)
	if expected == "" {
		return "", nil
	}
	if e.comparison == nil || e.comparison.keyID != expected || e.comparison.check == nil {
		return "", errConfigurationSecret
	}
	b, ok := e.find(op)
	if !ok {
		return "", errConfigurationTransition
	}
	read := func() (any, error) {
		if err := e.comparison.check(ctx); err != nil {
			return nil, err
		}
		var body map[string]any
		var raw []byte
		var err error
		if b.kind == "p" {
			raw, err = e.client.get(ctx, templateProjectsPath+"/"+id, nil, 200)
			if err != nil {
				return nil, err
			}
			if json.Unmarshal(raw, &body) != nil {
				return nil, errInvalidResponse
			}
		} else {
			raw, err = e.client.get(ctx, templatesPath+"/"+id, url.Values{"unCommitted": {"true"}}, 200)
			if err != nil {
				return nil, err
			}
			if json.Unmarshal(raw, &body) != nil {
				return nil, errInvalidResponse
			}
		}
		if body["id"] != id || body["name"] != b.name {
			return nil, errInvalidResponse
		}
		if b.kind != "p" && (body["projectId"] != op.ParentID || body["composite"] != (b.kind == "c")) {
			return nil, errConfigurationOwned
		}
		if b.snapshotMatches == nil {
			return nil, errConfigurationTransition
		}
		if err := b.snapshotMatches(ctx, body); err != nil {
			return nil, err
		}
		exact, err := decodeTemplateNativeJSON(raw)
		if err != nil {
			return nil, err
		}
		if history && b.kind != "p" {
			versions, err := e.client.templateVersionFootprint(ctx, templateSummary{ID: id, Name: b.name, ProjectID: op.ParentID})
			if err != nil {
				return nil, err
			}
			return map[string]any{"definition": exact, "history": versions}, nil
		}
		return exact, nil
	}
	first, err := read()
	if err != nil {
		return "", err
	}
	mac, err := templateNativeMAC(e.comparison, op.Resource, first)
	if err != nil {
		return "", err
	}
	second, err := read()
	if err != nil {
		return "", err
	}
	again, err := templateNativeMAC(e.comparison, op.Resource, second)
	if err != nil {
		return "", err
	}
	if !hmac.Equal([]byte(mac), []byte(again)) {
		return "", errConfigurationStale
	}
	if err := e.comparison.check(ctx); err != nil {
		return "", err
	}
	return mac, nil
}

// Retain numeric precision in unknown native fields; float64 would collapse
// distinct integers above 2^53 into the same comparison input. Only explicit
// non-custom parameter record order is canonicalized; every field/value remains.
func decodeTemplateNativeJSON(raw []byte) (any, error) {
	value, err := decodeExactNativeJSON(raw)
	if err != nil {
		return nil, err
	}
	if body, ok := value.(map[string]any); ok {
		if err := canonicalizeTemplateParameterOrder(body); err != nil {
			return nil, err
		}
	}
	return value, nil
}

// Decode native JSON without resource-specific normalization. Unknown fields,
// array order, null/absence and exact numeric representations remain significant.
func decodeExactNativeJSON(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, errInvalidResponse
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errInvalidResponse
	}
	return value, nil
}
