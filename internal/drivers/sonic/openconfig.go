// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package sonic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

type OperationVerb string

const (
	OperationReplace OperationVerb = "replace"
	OperationUpdate  OperationVerb = "update"
	OperationDelete  OperationVerb = "delete"
)

// OpenConfigOperation is one OpenConfig gNMI Set operation.
type OpenConfigOperation struct {
	Verb  OperationVerb   `json:"verb"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value,omitempty"`
}

// OpenConfigIntent is the resolved SONiC OpenConfig reconcile request.
type OpenConfigIntent struct {
	DeviceName   string
	ManagedPaths []string
	Operations   []OpenConfigOperation
	DriftPolicy  configv1alpha1.DriftPolicy
}

type FamilyResult struct {
	Name    string
	State   string
	Entries int32
	OpCount int32
	Message string
}

type DriftResult struct {
	Family   string
	Path     string
	Desired  string
	Observed string
}

type ApplyResult struct {
	FamilyResults []FamilyResult
	Drift         []DriftResult
	Applied       bool
}

type ConfigMapReader interface {
	Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error
}

type OpenConfigApplier struct {
	client gnmiClient
}

func NewOpenConfigApplier(spec *v1alpha1.DeviceSpec, password string) (*OpenConfigApplier, error) {
	client, err := newGNMIClientFromSpec(spec, password)
	if err != nil {
		return nil, err
	}
	return &OpenConfigApplier{client: client}, nil
}

func NewOpenConfigApplierWithClient(client gnmiClient) *OpenConfigApplier {
	return &OpenConfigApplier{client: client}
}

func (a *OpenConfigApplier) Close() error {
	if a == nil || a.client == nil {
		return nil
	}
	return a.client.Close()
}

func (a *OpenConfigApplier) Health(ctx context.Context) error {
	if a == nil || a.client == nil {
		return fmt.Errorf("sonic openconfig applier: nil client")
	}
	_, err := a.client.Capabilities(ctx)
	return err
}

func (a *OpenConfigApplier) Apply(ctx context.Context, intent OpenConfigIntent) (ApplyResult, error) {
	if a == nil || a.client == nil {
		return ApplyResult{}, fmt.Errorf("sonic openconfig applier: nil client")
	}
	if err := ValidateManagedPaths(intent.ManagedPaths); err != nil {
		return ApplyResult{}, err
	}
	if err := ValidateOperations(intent.Operations, intent.ManagedPaths); err != nil {
		return ApplyResult{}, err
	}
	if err := a.Health(ctx); err != nil {
		return ApplyResult{}, err
	}
	result := ApplyResult{FamilyResults: make([]FamilyResult, 0, len(intent.ManagedPaths))}
	opsByManagedPath := map[string]int32{}
	for _, op := range intent.Operations {
		owner := owningManagedPath(op.Path, intent.ManagedPaths)
		opsByManagedPath[owner]++
		if intent.DriftPolicy == configv1alpha1.DriftPolicyReport {
			result.Drift = append(result.Drift, DriftResult{Family: owner, Path: op.Path, Desired: string(op.Value), Observed: "not checked in report-only mode"})
		}
	}
	if intent.DriftPolicy == configv1alpha1.DriftPolicyReport {
		for _, path := range intent.ManagedPaths {
			count := opsByManagedPath[path]
			state := "InSync"
			msg := "no OpenConfig operations declared"
			if count > 0 {
				state = "Drifted"
				msg = "report-only: OpenConfig operations were planned but not written"
			}
			result.FamilyResults = append(result.FamilyResults, FamilyResult{Name: path, State: state, Entries: count, OpCount: count, Message: msg})
		}
		return result, nil
	}
	if err := a.client.Set(ctx, intent.Operations); err != nil {
		return result, err
	}
	result.Applied = len(intent.Operations) > 0
	for _, path := range intent.ManagedPaths {
		count := opsByManagedPath[path]
		msg := "OpenConfig path is in sync"
		if count == 0 {
			msg = "no OpenConfig operations declared"
		}
		result.FamilyResults = append(result.FamilyResults, FamilyResult{Name: path, State: "InSync", Entries: count, OpCount: count, Message: msg})
	}
	return result, nil
}

func LoadSource(ctx context.Context, r ConfigMapReader, ns string, src configv1alpha1.ConfigurationSource) ([]OpenConfigOperation, error) {
	inlineSet := src.Inline != nil && len(src.Inline.Raw) > 0
	configMapSet := src.ConfigMapRef != nil && src.ConfigMapRef.Name != ""
	if inlineSet == configMapSet {
		return nil, fmt.Errorf("spec.source: exactly one of inline or configMapRef must be set")
	}
	var raw []byte
	if inlineSet {
		raw = src.Inline.Raw
	} else {
		var cm corev1.ConfigMap
		key := types.NamespacedName{Namespace: ns, Name: src.ConfigMapRef.Name}
		if err := r.Get(ctx, key, &cm); err != nil {
			return nil, fmt.Errorf("get ConfigMap %s/%s: %w", ns, src.ConfigMapRef.Name, err)
		}
		body, ok := cm.Data[src.ConfigMapRef.Key]
		if !ok {
			return nil, fmt.Errorf("ConfigMap %s/%s does not contain key %q", ns, src.ConfigMapRef.Name, src.ConfigMapRef.Key)
		}
		raw = []byte(body)
	}
	return DecodeOpenConfigOperations(raw)
}

