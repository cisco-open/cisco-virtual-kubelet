// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"errors"
	"fmt"
)

// prepareSIGTemplate evaluates the module's provider and tunnel selectors
// before the provider-derived classic template mapping is applied.
func prepareSIGTemplate(input map[string]any, defaults map[string]any) (map[string]any, error) {
	provider, _ := input["sig_provider"].(string)
	if input["sig_provider"] != nil && provider != "umbrella" && provider != "zscaler" && provider != "other" {
		return nil, errors.New("invalid SIG provider")
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		if key != "sig_provider" {
			out[key] = value
		}
	}
	raw, exists := input["interfaces"]
	if !exists {
		return out, nil
	}
	interfaces, ok := raw.([]any)
	if !ok {
		return nil, errors.New("SIG interfaces must be an array")
	}
	selected := make([]any, 0, len(interfaces))
	ipsecFields := []string{"ike_ciphersuite", "ike_ciphersuite_variable", "ike_group", "ike_group_variable", "ike_pre_shared_key", "ike_pre_shared_key_variable", "ike_pre_shared_key_local_id", "ike_pre_shared_key_local_id_variable", "ike_pre_shared_key_remote_id", "ike_pre_shared_key_remote_id_variable", "ike_rekey_interval", "ike_rekey_interval_variable", "ipsec_ciphersuite", "ipsec_ciphersuite_variable", "ipsec_perfect_forward_secrecy", "ipsec_perfect_forward_secrecy_variable", "ipsec_rekey_interval", "ipsec_rekey_interval_variable", "ipsec_replay_window", "ipsec_replay_window_variable"}
	for _, item := range interfaces {
		row, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("SIG interface must be an object")
		}
		for _, synthetic := range []string{"auto_tunnel_mode", "ike_pre_shared_key_dynamic", "ike_version", "sig_provider"} {
			if _, exists := row[synthetic]; exists {
				return nil, fmt.Errorf("%s is derived from SIG selectors", synthetic)
			}
		}
		tunnelType, _ := row["tunnel_type"].(string)
		if row["tunnel_type"] != nil && tunnelType != "ipsec" && tunnelType != "gre" {
			return nil, errors.New("invalid SIG tunnel type")
		}
		mapped := make(map[string]any, len(row)+4)
		for key, value := range row {
			if key != "tunnel_type" {
				mapped[key] = value
			}
		}
		mapped["auto_tunnel_mode"] = provider != "other"
		if provider != "" {
			mapped["sig_provider"] = "secure-internet-gateway-" + provider
		}
		if tunnelType == "ipsec" {
			mapped["ike_version"] = float64(2)
			if provider != "other" {
				mapped["ike_pre_shared_key_dynamic"] = true
			}
			for key, value := range defaults {
				if mapped[key] == nil && mapped[key+"_variable"] == nil {
					mapped[key] = value
				}
			}
		} else {
			for _, key := range ipsecFields {
				delete(mapped, key)
			}
		}
		if tunnelType != "gre" {
			delete(mapped, "tunnel_public_source_ip")
			delete(mapped, "tunnel_public_source_ip_variable")
		}
		if provider != "other" {
			mapped["tunnel_destination"] = "dynamic"
			delete(mapped, "tunnel_destination_variable")
		}
		selected = append(selected, mapped)
	}
	out["interfaces"] = selected
	return out, nil
}
