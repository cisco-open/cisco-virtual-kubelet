// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"encoding/json"
	"errors"
	"reflect"
	"sigs.k8s.io/yaml"
	"strings"
)

type FeatureProfile struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Banner      *ProfileBanner `json:"banner,omitempty"`
}
type ProfileBanner struct {
	Name          *string `json:"name,omitempty"`
	Description   string  `json:"description,omitempty"`
	Login         *string `json:"login,omitempty"`
	LoginVariable *string `json:"login_variable,omitempty"`
	MOTD          *string `json:"motd,omitempty"`
	MOTDVariable  *string `json:"motd_variable,omitempty"`
}

var profileCollections = map[string]string{"system_profiles": "system", "transport_profiles": "transport", "service_profiles": "service", "cli_profiles": "cli", "other_profiles": "other", "application_priority_profiles": "application-priority", "ngfw_security_profiles": "embedded-security", "sse_profiles": "sse", "topology_profiles": "topology"}

func compileProfiles(raw map[string]json.RawMessage) ([]Resource, error) {
	out := []Resource{}
	for section, data := range raw {
		collection, ok := profileCollections[section]
		if !ok {
			return nil, errors.New("unsupported feature profile collection")
		}
		var profiles []FeatureProfile
		if yaml.UnmarshalStrict(data, &profiles) != nil {
			return nil, errors.New("invalid or unsupported feature profile fields")
		}
		seen := map[string]bool{}
		for _, p := range profiles {
			if p.Name == "" || len(p.Name) > 128 || seen[p.Name] {
				return nil, errors.New("invalid or duplicate feature profile name")
			}
			seen[p.Name] = true
			key := "feature_profiles/" + section + "/" + p.Name
			out = append(out, Resource{Key: key, Name: p.Name, Section: "feature_profiles", Kind: "profile", Type: collection, Collection: "/dataservice/v1/feature-profile/sdwan/" + collection, Body: map[string]any{"name": p.Name, "description": p.Description}})
			if b := p.Banner; b != nil {
				if collection != "system" {
					return nil, errors.New("banner is only valid in a system profile")
				}
				name := "banner"
				if b.Name != nil {
					name = *b.Name
				}
				if name == "" {
					return nil, errors.New("empty banner name")
				}
				out = append(out, Resource{Key: key + "/banner", Name: name, Section: "feature_profiles", Kind: "parcel", Type: "system_banner", Collection: "/dataservice/v1/feature-profile/sdwan/system", ParentName: p.Name, Body: map[string]any{"name": name, "description": b.Description, "data": map[string]any{"login": profileBannerValue(b.Login, b.LoginVariable), "motd": profileBannerValue(b.MOTD, b.MOTDVariable)}}})
			}
		}
	}
	return out, nil
}
func profileBannerValue(v, variable *string) map[string]any {
	if variable != nil {
		return map[string]any{"optionType": "variable", "value": "{{" + *variable + "}}"}
	}
	if v != nil {
		return map[string]any{"optionType": "global", "value": strings.ReplaceAll(*v, "\n", "\\n")}
	}
	return map[string]any{"optionType": "default", "value": ""}
}
func equalModern(r Resource, o map[string]any) (bool, error) {
	if r.Kind == "group" {
		return equalGroup(r, o)
	}
	if r.Kind == "profile" {
		name, ok := o["profileName"].(string)
		if !ok {
			return false, ErrResponse
		}
		description, _ := o["description"].(string)
		return name == r.Name && description == r.Body["description"], nil
	}
	payload, ok := o["payload"].(map[string]any)
	if !ok {
		return false, ErrResponse
	}
	want, e := json.Marshal(r.Body)
	if e != nil {
		return false, ErrResponse
	}
	var w map[string]any
	json.Unmarshal(want, &w)
	normalized := map[string]any{"name": payload["name"], "description": payload["description"], "data": payload["data"]}
	if normalized["description"] == nil {
		normalized["description"] = ""
	}
	return reflect.DeepEqual(w, normalized), nil
}
