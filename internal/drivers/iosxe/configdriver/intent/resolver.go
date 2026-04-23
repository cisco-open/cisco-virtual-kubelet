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

package intent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Reader is the subset of controller-runtime's client.Client the resolver
// needs. It is the minimum useful surface so fakes don't have to implement
// write methods.
type Reader interface {
	Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error
	List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
}

// Resolver composes the four netascode scopes plus the per-device source
// into the ResolvedIntent the config driver applies. It is safe to reuse
// across reconciles: the struct itself carries no per-call state.
type Resolver struct {
	// Client reads IOSXEConfigDefaults (cluster-scope),
	// IOSXEDeviceGroupConfig (namespaced), IOSXETemplate (namespaced),
	// the CiscoDevice the CR targets, and any ConfigMap referenced from
	// the source.
	Client Reader

	// KeyRules is the family-aware path → key-field map built from the
	// families.yaml schema. Empty is valid — the merger falls back to
	// the name>id heuristic.
	KeyRules KeyRules
}

// ResolvedIntent is the output of Resolve. It is a plain-data value
// suitable for hashing, logging, or sending to a family writer.
type ResolvedIntent struct {
	DeviceName      string
	ManagedFamilies []string
	Configuration   map[string]any
	Transactional   bool
	DriftPolicy     configv1alpha1.DriftPolicy
	WriteStartup    bool

	// SourceCR is a deep-copy of the per-device CR the intent was
	// resolved from, for status writes and event recording.
	SourceCR *configv1alpha1.IOSXEConfig
}

// Resolve materialises a ResolvedIntent from the supplied CR. The scopes
// are merged in netascode precedence order:
//
//	IOSXEConfigDefaults → IOSXEDeviceGroupConfig → IOSXETemplate → per-CR source
//
// Failures during any stage return early with a wrapped error that
// identifies the offending object so the caller can surface it on the
// CR's status without needing to re-parse the error.
func (r *Resolver) Resolve(ctx context.Context, cr *configv1alpha1.IOSXEConfig) (*ResolvedIntent, error) {
	if cr == nil {
		return nil, fmt.Errorf("Resolve: nil IOSXEConfig")
	}
	device := cr.Spec.DeviceRef.Name
	if device == "" {
		return nil, fmt.Errorf("Resolve: spec.deviceRef.name is empty")
	}

	// 1) Cluster-scoped defaults, merged in deterministic (name) order.
	var defaultsList configv1alpha1.IOSXEConfigDefaultsList
	if err := r.Client.List(ctx, &defaultsList); err != nil {
		return nil, fmt.Errorf("list IOSXEConfigDefaults: %w", err)
	}
	sort.SliceStable(defaultsList.Items, func(i, j int) bool {
		return defaultsList.Items[i].Name < defaultsList.Items[j].Name
	})
	configuration := map[string]any{}
	for i := range defaultsList.Items {
		block, err := decodeConfigurationBlock(defaultsList.Items[i].Spec.Configuration.Raw,
			fmt.Sprintf("IOSXEConfigDefaults/%s", defaultsList.Items[i].Name))
		if err != nil {
			return nil, err
		}
		configuration = asMap(MergeWithRules(configuration, block, r.KeyRules))
	}

	// 2) Device groups, merged in the order they are listed on the CR.
	//    Matching is by name OR label selector on the target CiscoDevice.
	device_, err := r.loadDevice(ctx, cr.Namespace, device)
	if err != nil {
		return nil, err
	}
	for _, groupName := range cr.Spec.DeviceGroups {
		group, err := r.loadDeviceGroup(ctx, cr.Namespace, groupName)
		if err != nil {
			return nil, err
		}
		if !deviceMatchesGroup(device_, group) {
			return nil, fmt.Errorf("IOSXEConfig %s/%s: device %q is not a member of group %q",
				cr.Namespace, cr.Name, device, groupName)
		}
		block, err := decodeConfigurationBlock(group.Spec.Configuration.Raw,
			fmt.Sprintf("IOSXEDeviceGroupConfig/%s", group.Name))
		if err != nil {
			return nil, err
		}
		configuration = asMap(MergeWithRules(configuration, block, r.KeyRules))
	}

	// 3) Templates, expanded with the caller-supplied values, merged in
	//    the order they appear on the CR.
	for _, ref := range cr.Spec.TemplateRefs {
		tpl, err := r.loadTemplate(ctx, cr.Namespace, ref.Name)
		if err != nil {
			return nil, err
		}
		values, err := rawExtensionToStringMap(ref.Values)
		if err != nil {
			return nil, fmt.Errorf("templateRefs[%s]: %w", ref.Name, err)
		}
		expanded, err := ExpandTemplate(tpl, values)
		if err != nil {
			return nil, err
		}
		configuration = asMap(MergeWithRules(configuration, expanded, r.KeyRules))
	}

	// 4) Per-device source — either inline or ConfigMap-borne.
	source, err := LoadSource(ctx, r.Client, cr.Namespace, device, cr.Spec.Source)
	if err != nil {
		return nil, fmt.Errorf("IOSXEConfig %s/%s: %w", cr.Namespace, cr.Name, err)
	}
	configuration = asMap(MergeWithRules(configuration, source, r.KeyRules))

	policy := cr.Spec.DriftPolicy
	if policy == "" {
		policy = configv1alpha1.DriftPolicyRevert
	}

	return &ResolvedIntent{
		DeviceName:      device,
		ManagedFamilies: append([]string(nil), cr.Spec.ManagedFamilies...),
		Configuration:   configuration,
		Transactional:   cr.Spec.Transactional,
		DriftPolicy:     policy,
		WriteStartup:    cr.Spec.WriteStartup,
		SourceCR:        cr.DeepCopy(),
	}, nil
}

