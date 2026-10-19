// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"strconv"
	"strings"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func (w policyServerIntent) credentialIntents() []configurationCredentialIntent {
	out := []configurationCredentialIntent{{path: w.path, field: "sharedSecret", version: w.version}}
	return append(out, w.extra...)
}

func parsePolicyServerKeyWrap(entry map[string]any, base string) ([]configurationCredentialIntent, error) {
	protocols, _ := entry["protocols"].(map[string]any)
	radius, _ := protocols["radius"].(map[string]any)
	value, present := radius["enable_key_wrap"]
	if !present {
		return nil, nil
	}
	wrap, ok := value.(map[string]any)
	if !ok {
		return nil, errConfigurationSecret
	}
	for key, value := range wrap {
		switch key {
		case "encryption_key", "message_key":
			if value != nil {
				return nil, errConfigurationSecret
			}
		case "encryption_key_version", "message_key_version":
		default:
			return nil, errConfigurationSecret
		}
	}
	out := []configurationCredentialIntent{}
	for _, key := range []string{"encryption_key", "message_key"} {
		raw, err := json.Marshal(wrap[key+"_version"])
		var version int64
		if err != nil || json.Unmarshal(raw, &version) != nil || version < 1 {
			return nil, errConfigurationSecret
		}
		field := "encryptionKey"
		if key == "message_key" {
			field = "messageKey"
		}
		out = append(out, configurationCredentialIntent{path: base + "/protocols/radius/enable_key_wrap/" + key, field: field, version: version})
	}
	delete(radius, "enable_key_wrap")
	return out, nil
}

func (e *policyServerExecutor) credentials(want policyServerIntent) (map[string]string, []ciskov1.NetworkControllerConfigurationCredentialVersion, error) {
	return resolveVersionedConfigurationCredentials(e.secrets, e.owner, "system_settings", want.credentialIntents())
}

// Array position is not resource identity. Compare counters by the credential
// leaf within the UUID-bound server receipt, while rejecting unknown paths.
func policyCredentialKind(path string) string {
	p, err := configurationSecretPointer(path)
	if err != nil || len(p) < 3 || p[0] != "authentication_and_policy_servers" {
		return ""
	}
	var leaf []string
	switch p[1] {
	case "aaa":
		if len(p) < 4 {
			return ""
		}
		i, err := strconv.Atoi(p[2])
		if err != nil || i < 0 || strconv.Itoa(i) != p[2] {
			return ""
		}
		leaf = p[3:]
	case "ise":
		leaf = p[2:]
	default:
		return ""
	}
	if len(leaf) == 1 {
		if leaf[0] == "shared_secret" {
			return "sharedSecret"
		}
		if p[1] == "ise" && leaf[0] == "password" {
			return "isePassword"
		}
	}
	if len(leaf) == 4 && leaf[0] == "protocols" && leaf[1] == "radius" && leaf[2] == "enable_key_wrap" {
		switch leaf[3] {
		case "encryption_key":
			return "encryptionKey"
		case "message_key":
			return "messageKey"
		}
	}
	return ""
}

func policyCredentialRotation(prior, desired []ciskov1.NetworkControllerConfigurationCredentialVersion) (bool, error) {
	return configurationCredentialRotation(prior, desired, func(path string) string {
		kind := policyCredentialKind(path)
		if kind == "" {
			return ""
		}
		family := "aaa"
		if strings.HasPrefix(path, "/authentication_and_policy_servers/ise/") {
			family = "ise"
		}
		return family + "/" + kind
	})
}
