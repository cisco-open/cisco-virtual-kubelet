// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestWirelessSSIDHistoryBindingAndPruning(t *testing.T) {
	versions := []ciskov1.NetworkControllerConfigurationCredentialVersion{{Key: "passphrase", Path: "/ssids/0/passphrase", Version: 1, SourceAlias: "main", SecretName: "keys", SecretKey: "main"}}
	history := []ciskov1.NetworkControllerConfigurationSSIDCredentialHistory{{Priority: 0, Version: 1}, {Priority: 4, Version: 3}}
	resource := ssidHistoryPrefix(history) + ":global:CVK"
	if err := validateSSIDCredentialHistory("wireless", resource, versions, history); err != nil {
		t.Fatal(err)
	}
	for _, modified := range [][]ciskov1.NetworkControllerConfigurationSSIDCredentialHistory{nil, history[:1], {{Priority: 4, Version: 3}, {Priority: 0, Version: 1}}, {{Priority: 0, Version: 1}, {Priority: 4, Version: 2}}} {
		if err := validateSSIDCredentialHistory("wireless", resource, versions, modified); err == nil {
			t.Fatal("pruned/changed history accepted")
		}
	}
	for _, section := range []string{"inventory", "network_settings", "system_settings"} {
		if err := validateSSIDCredentialHistory(section, resource, versions, history); err == nil {
			t.Fatal("history escaped SSID executor")
		}
	}
	for _, bad := range [][]ciskov1.NetworkControllerConfigurationSSIDCredentialHistory{{{Priority: 0, Version: 0}}, {{Priority: -1, Version: 1}}, {{Priority: 5, Version: 1}}, {{Priority: 0, Version: 1}, {Priority: 0, Version: 2}}} {
		if err := validateSSIDCredentialHistory("wireless", ssidHistoryPrefix(bad)+":global:CVK", versions, bad); err == nil {
			t.Fatal("invalid history accepted")
		}
	}
	if err := validateSSIDCredentialHistory("wireless", "ssid-credential:global:CVK", versions, history); err == nil {
		t.Fatal("legacy marker silently acquired history")
	}
	state := &ciskov1.NetworkControllerConfigurationStatus{CredentialApplications: []ciskov1.NetworkControllerConfigurationCredentialApplication{{Section: "wireless", Resource: "ssid-credential:global:CVK", ConfigUID: "owner", Versions: versions}}}
	// A real migration derives retired floors from previously applied keys.
	old := slices.Clone(versions)
	for index, h := range history {
		key := versions[0]
		key.Key = fmt.Sprintf("multi-psk:%d", h.Priority)
		key.Path = fmt.Sprintf("/ssids/0/multi_psk_settings/%d/passphrase_wo", index)
		key.Version = h.Version
		key.SourceAlias = fmt.Sprintf("priority-%d", h.Priority)
		key.SecretKey = key.SourceAlias
		old = append(old, key)
	}
	state.CredentialApplications[0].Versions = old
	if err := configurationCredentialCapacity(state, "owner", "wireless", resource, versions, history); err != nil {
		t.Fatal("legacy migration capacity", err)
	}
	// A stale plan cannot replace a newer durable retired-priority floor.
	advanced := append([]ciskov1.NetworkControllerConfigurationSSIDCredentialHistory(nil), history...)
	advanced[1].Version++
	current := state.CredentialApplications[0]
	current.Resource = ssidHistoryPrefix(advanced) + ":global:CVK"
	current.SSIDCredentialHistory = advanced
	current.Versions = versions
	newer := &ciskov1.NetworkControllerConfigurationStatus{CredentialApplications: []ciskov1.NetworkControllerConfigurationCredentialApplication{current}}
	if err := configurationCredentialCapacity(newer, "owner", "wireless", resource, versions, history); err == nil {
		t.Fatal("stale planner discarded newer retired floor")
	}
	state.CredentialApplications = append(state.CredentialApplications, ciskov1.NetworkControllerConfigurationCredentialApplication{Section: "wireless", Resource: resource, ConfigUID: "owner", Versions: versions, SSIDCredentialHistory: history})
	if err := configurationCredentialCapacity(state, "owner", "wireless", resource, versions, history); err == nil {
		t.Fatal("duplicate receipt aliases adopted")
	}
}

func TestWirelessSSIDRetirementRequiresExplicitSetAndAdvancedReturn(t *testing.T) {
	main := ciskov1.NetworkControllerConfigurationCredentialVersion{Key: "passphrase", Path: "/ssids/0/passphrase", Version: 1, SourceAlias: "main", SecretName: "keys", SecretKey: "main"}
	key := main
	key.Key = "multi-psk:4"
	key.Path = "/ssids/0/multi_psk_settings/0/passphrase_wo"
	key.Version = 7
	key.SourceAlias = "four"
	key.SecretKey = "four"
	prior := &ciskov1.NetworkControllerConfigurationCredentialApplication{Section: "wireless", Resource: "ssid-credential:global:CVK", Versions: []ciskov1.NetworkControllerConfigurationCredentialVersion{key, main}}
	if _, _, err := wirelessSSIDCredentialTransition(prior, []ciskov1.NetworkControllerConfigurationCredentialVersion{main}, false); err == nil {
		t.Fatal("omission removed key")
	}
	rotate, history, err := wirelessSSIDCredentialTransition(prior, []ciskov1.NetworkControllerConfigurationCredentialVersion{main}, true)
	if err != nil || !rotate || len(history) != 1 || history[0].Version != 7 {
		t.Fatal("retirement", err, history)
	}
	prior.Resource = ssidHistoryPrefix(history) + ":global:CVK"
	prior.SSIDCredentialHistory = history
	prior.Versions = []ciskov1.NetworkControllerConfigurationCredentialVersion{main}
	for _, counter := range []int64{1, 6, 7} {
		key.Version = counter
		if _, _, err := wirelessSSIDCredentialTransition(prior, []ciskov1.NetworkControllerConfigurationCredentialVersion{key, main}, true); err == nil {
			t.Fatal("retired counter reused", counter)
		}
	}
	key.Version = 8
	rotate, next, err := wirelessSSIDCredentialTransition(prior, []ciskov1.NetworkControllerConfigurationCredentialVersion{key, main}, true)
	if err != nil || !rotate || next[0].Version != 8 || history[0].Version != 7 {
		t.Fatal("advanced reintroduction", err)
	}
	if !configurationCredentialResourceSame("wireless", prior.Resource, "ssid-credential:global:CVK") || configurationCredentialResourceSame("wireless", prior.Resource, "ssid-credential:other:CVK") {
		t.Fatal("migration identity scope")
	}
	copy := prior.DeepCopy()
	copy.SSIDCredentialHistory[0].Version++
	if slices.Equal(copy.SSIDCredentialHistory, prior.SSIDCredentialHistory) {
		t.Fatal("history deepcopy alias")
	}
	if strings.Contains(ssidHistoryPrefix(history), "keys") {
		t.Fatal("source entered resource identity")
	}
}
