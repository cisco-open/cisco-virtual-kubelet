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
	"sigs.k8s.io/yaml"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

// LoadPlatformSource materialises a common config source into the
// per-device configuration block shape the engine consumes. platformEnvelope
// is the top-level Network-as-Code key, such as "nxos" or "iosxr".
func LoadPlatformSource(
	ctx context.Context,
	r ConfigMapReader,
	ns, deviceName, platformEnvelope string,
	src configv1alpha1.ConfigurationSource,
) (map[string]any, error) {
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
			if _, binOK := cm.BinaryData[src.ConfigMapRef.Key]; binOK {
				return nil, fmt.Errorf("ConfigMap %s/%s key %q is binary; expected netascode YAML text", ns, src.ConfigMapRef.Name, src.ConfigMapRef.Key)
			}
			return nil, fmt.Errorf("ConfigMap %s/%s does not contain key %q", ns, src.ConfigMapRef.Name, src.ConfigMapRef.Key)
		}
		raw = []byte(body)
	}
	return decodePlatformBody(raw, deviceName, platformEnvelope)
}

func decodePlatformBody(raw []byte, deviceName, platformEnvelope string) (map[string]any, error) {
	var root any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse netascode body: %w", err)
	}
	top, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("netascode body top-level must be a mapping, got %T", root)
	}
	if platformEnvelope != "" {
		if body, hasEnvelope := top[platformEnvelope]; hasEnvelope {
			return extractPlatformDeviceFromEnvelope(body, deviceName, platformEnvelope)
		}
	}
	return top, nil
}

func extractPlatformDeviceFromEnvelope(body any, deviceName, platformEnvelope string) (map[string]any, error) {
	envelope, ok := body.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("netascode envelope: .%s is %T, want mapping", platformEnvelope, body)
	}
	devs, ok := envelope["devices"].([]any)
	if !ok {
		return nil, fmt.Errorf("netascode envelope: .%s.devices missing or not a list", platformEnvelope)
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
	return nil, fmt.Errorf("netascode envelope: device %q not present under .%s.devices", deviceName, platformEnvelope)
}
