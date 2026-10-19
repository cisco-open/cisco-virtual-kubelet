// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestWirelessSSIDCredentialVersionsFollowPriority(t *testing.T) {
	one := ciskov1.NetworkControllerConfigurationCredentialVersion{Key: "multi-psk:4", Path: "/ssids/0/multi_psk_settings/0/passphrase_wo", Version: 7, SourceAlias: "priority-four", SecretName: "keys", SecretKey: "four"}
	two := ciskov1.NetworkControllerConfigurationCredentialVersion{Key: "multi-psk:0", Path: "/ssids/0/multi_psk_settings/1/passphrase_wo", Version: 2, SourceAlias: "priority-zero", SecretName: "keys", SecretKey: "zero"}
	prior := []ciskov1.NetworkControllerConfigurationCredentialVersion{one, two}
	// Empty-set readback equivalence must never authorize dropping ledger
	// history, either for a single priority or for the complete set.
	for _, removed := range [][]ciskov1.NetworkControllerConfigurationCredentialVersion{nil, {one}} {
		if _, err := wirelessSSIDCredentialRotation(prior, removed); err == nil {
			t.Fatal("credential retirement discarded version history")
		}
	}
	next := []ciskov1.NetworkControllerConfigurationCredentialVersion{two, one}
	next[0].Path = "/ssids/2/multi_psk_settings/0/passphrase_wo"
	next[1].Path = "/ssids/2/multi_psk_settings/1/passphrase_wo"
	if rotate, err := wirelessSSIDCredentialRotation(prior, next); err != nil || rotate {
		t.Fatal("reordering forced rotation", err)
	}
	if prior[0].Path != one.Path {
		t.Fatal("receipt path mutated")
	}
	next[1].Version = 6
	if _, err := wirelessSSIDCredentialRotation(prior, next); err == nil {
		t.Fatal("priority version downgrade accepted")
	}
	next[1].Version = 8
	if rotate, err := wirelessSSIDCredentialRotation(prior, next); err != nil || !rotate {
		t.Fatal("rotation not detected", err)
	}
	next[0].Key = next[1].Key
	if _, err := wirelessSSIDCredentialRotation(prior, next); err == nil {
		t.Fatal("duplicate semantic identity accepted")
	}
	for _, key := range []string{"", "multi-psk:05", "multi-psk:9", "other"} {
		bad := []ciskov1.NetworkControllerConfigurationCredentialVersion{one}
		bad[0].Key = key
		if _, err := wirelessSSIDCredentialRotation(bad, nil); err == nil {
			t.Fatal("invalid stored key accepted", key)
		}
	}
}

func TestWirelessSSIDCredentialKeyBoundToSource(t *testing.T) {
	row := ssidTestRow("CVK")
	row["passphrase_version"] = 3
	row["multi_psk_settings"] = []any{map[string]any{"priority": 4, "passphrase_wo_version": 7}}
	raw := ssidTestModel(row)
	desired, err := parseWirelessSSIDs(raw)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := ssidCredentialSnapshot(desired[0], map[string]string{"passphrase": "fixture-secret", "multiPSKSettings.0.passphrase": "other-fixture-secret"})
	_, versions, err := resolveVersionedConfigurationCredentials(snapshot, "owner", "wireless", desired[0].credentials)
	if err != nil {
		t.Fatal(err)
	}
	source := &configSourceSnapshot{raw: raw, secrets: snapshot}
	if err := validateConfigurationCredentialVersions(source, "wireless", versions); err != nil {
		t.Fatal(err)
	}
	duplicate := versions[0]
	duplicate.Path = "/ssids/0/multi_psk_settings/1/passphrase_wo"
	duplicateSecret := snapshot.values[0]
	duplicateSecret.ref.Path = duplicate.Path
	entries := row["multi_psk_settings"].([]any)
	row["multi_psk_settings"] = append(entries, map[string]any{"priority": 4, "passphrase_wo_version": 7})
	duplicateSource := &configSourceSnapshot{raw: ssidTestModel(row), secrets: &configurationSecretSnapshot{uid: "owner", values: append(append([]configurationSecretValue(nil), snapshot.values...), duplicateSecret)}}
	if err := validateConfigurationCredentialVersions(duplicateSource, "wireless", []ciskov1.NetworkControllerConfigurationCredentialVersion{versions[0], duplicate}); err == nil {
		t.Fatal("duplicate semantic key admitted")
	}
	row["multi_psk_settings"] = entries
	for _, key := range []string{"", "multi-psk:0", "passphrase"} {
		altered := append([]ciskov1.NetworkControllerConfigurationCredentialVersion(nil), versions...)
		altered[0].Key = key
		if err := validateConfigurationCredentialVersions(source, "wireless", altered); err == nil {
			t.Fatal("source/semantic key mismatch accepted")
		}
	}
	row["multi_psk_settings"].([]any)[0].(map[string]any)["priority"] = 0
	source.raw = ssidTestModel(row)
	if err := validateConfigurationCredentialVersions(source, "wireless", versions); err == nil {
		t.Fatal("model priority substitution accepted")
	}
}
