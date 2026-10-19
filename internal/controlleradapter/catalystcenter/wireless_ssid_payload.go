// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func validWirelessSSIDPassphrase(value string, hexValue bool) bool {
	if hexValue {
		if len(value) != 64 {
			return false
		}
		_, err := hex.DecodeString(value)
		return err == nil
	}
	if len(value) < 8 || len(value) > 63 {
		return false
	}
	for _, c := range []byte(value) {
		if c < 32 || c > 126 {
			return false
		}
	}
	return true
}

func cloneWirelessSSIDFields(fields map[string]any) map[string]any {
	out := cloneWirelessAPFields(fields)
	if entries, ok := fields["multiPSKSettings"].([]map[string]any); ok {
		copyEntries := make([]map[string]any, len(entries))
		for i, entry := range entries {
			copyEntries[i] = cloneWirelessAPFields(entry)
		}
		out["multiPSKSettings"] = copyEntries
	}
	return out
}

// SSID PUT replaces the object. Every retained secret must come from a
// versioned projection; API-returned credentials are never used as defaults.
// Identity, ownership and durable receipt checks belong to the executor.
func (want wirelessSSIDIntent) payload(snapshot *configurationSecretSnapshot, owner string, actual *wirelessSSIDMetadata) ([]byte, []ciskov1.NetworkControllerConfigurationCredentialVersion, error) {
	base := map[string]any{}
	if actual != nil {
		if actual.sensor || actual.unmapped {
			return nil, nil, errWirelessSSIDModel
		}
		base = actual.fields
	}
	payload := cloneWirelessSSIDFields(mergeWirelessAPFields(base, want.metadata))
	// Preserve optional per-key metadata by priority, not array position.
	// Credential source paths continue to follow the manifest's original order.
	if desired, owned := want.metadata["multiPSKSettings"].([]map[string]any); owned {
		prior, _ := base["multiPSKSettings"].([]map[string]any)
		merged := make([]map[string]any, len(desired))
		for i, entry := range desired {
			old := map[string]any{}
			for _, candidate := range prior {
				if candidate["priority"] == entry["priority"] {
					old = candidate
					break
				}
			}
			merged[i] = mergeWirelessAPFields(old, entry)
		}
		payload["multiPSKSettings"] = merged
	}
	// An observation-only empty string is not an explicit request. Omit it
	// unless the manifest owns the field (empty scalar intent is rejected).
	for key, value := range payload {
		if value == "" {
			if _, owned := want.metadata[key]; !owned {
				delete(payload, key)
			}
		}
	}
	// Empty version lists are omitted by the Kubernetes JSON journal. Keep
	// the planner identity canonical so its exact persisted comparison agrees.
	var versions []ciskov1.NetworkControllerConfigurationCredentialVersion
	values := map[string]string{}
	if len(want.credentials) > 0 {
		var err error
		values, versions, err = resolveVersionedConfigurationCredentials(snapshot, owner, "wireless", want.credentials)
		if err != nil {
			return nil, nil, err
		}
	}
	main, hasMain := values["passphrase"]
	if actual != nil && actual.mainCredential && !hasMain {
		return nil, nil, errConfigurationSecret
	}
	if hasMain {
		hexValue, _ := payload["isHex"].(bool)
		if !validWirelessSSIDPassphrase(main, hexValue) {
			return nil, nil, errConfigurationSecret
		}
		payload["passphrase"] = main
		delete(values, "passphrase")
	}
	entries, _ := payload["multiPSKSettings"].([]map[string]any)
	for i, entry := range entries {
		field := "multiPSKSettings." + strconv.Itoa(i) + ".passphrase"
		secret, ok := values[field]
		if !ok || !validWirelessSSIDPassphrase(secret, entry["passphraseType"] == "HEX") {
			return nil, nil, errConfigurationSecret
		}
		entry["passphrase"] = secret
		delete(values, field)
	}
	if len(values) > 0 {
		return nil, nil, errConfigurationSecret
	}
	auth, _ := payload["authType"].(string)
	if strings.Contains(auth, "PERSONAL") {
		if !hasMain && len(entries) == 0 {
			return nil, nil, errConfigurationSecret
		}
	} else if hasMain || len(entries) > 0 {
		return nil, nil, errConfigurationSecret
	}
	if err := validateWirelessSSIDAuthentication(payload); err != nil {
		return nil, nil, err
	}
	raw, err := json.Marshal(payload)
	return raw, versions, err
}

