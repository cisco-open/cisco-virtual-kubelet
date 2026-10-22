// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

//go:embed definitions/embedded-security-policy-schema.json
var embeddedSecurityPolicySchema []byte

func compileEmbeddedSecurityPolicy(p FeatureProfile, family, key string) ([]Resource, error) {
	if len(p.NGFWPolicies) == 0 {
		return nil, nil
	}
	if family != "embedded-security" {
		return nil, errors.New("embedded security policy requires NGFW profile")
	}
	assembly := []any{}
	refs := []ParcelReference{}
	for i, policy := range p.NGFWPolicies {
		name, ok := policy["name"].(string)
		if !ok || name == "" {
			return nil, errors.New("embedded security policy requires named NGFW parcel")
		}
		source, ok := policy["source_zone"].(string)
		if !ok || source == "" {
			return nil, errors.New("embedded security policy requires source_zone")
		}
		destinations, err := meshStrings(policy["destination_zones"], false)
		if err != nil || len(destinations) == 0 {
			return nil, errors.New("embedded security policy requires destination_zones")
		}
		zone := func(value string) (map[string]any, error) {
			if value == "no_zone" {
				value = "default"
			}
			if value != "self" && value != "default" && value != "untrusted" {
				return nil, errors.New("security zone references require a declared policy-object profile")
			}
			return map[string]any{"optionType": "global", "value": value}, nil
		}
		src, err := zone(source)
		if err != nil {
			return nil, err
		}
		entries := []any{}
		for _, destination := range destinations {
			dst, err := zone(destination)
			if err != nil {
				return nil, err
			}
			entries = append(entries, map[string]any{"srcZone": src, "dstZone": dst})
		}
		item := map[string]any{"ngfirewall": map[string]any{"refId": map[string]any{"optionType": "global", "value": parcelPlaceholder(name)}, "entries": entries}}
		assembly = append(assembly, item)
		refs = append(refs, ParcelReference{Kind: "embedded_security_ngfw", Name: name, ParcelPath: "unified/ngfirewall", BodyPath: []string{"data", "assembly", strconv.Itoa(i), "ngfirewall", "refId", "value"}})
	}
	data := map[string]any{"assembly": assembly, "settings": map[string]any{"securityLogging": map[string]any{"optionType": "network-settings", "value": true}}}
	var schema map[string]any
	var normalized any
	encoded, _ := json.Marshal(data)
	if json.Unmarshal(embeddedSecurityPolicySchema, &schema) != nil || json.Unmarshal(encoded, &normalized) != nil {
		return nil, ErrResponse
	}
	if !validScalarValue(normalized, schema) {
		for field, value := range normalized.(map[string]any) {
			if !validScalarValue(value, schema["properties"].(map[string]any)[field].(map[string]any)) {
				return nil, fmt.Errorf("embedded security field %s violates provider schema", field)
			}
		}
		return nil, errors.New("embedded security policy violates provider schema")
	}
	description := p.Description
	if description == "" {
		description = p.Name
	}
	return []Resource{{Key: key + "/policy", Name: p.Name, Section: "feature_profiles", Kind: "policy-parcel", Type: "embedded_security_policy", Collection: "/dataservice/v1/feature-profile/sdwan/embedded-security", ParentName: p.Name, ParcelPath: "policy", ParcelRefs: refs, Body: map[string]any{"name": p.Name, "description": description, "data": data}}}, nil
}
