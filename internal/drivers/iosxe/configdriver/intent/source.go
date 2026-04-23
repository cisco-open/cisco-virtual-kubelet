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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

// ConfigMapReader is the minimum surface of a Kubernetes client the source
// loader needs. It is extracted as an interface so unit tests can supply a
// deterministic fake without pulling controller-runtime's fake client into
// intent's dependency graph.
type ConfigMapReader interface {
	Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error
}

// LoadSource materialises the body of an IOSXEConfig's spec.source into a
// netascode-shaped map[string]any. Exactly one of Inline or ConfigMapRef
// must be set; the loader rejects a source that violates that invariant
// before touching any external data.
//
// For ConfigMapRef, the referenced ConfigMap must live in ns and carry the
// named key. The body is decoded as YAML (which is a superset of JSON);
// the top-level shape may be either the netascode "iosxe.devices[*]"
// envelope or a direct configuration block. Both are accepted — if the
// envelope is present, the loader extracts the per-device configuration
// matching deviceName, else it returns the body verbatim.
func LoadSource(ctx context.Context, r ConfigMapReader, ns, deviceName string, src configv1alpha1.ConfigurationSource) (map[string]any, error) {
	inlineSet := src.Inline != nil && len(src.Inline.Raw) > 0
	configMapSet := src.ConfigMapRef != nil && src.ConfigMapRef.Name != ""
	if inlineSet == configMapSet {
		return nil, fmt.Errorf("spec.source: exactly one of inline or configMapRef must be set")
	}

	var raw []byte
	switch {
	case inlineSet:
		raw = src.Inline.Raw
	case configMapSet:
		var cm corev1.ConfigMap
		key := types.NamespacedName{Namespace: ns, Name: src.ConfigMapRef.Name}
		if err := r.Get(ctx, key, &cm); err != nil {
			return nil, fmt.Errorf("get ConfigMap %s/%s: %w", ns, src.ConfigMapRef.Name, err)
		}
		body, ok := cm.Data[src.ConfigMapRef.Key]
		if !ok {
			// BinaryData is never netascode YAML in practice; still include
			// the error message for diagnostic clarity.
			if _, binOk := cm.BinaryData[src.ConfigMapRef.Key]; binOk {
				return nil, fmt.Errorf("ConfigMap %s/%s key %q is binary; expected netascode YAML text", ns, src.ConfigMapRef.Name, src.ConfigMapRef.Key)
			}
			return nil, fmt.Errorf("ConfigMap %s/%s does not contain key %q", ns, src.ConfigMapRef.Name, src.ConfigMapRef.Key)
		}
		raw = []byte(body)
	}

	decoded, err := decodeNetascodeBody(raw, deviceName)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

// decodeNetascodeBody parses raw YAML/JSON and normalises it into the
// per-device configuration block shape the merger consumes. Two shapes
// are accepted:
//
//   - Envelope shape (full netascode file):
//       iosxe:
//         devices:
//           - name: <deviceName>
//             configuration: {...}
//     → returns the matching devices[].configuration block.
//
//   - Fragment shape (already a configuration block):
//       system: {...}
//       vlan: {...}
//     → returned verbatim.
//
// A fragment that happens to contain only the key "iosxe" is treated as
// an envelope to avoid accidental mis-classification.
func decodeNetascodeBody(raw []byte, deviceName string) (map[string]any, error) {
	var root any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse netascode body: %w", err)
	}
	top, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("netascode body top-level must be a mapping, got %T", root)
	}

	// Envelope path.
	if iosxe, hasIOSXE := top["iosxe"]; hasIOSXE {
		return extractDeviceFromEnvelope(iosxe, deviceName)
	}
	// Fragment path.
	return top, nil
}

func extractDeviceFromEnvelope(iosxe any, deviceName string) (map[string]any, error) {
	envelope, ok := iosxe.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("netascode envelope: .iosxe is %T, want mapping", iosxe)
	}
	devs, ok := envelope["devices"].([]any)
	if !ok {
		return nil, fmt.Errorf("netascode envelope: .iosxe.devices missing or not a list")
	}
	for _, d := range devs {
		dm, ok := d.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := dm["name"].(string); name != deviceName {
			continue
		}
		cfg, ok := dm["configuration"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("netascode envelope: device %q has no configuration block", deviceName)
		}
		return cfg, nil
	}
	return nil, fmt.Errorf("netascode envelope: device %q not present under .iosxe.devices", deviceName)
}
