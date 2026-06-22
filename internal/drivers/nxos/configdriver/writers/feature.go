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
	"fmt"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
)

type featureWriter struct{}
type featureSetWriter struct{}

func init() {
	register(featureWriter{})
	register(featureSetWriter{})
}

func (featureWriter) Family() string { return nxosschema.FamilyFeature }

func (featureWriter) YANGPaths() []string { return []string{nxosschema.PathFeature} }

func (featureWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	raw, err := c.Fetch(ctx, nxosschema.PathFeature)
	if err != nil {
		return nil, err
	}
	return decodeMap(raw, "feature")
}

func (featureWriter) Diff(desired, observed any) ([]transport.Op, error) {
	want, err := coerceMap(desired, "feature.desired")
	if err != nil || want == nil {
		return nil, err
	}
	got, err := coerceMap(observed, "feature.observed")
	if err != nil {
		return nil, err
	}
	if got == nil {
		got = map[string]any{}
	}
	if err := rejectUnsupportedKeys(want, "feature", nxosschema.FeatureFields()...); err != nil {
		return nil, err
	}

	var children []map[string]any
	for _, mapping := range nxosschema.FeatureDMEMappings() {
		raw, ok := want[mapping.Field]
		if !ok {
			continue
		}
		enabled, state, err := desiredAdminState(raw, "feature."+mapping.Field, "enabled", "disabled")
		if err != nil {
			return nil, err
		}
		if err := rejectProtectedFeatureDisable(mapping.Field, enabled); err != nil {
			return nil, err
		}
		if adminStateEqual(got[mapping.Field], state) {
			continue
		}
		children = append(children, dmeObject(mapping.Class, map[string]string{"adminSt": state}))
	}
	if len(children) == 0 {
		return nil, nil
	}
	op, err := dmeMergeOp(nxosschema.DNSystem, dmeObject("topSystem", nil,
		dmeObject("fmEntity", nil, children...),
	))
	if err != nil {
		return nil, err
	}
	return []transport.Op{op}, nil
}

func (featureWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}

func (featureSetWriter) Family() string { return nxosschema.FamilyFeatureSet }

func (featureSetWriter) YANGPaths() []string { return []string{nxosschema.PathFeatureSet} }

func (featureSetWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	raw, err := c.Fetch(ctx, nxosschema.PathFeatureSet)
	if err != nil {
		return nil, err
	}
	return decodeMap(raw, "feature_set")
}

func (featureSetWriter) Diff(desired, observed any) ([]transport.Op, error) {
	want, err := coerceMap(desired, "feature_set.desired")
	if err != nil || want == nil {
		return nil, err
	}
	got, err := coerceMap(observed, "feature_set.observed")
	if err != nil {
		return nil, err
	}
	if got == nil {
		got = map[string]any{}
	}
	if err := rejectUnsupportedKeys(want, "feature_set", nxosschema.FeatureSetFields()...); err != nil {
		return nil, err
	}

	var children []map[string]any
	for _, field := range nxosschema.FeatureSetFields() {
		raw, ok := want[field]
		if !ok {
			continue
		}
		_, state, err := desiredAdminState(raw, "feature_set."+field, "none", "enabled", "disabled", "installed", "uninstalled")
		if err != nil {
			return nil, err
		}
		if adminStateEqual(got[field], state) {
			continue
		}
		children = append(children, dmeObject("fsetFeatureSet", map[string]string{
			"name":    field,
			"adminSt": state,
		}))
	}
	if len(children) == 0 {
		return nil, nil
	}
	op, err := dmeMergeOp(nxosschema.DNSystem, dmeObject("topSystem", nil, children...))
	if err != nil {
		return nil, err
	}
	return []transport.Op{op}, nil
}

func (featureSetWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}

func desiredAdminState(v any, origin string, allowedStrings ...string) (*bool, string, error) {
	if b, ok := boolLeaf(v); ok {
		if b {
			return &b, "enabled", nil
		}
		return &b, "disabled", nil
	}
	state := strings.ToLower(strings.TrimSpace(stringLeaf(v)))
	if state == "" {
		return nil, "", fmt.Errorf("%s admin state must not be empty", origin)
	}
	for _, allowed := range allowedStrings {
		if state == allowed {
			switch state {
			case "enabled":
				b := true
				return &b, state, nil
			case "disabled":
				b := false
				return &b, state, nil
			}
			return nil, state, nil
		}
	}
	return nil, "", fmt.Errorf("%s admin state %q is not supported", origin, state)
}

func adminStateEqual(observed any, desired string) bool {
	if b, ok := boolLeaf(observed); ok {
		return (b && desired == "enabled") || (!b && desired == "disabled")
	}
	return strings.EqualFold(strings.TrimSpace(stringLeaf(observed)), desired)
}

func rejectProtectedFeatureDisable(field string, enabled *bool) error {
	if enabled == nil || *enabled {
		return nil
	}
	if reason, ok := protectedManagementFeature(field); ok {
		return fmt.Errorf("feature.%s cannot be disabled through NXOSConfig because it may remove %s; use an out-of-band maintenance workflow", field, reason)
	}
	return nil
}

func protectedManagementFeature(field string) (string, bool) {
	switch field {
	case "nxapi":
		return "the active NX-API REST transport", true
	case "ssh":
		return "interactive management access", true
	case "scp_server", "sftp_server":
		return "file-transfer access used by operational workflows", true
	case "tacacs":
		return "centralized authentication access", true
	default:
		return "", false
	}
}
