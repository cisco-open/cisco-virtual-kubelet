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

// CLIBlock is a rendered CLI template output plus the name of the
// template it came from. The engine emits one transport.Op with
// VerbCLI per block during Apply; the transport pushes via the
// device's Cisco-IA cli-config-data RPC.
//
// Ordering is the operator-visible order of spec.templateRefs on
// the IOSXEConfig CR, preserved across the resolver so CLI blocks
// that depend on each other (e.g. "interface + ip address under
// it") land on the device in the declared order.
type CLIBlock struct {
	// TemplateName is the name of the IOSXETemplate the block
	// came from. Carried for status reporting and for distinct
	// transport.Op grouping — each block is its own op so a
	// single block's failure does not roll back the others.
	TemplateName string

	// CLI is the rendered body: multi-line IOS-XE CLI text. Each
	// non-empty, trimmed line becomes one <cmd> when the
	// transport marshals it for Cisco-IA cli-config-data.
	CLI string
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

	// CLIBlocks carries CLI-type template expansions that do not
	// merge into Configuration. Populated by the resolver when
	// spec.templateRefs reference an IOSXETemplate with
	// spec.type=cli; consumed by the engine after family writes.
	CLIBlocks []CLIBlock

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

	// 3a) Interface groups, expanded per (device, interface) pair and
	//     merged before templates. Membership is the intersection of
	//     DeviceRefs / DeviceSelector and the device's InterfaceSelector
	//     match; only matching interfaces are projected.
	for _, groupName := range cr.Spec.InterfaceGroups {
		group, err := r.loadInterfaceGroup(ctx, cr.Namespace, groupName)
		if err != nil {
			return nil, err
		}
		if !r.interfaceGroupAppliesToDevice(device_, group) {
			// Device not a member: this is not an error (operators
			// commonly list a group on several CRs; non-matching devices
			// simply skip it).
			continue
		}
		expanded, err := r.expandInterfaceGroupForDevice(device_, group)
		if err != nil {
			return nil, fmt.Errorf("IOSXEInterfaceGroupConfig/%s: %w", group.Name, err)
		}
		configuration = asMap(MergeWithRules(configuration, expanded, r.KeyRules))
	}

	// 3b) Templates. Two types:
	//     - data-model templates render into a netascode fragment
	//       and merge into `configuration` like every other scope.
	//     - cli templates render into a plain text block that is
	//       carried separately on CLIBlocks and pushed via the
	//       Cisco-IA RPC at apply time. CLI blocks do not merge
	//       into the netascode tree because their output is text,
	//       not structured data.
	var cliBlocks []CLIBlock
	for _, ref := range cr.Spec.TemplateRefs {
		tpl, err := r.loadTemplate(ctx, cr.Namespace, ref.Name)
		if err != nil {
			return nil, err
		}
		values, err := rawExtensionToStringMap(ref.Values)
		if err != nil {
			return nil, fmt.Errorf("templateRefs[%s]: %w", ref.Name, err)
		}
		switch tpl.Spec.Type {
		case configv1alpha1.CLITemplate:
			cli, err := ExpandCLITemplate(tpl, values)
			if err != nil {
				return nil, err
			}
			cliBlocks = append(cliBlocks, CLIBlock{
				TemplateName: tpl.Name,
				CLI:          cli,
			})
		case "", configv1alpha1.DataModelTemplate:
			expanded, err := ExpandTemplate(tpl, values)
			if err != nil {
				return nil, err
			}
			configuration = asMap(MergeWithRules(configuration, expanded, r.KeyRules))
		default:
			return nil, fmt.Errorf(
				"templateRefs[%s]: unknown spec.type %q", ref.Name, tpl.Spec.Type)
		}
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
		CLIBlocks:       cliBlocks,
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

func (r *Resolver) loadInterfaceGroup(ctx context.Context, ns, name string) (*configv1alpha1.IOSXEInterfaceGroupConfig, error) {
	var group configv1alpha1.IOSXEInterfaceGroupConfig
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &group); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("IOSXEInterfaceGroupConfig %s/%s not found", ns, name)
		}
		return nil, fmt.Errorf("get IOSXEInterfaceGroupConfig %s/%s: %w", ns, name, err)
	}
	return &group, nil
}

