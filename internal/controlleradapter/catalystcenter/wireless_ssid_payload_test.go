// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func ssidCredentialSnapshot(want wirelessSSIDIntent, values map[string]string) *configurationSecretSnapshot {
	snapshot := &configurationSecretSnapshot{uid: "owner"}
	for i, leaf := range want.credentials {
		raw, _ := json.Marshal(map[string]any{"version": leaf.version, "value": values[leaf.field]})
		snapshot.values = append(snapshot.values, configurationSecretValue{ref: configapi.NetworkControllerSecretRef{Section: "wireless", Path: leaf.path, Source: fmt.Sprintf("ssid-%d", i)}, source: ciskov1.NetworkControllerIntentSecretSource{Name: "ssid-secrets", Key: fmt.Sprintf("key-%d", i)}, value: raw})
	}
	return snapshot
}

func ssidPersonalIntent(t *testing.T) wirelessSSIDIntent {
	t.Helper()
	row := ssidTestRow("CVK-Personal")
	row["auth_type"] = "WPA2_PERSONAL"
	row["auth_key_psk"] = true
	row["rsn_cipher_suite_ccmp128"] = true
	row["passphrase_version"] = 1
	row["multi_psk_settings"] = []any{map[string]any{"priority": 4, "passphrase_wo_version": 2}, map[string]any{"priority": 0, "passphrase_wo_version": 3}}
	rows, err := parseWirelessSSIDs(ssidTestModel(row))
	if err != nil {
		t.Fatal(err)
	}
	return rows[0]
}

func TestWirelessSSIDPayloadPreservesCredentialsWithoutMutatingMetadata(t *testing.T) {
	want := ssidPersonalIntent(t)
	actual := &wirelessSSIDMetadata{mainCredential: true, fields: map[string]any{"profileName": "retained-profile", "isCustomNasIdOptions": true, "multiPSKSettings": []map[string]any{{"priority": int64(0), "passphraseType": "ASCII"}, {"priority": int64(4), "passphraseType": "HEX"}}}}
	beforeWant, _ := json.Marshal(want.metadata)
	beforeActual, _ := json.Marshal(actual.fields)
	values := map[string]string{"passphrase": "main-fixture-secret", "multiPSKSettings.0.passphrase": strings.Repeat("ab", 32), "multiPSKSettings.1.passphrase": "secondary-fixture-secret"}
	raw, versions, err := want.payload(ssidCredentialSnapshot(want, values), "owner", actual)
	if err != nil || len(versions) != 3 {
		t.Fatal("payload/receipt binding", err)
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		t.Fatal("invalid payload")
	}
	if body["passphrase"] != values["passphrase"] || body["profileName"] != "retained-profile" || body["isCustomNasIdOptions"] != true {
		t.Fatal("retained fields or projected main credential missing")
	}
	psks := body["multiPSKSettings"].([]any)
	for i, priority := range []float64{4, 0} {
		p := psks[i].(map[string]any)
		if p["priority"] != priority || p["passphrase"] != values[fmt.Sprintf("multiPSKSettings.%d.passphrase", i)] {
			t.Fatal("credential attached to wrong priority")
		}
	}
	if psks[0].(map[string]any)["passphraseType"] != "HEX" {
		t.Fatal("optional metadata matched by position rather than priority")
	}
	afterWant, _ := json.Marshal(want.metadata)
	afterActual, _ := json.Marshal(actual.fields)
	if string(beforeWant) != string(afterWant) || string(beforeActual) != string(afterActual) {
		t.Fatal("payload leaked credential into public metadata")
	}
}

func TestWirelessSSIDPayloadRefusesIncompleteSecrets(t *testing.T) {
	for _, test := range []string{"missing projection", "stale projection", "missing main", "retained multi", "unknown metadata", "sensor", "wrong owner", "bad hex", "short ASCII"} {
		t.Run(test, func(t *testing.T) {
			want := ssidPersonalIntent(t)
			values := map[string]string{"passphrase": "main-fixture-secret", "multiPSKSettings.0.passphrase": "first-fixture-secret", "multiPSKSettings.1.passphrase": "other-fixture-secret"}
			actual := &wirelessSSIDMetadata{mainCredential: true, fields: map[string]any{}}
			owner := "owner"
			switch test {
			case "missing main":
				want.credentials = want.credentials[:2] // sorted nested references precede the main reference
			case "retained multi":
				delete(want.metadata, "multiPSKSettings")
				want.credentials = want.credentials[2:]
				actual.fields["multiPSKSettings"] = []map[string]any{{"priority": int64(1)}}
			case "unknown metadata":
				actual.unmapped = true
			case "sensor":
				actual.sensor = true
			case "wrong owner":
				owner = "different"
			case "bad hex":
				want.metadata["isHex"] = true
			case "short ASCII":
				values["passphrase"] = "short"
			}
			snapshot := ssidCredentialSnapshot(want, values)
			if test == "missing projection" {
				snapshot.values = snapshot.values[:len(snapshot.values)-1]
			}
			if test == "stale projection" {
				snapshot.values[0].value = []byte(`{"version":1,"value":"stale-fixture-secret"}`)
			}
			if _, _, err := want.payload(snapshot, owner, actual); err == nil {
				t.Fatal("incomplete/unsafe payload accepted")
			}
		})
	}
}

func TestWirelessSSIDPassphraseFormat(t *testing.T) {
	for _, v := range []string{"12345678", strings.Repeat("x", 63)} {
		if !validWirelessSSIDPassphrase(v, false) {
			t.Fatal("valid ASCII rejected")
		}
	}
	for _, v := range []string{"1234567", strings.Repeat("x", 64), "newline\n", strings.Repeat("é", 8)} {
		if validWirelessSSIDPassphrase(v, false) {
			t.Fatal("invalid ASCII accepted")
		}
	}
	if !validWirelessSSIDPassphrase(strings.Repeat("aB", 32), true) || validWirelessSSIDPassphrase(strings.Repeat("xy", 32), true) {
		t.Fatal("hex validation")
	}
}

func TestWirelessSSIDAuthenticationConstraints(t *testing.T) {
	for _, test := range []string{"cipher", "akm", "pmf", "guest external", "enterprise acl"} {
		t.Run(test, func(t *testing.T) {
			p := map[string]any{"authType": "WPA3_PERSONAL", "wlanType": "Enterprise", "protectedManagementFrame": "REQUIRED", "rsnCipherSuiteCcmp128": true, "isAuthKeySae": true}
			if err := validateWirelessSSIDAuthentication(p); err != nil {
				t.Fatal("valid base rejected")
			}
			switch test {
			case "cipher":
				delete(p, "rsnCipherSuiteCcmp128")
			case "akm":
				delete(p, "isAuthKeySae")
			case "pmf":
				p["protectedManagementFrame"] = "OPTIONAL"
			case "guest external":
				p["wlanType"] = "Guest"
				p["l3AuthType"] = "web_auth"
				p["authServer"] = "auth_external"
			case "enterprise acl":
				p["aclName"] = "ACL"
			}
			if err := validateWirelessSSIDAuthentication(p); err == nil {
				t.Fatal("incompatible authentication accepted")
			}
		})
	}
}