func (r *Resolver) loadDevice(ctx context.Context, ns, name string) (*ciskov1.CiscoDevice, error) {
	var dev ciskov1.CiscoDevice
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &dev); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("CiscoDevice %s/%s not found", ns, name)
		}
		return nil, fmt.Errorf("get CiscoDevice %s/%s: %w", ns, name, err)
	}
	return &dev, nil
}

func (r *Resolver) loadDeviceGroup(ctx context.Context, ns, name string) (*configv1alpha1.IOSXEDeviceGroupConfig, error) {
	var group configv1alpha1.IOSXEDeviceGroupConfig
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &group); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("IOSXEDeviceGroupConfig %s/%s not found", ns, name)
		}
		return nil, fmt.Errorf("get IOSXEDeviceGroupConfig %s/%s: %w", ns, name, err)
	}
	return &group, nil
}

func (r *Resolver) loadTemplate(ctx context.Context, ns, name string) (*configv1alpha1.IOSXETemplate, error) {
	var tpl configv1alpha1.IOSXETemplate
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &tpl); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("IOSXETemplate %s/%s not found", ns, name)
		}
		return nil, fmt.Errorf("get IOSXETemplate %s/%s: %w", ns, name, err)
	}
	return &tpl, nil
}

// deviceMatchesGroup reports whether the named device satisfies the
// group's inclusion rules (explicit refs or label selector). A group with
// neither rule set matches nothing — the reviewer should see the group is
// empty rather than the inclusion silently universal.
func deviceMatchesGroup(device *ciskov1.CiscoDevice, group *configv1alpha1.IOSXEDeviceGroupConfig) bool {
	if device == nil || group == nil {
		return false
	}
	for _, ref := range group.Spec.DeviceRefs {
		if ref.Name == device.Name {
			return true
		}
	}
	if group.Spec.DeviceSelector != nil {
		sel, err := metav1.LabelSelectorAsSelector(group.Spec.DeviceSelector)
		if err != nil {
			return false
		}
		if sel.Matches(labels.Set(device.Labels)) {
			return true
		}
	}
	return false
}

// rawExtensionToStringMap turns the caller-supplied RawExtension values
// into a string-keyed string map. Scalar values pass through; non-scalars
// are serialised to JSON so the template engine sees a deterministic
// string representation.
func rawExtensionToStringMap(raw *runtime.RawExtension) (map[string]string, error) {
	if raw == nil || len(raw.Raw) == 0 {
		return map[string]string{}, nil
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(raw.Raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse values: %w", err)
	}
	out := make(map[string]string, len(parsed))
	for k, v := range parsed {
		switch tv := v.(type) {
		case string:
			out[k] = tv
		case bool:
			if tv {
				out[k] = "true"
			} else {
				out[k] = "false"
			}
		case float64, int, int64:
			out[k] = fmt.Sprintf("%v", tv)
		case nil:
			out[k] = ""
		default:
			b, err := json.Marshal(tv)
			if err != nil {
				return nil, fmt.Errorf("serialise value %q: %w", k, err)
			}
			out[k] = string(b)
		}
	}
	return out, nil
}

// asMap coerces the merger's any return to the resolver's map type. Panics
// on a non-map root, which is a programming error — Merge at the top level
// is always map-on-map.
func asMap(v any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	m, ok := v.(map[string]any)
	if !ok {
		panic(fmt.Sprintf("intent.Resolver: expected map[string]any at top level, got %T", v))
	}
	return m
}

func decodeConfigurationBlock(raw []byte, origin string) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: parse: %w", origin, err)
	}
	return m, nil
}
