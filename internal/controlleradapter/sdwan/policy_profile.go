// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"encoding/json"
	"errors"
	"fmt"
	"sigs.k8s.io/yaml"
)

// compilePolicyObjectProfile preserves the singular profile object in the NaC model.
func compilePolicyObjectProfile(raw []byte) ([]Resource, error) {
	var input map[string]any
	if err := yaml.UnmarshalStrict(raw, &input); err != nil || input == nil {
		return nil, errors.New("invalid policy object profile")
	}
	name := "policy_objects"
	if value, exists := input["name"]; exists {
		var ok bool
		name, ok = value.(string)
		if !ok {
			return nil, errors.New("invalid policy object profile name")
		}
	}
	if name == "" || len(name) > 128 {
		return nil, errors.New("invalid policy object profile name")
	}
	description := ""
	if value := input["description"]; value != nil {
		var ok bool
		description, ok = value.(string)
		if !ok {
			return nil, errors.New("invalid policy object profile description")
		}
	}
	key := "feature_profiles/policy_object_profile/" + name
	collection := "/dataservice/v1/feature-profile/sdwan/policy-object"
	out := []Resource{{Key: key, Name: name, Section: "feature_profiles", Kind: "profile", Type: "policy-object", Collection: collection, Body: map[string]any{"name": name, "description": description}}}
	var definitions struct{ Parcels map[string]scalarDefinition }
	if json.Unmarshal(scalarDefinitions, &definitions) != nil {
		return nil, ErrResponse
	}
	byCollection := map[string]string{}
	for kind, definition := range definitions.Parcels {
		if definition.Collection != "" {
			byCollection[definition.Collection] = kind
		}
	}
	seenIdentity := map[string]bool{}
	for public, value := range input {
		if public == "name" || public == "description" {
			continue
		}
		kind, exists := byCollection[public]
		if !exists {
			return nil, fmt.Errorf("unsupported policy object profile collection %s", public)
		}
		objects, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("%s must be an array", public)
		}
		definition := definitions.Parcels[kind]
		seen := map[string]bool{}
		for _, object := range objects {
			fields, ok := object.(map[string]any)
			if !ok {
				return nil, errors.New("policy objects must be objects")
			}
			meta, body, err := scalarParcelData(kind, fields)
			if err != nil {
				return nil, err
			}
			if meta.Name == nil || *meta.Name == "" || len(*meta.Name) > 128 || seen[*meta.Name] {
				return nil, errors.New("policy objects require unique names")
			}
			identity := definition.Path + "/" + *meta.Name
			if seenIdentity[identity] {
				return nil, errors.New("policy object name collision on shared parcel endpoint")
			}
			seenIdentity[identity] = true
			seen[*meta.Name] = true
			if definition.IgnoreDescription {
				meta.Description = ""
			}
			out = append(out, Resource{Key: key + "/" + public + "/" + *meta.Name, Name: *meta.Name, Section: "feature_profiles", Kind: "parcel", Type: kind, Collection: collection, ParentName: name, ParcelPath: definition.Path, Body: map[string]any{"name": *meta.Name, "description": meta.Description, "data": body}})
		}
	}
	return out, nil
}