func DecodeOpenConfigOperations(raw []byte) ([]OpenConfigOperation, error) {
	var root any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	body, ok := normalizeMap(root)
	if !ok {
		return nil, fmt.Errorf("OpenConfig source must be a mapping")
	}
	if nested, ok := body["openconfig"]; ok {
		if m, ok := normalizeMap(nested); ok {
			body = m
		} else {
			return nil, fmt.Errorf("openconfig envelope must be a mapping")
		}
	}
	var ops []OpenConfigOperation
	for _, key := range []struct {
		name string
		verb OperationVerb
	}{
		{"replace", OperationReplace},
		{"replaces", OperationReplace},
		{"update", OperationUpdate},
		{"updates", OperationUpdate},
		{"delete", OperationDelete},
		{"deletes", OperationDelete},
	} {
		v, ok := body[key.name]
		if !ok || v == nil {
			continue
		}
		parsed, err := parseOperationSet(key.verb, v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key.name, err)
		}
		ops = append(ops, parsed...)
	}
	return ops, nil
}

func parseOperationSet(verb OperationVerb, v any) ([]OpenConfigOperation, error) {
	if m, ok := normalizeMap(v); ok {
		ops := make([]OpenConfigOperation, 0, len(m))
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, path := range keys {
			raw, err := rawJSON(m[path])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			ops = append(ops, OpenConfigOperation{Verb: verb, Path: path, Value: raw})
		}
		return ops, nil
	}
	list, ok := normalizeSlice(v)
	if !ok {
		return nil, fmt.Errorf("operation set must be a map or list")
	}
	ops := make([]OpenConfigOperation, 0, len(list))
	for i, item := range list {
		if s, ok := item.(string); ok {
			if verb != OperationDelete {
				return nil, fmt.Errorf("item[%d]: string form is valid only for delete", i)
			}
			ops = append(ops, OpenConfigOperation{Verb: verb, Path: s})
			continue
		}
		m, ok := normalizeMap(item)
		if !ok {
			return nil, fmt.Errorf("item[%d] must be a mapping", i)
		}
		path, _ := m["path"].(string)
		if path == "" {
			return nil, fmt.Errorf("item[%d].path is required", i)
		}
		var value json.RawMessage
		if verb != OperationDelete {
			if _, ok := m["value"]; !ok {
				return nil, fmt.Errorf("item[%d].value is required", i)
			}
			raw, err := rawJSON(m["value"])
			if err != nil {
				return nil, fmt.Errorf("item[%d].value: %w", i, err)
			}
			value = raw
		}
		ops = append(ops, OpenConfigOperation{Verb: verb, Path: path, Value: value})
	}
	return ops, nil
}

func ValidateManagedPaths(paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("spec.managedPaths must not be empty")
	}
	seen := map[string]bool{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || !strings.HasPrefix(path, "/") {
			return fmt.Errorf("managed path %q must start with '/'", path)
		}
		if seen[path] {
			return fmt.Errorf("duplicate managed path %q", path)
		}
		seen[path] = true
	}
	return nil
}

func ValidateOperations(ops []OpenConfigOperation, managedPaths []string) error {
	for i, op := range ops {
		if op.Path == "" || !strings.HasPrefix(op.Path, "/") {
			return fmt.Errorf("operation[%d].path must start with '/'", i)
		}
		if owningManagedPath(op.Path, managedPaths) == "" {
			return fmt.Errorf("operation[%d].path %q is outside managedPaths", i, op.Path)
		}
		switch op.Verb {
		case OperationReplace, OperationUpdate:
			if len(op.Value) == 0 {
				return fmt.Errorf("operation[%d].value is required for %s", i, op.Verb)
			}
		case OperationDelete:
		default:
			return fmt.Errorf("operation[%d].verb %q is unsupported", i, op.Verb)
		}
	}
	return nil
}

func owningManagedPath(path string, managed []string) string {
	for _, m := range managed {
		if path == m || strings.HasPrefix(path, strings.TrimRight(m, "/")+"/") {
			return m
		}
	}
	return ""
}

func CanonicalHash(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func rawJSON(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

func normalizeMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[fmt.Sprint(k)] = v
		}
		return out, true
	default:
		return nil, false
	}
}

func normalizeSlice(v any) ([]any, bool) {
	switch s := v.(type) {
	case []any:
		return s, true
	default:
		return nil, false
	}
}
