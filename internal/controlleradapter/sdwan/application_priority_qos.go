// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
)

//go:embed definitions/application-priority-qos-schema.json
var applicationPriorityQoSSchema []byte

func compileQoSPolicies(p FeatureProfile, family, key string) ([]Resource, error) {
	if len(p.QoSPolicies) > 0 && family != "application-priority" {
		return nil, errors.New("QoS policies require application-priority profile")
	}
	out := make([]Resource, 0, len(p.QoSPolicies))
	for _, input := range p.QoSPolicies {
		name, ok := input["name"].(string)
		if !ok || name == "" || len(name) > 128 {
			return nil, errors.New("QoS policy requires name")
		}
		for field := range input {
			if field != "name" && field != "target_interfaces" && field != "target_interfaces_variable" {
				return nil, fmt.Errorf("unsupported QoS policy field %s", field)
			}
		}
		target := map[string]any{}
		if raw := input["target_interfaces_variable"]; raw != nil {
			variable, ok := raw.(string)
			if !ok || variable == "" || input["target_interfaces"] != nil {
				return nil, errors.New("invalid QoS target_interfaces_variable")
			}
			target["interfaces"] = map[string]any{"optionType": "variable", "value": "{{" + variable + "}}"}
		} else if raw := input["target_interfaces"]; raw != nil {
			interfaces, err := meshStrings(raw, false)
			if err != nil || len(interfaces) == 0 {
				return nil, errors.New("invalid QoS target_interfaces")
			}
			target["interfaces"] = map[string]any{"optionType": "global", "value": interfaces}
		} else {
			return nil, errors.New("QoS policy requires target interfaces")
		}
		data := map[string]any{"target": target, "qosMap": map[string]any{"qosSchedulers": []any{}}}
		var schema map[string]any
		var normalized any
		encoded, _ := json.Marshal(data)
		if json.Unmarshal(applicationPriorityQoSSchema, &schema) != nil || json.Unmarshal(encoded, &normalized) != nil || !validScalarValue(normalized, schema) {
			return nil, errors.New("QoS policy violates provider schema")
		}
		out = append(out, Resource{Key: key + "/qos_policies/" + name, Name: name, Section: "feature_profiles", Kind: "policy-parcel", Type: "application_priority_qos", Collection: "/dataservice/v1/feature-profile/sdwan/application-priority", ParentName: p.Name, ParcelPath: "qos-policy", Body: map[string]any{"name": name, "description": "", "data": data}})
	}
	return out, nil
}