// interfaceGroupAppliesToDevice checks the device-level membership
// filter (explicit refs and/or label selector). Interface-level
// filtering happens separately in expandInterfaceGroupForDevice —
// a device may match at the device level but have no interfaces that
// match at the interface level, in which case the group contributes
// nothing rather than failing resolution.
func (r *Resolver) interfaceGroupAppliesToDevice(device *ciskov1.CiscoDevice, group *configv1alpha1.IOSXEInterfaceGroupConfig) bool {
	if device == nil || group == nil {
		return false
	}
	// No device filter at all means "no members" per the
	// interface-group contract (documented on the CRD).
	explicit := len(group.Spec.DeviceRefs) > 0
	selector := group.Spec.DeviceSelector != nil
	if !explicit && !selector {
		return false
	}
	for _, ref := range group.Spec.DeviceRefs {
		if ref.Name == device.Name {
			return true
		}
	}
	if selector {
		sel, err := metav1.LabelSelectorAsSelector(group.Spec.DeviceSelector)
		if err == nil && sel.Matches(labels.Set(device.Labels)) {
			return true
		}
	}
	return false
}

// expandInterfaceGroupForDevice projects a group's Configuration body
// onto every (type, name) in InterfaceSelector that resolves to a
// real interface for this device (Phase-3 optimistically projects
// every selector entry; per-device interface enumeration against the
// device itself is a Phase-4 follow-up that requires operational-YANG
// reads).
//
// The projection replicates the group's configuration body for each
// matched interface, injecting the selector's (type, name) as the
// entry's key fields. Families that don't already carry a "type"/
// "name" key at the top level are left alone (rare in practice — the
// whole point of interface_groups is interface-keyed families).
func (r *Resolver) expandInterfaceGroupForDevice(device *ciskov1.CiscoDevice, group *configv1alpha1.IOSXEInterfaceGroupConfig) (map[string]any, error) {
	var body map[string]any
	if err := yaml.Unmarshal(group.Spec.Configuration.Raw, &body); err != nil {
		return nil, fmt.Errorf("decode configuration: %w", err)
	}
	if len(body) == 0 {
		return map[string]any{}, nil
	}

	out := map[string]any{}
	for _, match := range group.Spec.InterfaceSelector {
		projected := projectInterfaceEntry(body, match)
		out = asMap(MergeWithRules(out, projected, r.KeyRules))
	}
	return out, nil
}

// projectInterfaceEntry replicates a family block with the
// (type, name) key fields injected on every list element. The Phase-3
// contract: the group's Configuration body declares one or more
// interface families; each family's entries inherit the selector's
// type+name so operators don't repeat them per-entry.
func projectInterfaceEntry(body map[string]any, match configv1alpha1.InterfaceMatch) map[string]any {
	out := make(map[string]any, len(body))
	for famKey, famVal := range body {
		fam, ok := famVal.(map[string]any)
		if !ok {
			// Preserve non-family leaves verbatim.
			out[famKey] = famVal
			continue
		}
		newFam := make(map[string]any, len(fam))
		for k, v := range fam {
			list, isList := v.([]any)
			if !isList {
				newFam[k] = v
				continue
			}
			newList := make([]any, 0, len(list))
			for _, el := range list {
				m, ok := el.(map[string]any)
				if !ok {
					newList = append(newList, el)
					continue
				}
				entry := make(map[string]any, len(m)+2)
				for ek, ev := range m {
					entry[ek] = ev
				}
				if match.Type != "" {
					entry["type"] = match.Type
				}
				if match.Name != "" {
					entry["name"] = match.Name
				}
				newList = append(newList, entry)
			}
			newFam[k] = newList
		}
		out[famKey] = newFam
	}
	return out
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
