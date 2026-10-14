// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import "errors"

type SecurityPolicies struct {
	FeaturePolicies []ClassicSecurityFeaturePolicy `json:"feature_policies,omitempty"`
	Definitions     struct {
		IntrusionPrevention []ClassicIntrusionPrevention `json:"intrusion_prevention,omitempty"`
	} `json:"definitions"`
}
type ClassicSecurityFeaturePolicy struct {
	Name                      string `json:"name"`
	Description               string `json:"description"`
	Mode                      string `json:"mode,omitempty"`
	UseCase                   string `json:"use_case"`
	IntrusionPreventionPolicy string `json:"intrusion_prevention_policy,omitempty"`
}
type ClassicIntrusionPrevention struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	InspectionMode string   `json:"inspection_mode,omitempty"`
	LogLevel       string   `json:"log_level,omitempty"`
	SignatureSet   string   `json:"signature_set,omitempty"`
	TargetVPNs     []string `json:"target_vpns,omitempty"`
}

func compileSecurityPolicies(input *SecurityPolicies) ([]Resource, error) {
	if input == nil {
		return nil, nil
	}
	out := []Resource{}
	seen := map[string]bool{}
	for _, p := range input.Definitions.IntrusionPrevention {
		if p.Name == "" || p.Description == "" || seen[p.Name] {
			return nil, errors.New("invalid or duplicate intrusion prevention definition")
		}
		seen[p.Name] = true
		if p.InspectionMode != "" && p.InspectionMode != "protection" && p.InspectionMode != "detection" {
			return nil, errors.New("invalid IPS inspection mode")
		}
		if p.SignatureSet != "" && p.SignatureSet != "balanced" && p.SignatureSet != "connectivity" && p.SignatureSet != "security" {
			return nil, errors.New("invalid IPS signature set")
		}
		validLogs := map[string]bool{"emergency": true, "alert": true, "critical": true, "error": true, "warning": true, "notice": true, "info": true, "debug": true}
		if p.LogLevel != "" && !validLogs[p.LogLevel] {
			return nil, errors.New("invalid IPS log level")
		}
		definition := map[string]any{"logging": []any{}}
		if p.InspectionMode != "" {
			definition["inspectionMode"] = p.InspectionMode
		}
		if p.LogLevel != "" {
			definition["logLevel"] = p.LogLevel
		}
		if p.SignatureSet != "" {
			definition["signatureSet"] = p.SignatureSet
		}
		if p.TargetVPNs != nil {
			if len(p.TargetVPNs) == 0 {
				return nil, errors.New("empty IPS target VPNs")
			}
			definition["targetVpns"] = p.TargetVPNs
		}
		out = append(out, Resource{Key: "security_policies/definitions/intrusion_prevention/" + p.Name, Name: p.Name, Section: "security_policies", Kind: "classic-definition", Type: "intrusion_prevention_policy_definition", Collection: "/dataservice/template/policy/definition/intrusionprevention/", Body: map[string]any{"type": "intrusionPrevention", "name": p.Name, "description": p.Description, "mode": "security", "definition": definition}})
	}
	for _, p := range input.FeaturePolicies {
		if p.Name == "" || p.Description == "" || p.IntrusionPreventionPolicy == "" {
			return nil, errors.New("invalid security feature policy")
		}
		mode := p.Mode
		if mode == "" {
			mode = "security"
		}
		if mode != "security" {
			return nil, errors.New("unsupported security policy mode")
		}
		useCase := p.UseCase
		if useCase == "" {
			useCase = "custom"
		}
		validUseCase := map[string]bool{"custom": true, "compliance": true, "guestAccess": true, "directCloudAccess": true, "directInternetAccess": true}
		if !validUseCase[useCase] {
			return nil, errors.New("invalid security policy use_case")
		}
		out = append(out, Resource{Key: "security_policies/feature_policies/" + p.Name, Name: p.Name, Section: "security_policies", Kind: "security-policy", Type: "security_policy", Collection: "/dataservice/template/policy/security/", ClassicDefinitionRefs: []ClassicDefinitionReference{{Type: "security_policies/definitions/intrusion_prevention", Name: p.IntrusionPreventionPolicy}}, Body: map[string]any{"policyType": "feature", "policyName": p.Name, "policyDescription": p.Description, "policyMode": mode, "policyUseCase": useCase, "policyDefinition": map[string]any{"assembly": []any{map[string]any{"definitionId": "", "type": "intrusionPrevention"}}, "settings": map[string]any{"logging": []any{}}}}})
	}
	return out, nil
}