func validateWirelessSSIDAuthentication(fields map[string]any) error {
	auth, _ := fields["authType"].(string)
	if auth == "WPA3_PERSONAL" || auth == "WPA3_ENTERPRISE" || auth == "OPEN-SECURED" {
		if fields["protectedManagementFrame"] != "REQUIRED" {
			return errWirelessSSIDModel
		}
	}
	if auth == "WPA2_WPA3_PERSONAL" || auth == "WPA2_WPA3_ENTERPRISE" {
		if fields["protectedManagementFrame"] != "OPTIONAL" && fields["protectedManagementFrame"] != "REQUIRED" {
			return errWirelessSSIDModel
		}
	}
	if auth != "OPEN" {
		cipher := false
		for _, key := range []string{"rsnCipherSuiteCcmp128", "rsnCipherSuiteCcmp256", "rsnCipherSuiteGcmp128", "rsnCipherSuiteGcmp256"} {
			if fields[key] == true {
				cipher = true
			}
		}
		keys := []string{}
		switch auth {
		case "WPA2_ENTERPRISE":
			keys = []string{"isAuthKey8021x", "isAuthKey8021xPlusFT", "isAuthKey8021x_SHA256"}
		case "WPA3_ENTERPRISE":
			keys = []string{"isAuthKey8021x_SHA256", "isAuthKeySuiteB1x", "isAuthKeySuiteB1921x"}
		case "WPA2_WPA3_ENTERPRISE":
			keys = []string{"isAuthKey8021x", "isAuthKey8021xPlusFT", "isAuthKey8021x_SHA256", "isAuthKeySuiteB1x", "isAuthKeySuiteB1921x"}
		case "WPA2_PERSONAL":
			keys = []string{"isAuthKeyPSK", "isAuthKeyPSKPlusFT", "isAuthKeyEasyPSK", "isAuthKeyPSKSHA256"}
		case "WPA3_PERSONAL":
			keys = []string{"isAuthKeySae", "isAuthKeySaePlusFT", "isAuthKeySaeExt", "isAuthKeySaeExtPlusFT"}
		case "WPA2_WPA3_PERSONAL":
			keys = []string{"isAuthKeyPSK", "isAuthKeyPSKPlusFT", "isAuthKeyEasyPSK", "isAuthKeyPSKSHA256", "isAuthKeySae", "isAuthKeySaePlusFT", "isAuthKeySaeExt", "isAuthKeySaeExtPlusFT"}
		case "OPEN-SECURED":
			keys = []string{"isAuthKeyOWE"}
		default:
			return errWirelessSSIDModel
		}
		akm := false
		for _, key := range keys {
			if fields[key] == true {
				akm = true
			}
		}
		if !cipher || !akm {
			return errWirelessSSIDModel
		}
	}
	if fields["wlanType"] == "Guest" {
		if fields["l3AuthType"] != "open" && fields["l3AuthType"] != "web_auth" {
			return errWirelessSSIDModel
		}
		if fields["l3AuthType"] == "web_auth" {
			server, _ := fields["authServer"].(string)
			if server != "auth_ise" && server != "auth_external" && server != "auth_internal" {
				return errWirelessSSIDModel
			}
			if server == "auth_external" {
				if value, _ := fields["externalAuthIpAddress"].(string); value == "" {
					return errWirelessSSIDModel
				}
			}
		}
	}
	if fields["wlanType"] == "Enterprise" {
		if acl, _ := fields["aclName"].(string); acl != "" && fields["isPosturingEnabled"] != true {
			return errWirelessSSIDModel
		}
	}
	return nil
}
