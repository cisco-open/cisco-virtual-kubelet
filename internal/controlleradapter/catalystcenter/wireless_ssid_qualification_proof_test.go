// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ssidQualificationProof(t *testing.T, stage int) (ssidQualificationIdentity, *cvk.NetworkControllerConfigurationStatus, []byte) {
	t.Helper()
	identity := ssidQualificationIdentity{Name: "CVK_PSK_fixture", ID: "ssid-id", SiteID: "global", Posts: 1, Puts: stage, MultiPSK: true, Retirement: true}
	version := func(key string, n int64, path string) cvk.NetworkControllerConfigurationCredentialVersion {
		return cvk.NetworkControllerConfigurationCredentialVersion{Key: key, Version: n, Path: path, SourceAlias: strings.ReplaceAll(key, ":", "-"), SecretName: "keys", SecretKey: strings.ReplaceAll(key, ":", "-")}
	}
	main := version("passphrase", 1, "/ssids/0/passphrase")
	prior := []cvk.NetworkControllerConfigurationCredentialVersion{main, version("multi-psk:0", 1, "/ssids/0/multi_psk_settings/0/passphrase_wo"), version("multi-psk:4", 1, "/ssids/0/multi_psk_settings/1/passphrase_wo")}
	desired := append([]cvk.NetworkControllerConfigurationCredentialVersion(nil), prior...)
	desired[2].Version = 2
	oldFloor, newFloor := int64(1), int64(2)
	switch stage {
	case 2:
		prior[2].Version = 2
		desired = desired[:2]
		oldFloor = 2
	case 3:
		prior = prior[:2]
		desired[2].Version = 3
		oldFloor = 2
		newFloor = 3
	case 4:
		prior[2].Version = 3
		desired = desired[:1]
		oldFloor = 3
		newFloor = 3
	}
	oldHistory := []cvk.NetworkControllerConfigurationSSIDCredentialHistory{{Priority: 0, Version: 1}, {Priority: 4, Version: oldFloor}}
	history := []cvk.NetworkControllerConfigurationSSIDCredentialHistory{{Priority: 0, Version: 1}, {Priority: 4, Version: newFloor}}
	now := metav1.NewTime(time.Now().Add(-time.Second))
	operation := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("b", 32), ConfigName: "fixture", ConfigUID: "config-uid", ConfigGeneration: 1, ControllerGeneration: 1, Section: "wireless", Resource: ssidHistoryPrefix(history) + ":global:" + identity.Name, Action: "Update", Phase: "Accepted", TargetID: identity.ID, ParentID: identity.SiteID, TaskID: "task-id", SubmittedAt: &now, CredentialVersions: desired, SSIDCredentialHistory: history}
	application := cvk.NetworkControllerConfigurationCredentialApplication{Section: "wireless", Resource: ssidHistoryPrefix(oldHistory) + ":global:" + identity.Name, ConfigUID: operation.ConfigUID, TargetID: identity.ID, OperationID: strings.Repeat("a", 32), Versions: prior, SSIDCredentialHistory: oldHistory}
	row := ssidTestRow(identity.Name)
	row["auth_type"] = "WPA2_PERSONAL"
	row["enabled"] = false
	row["auth_key_psk"] = true
	row["rsn_cipher_suite_ccmp128"] = true
	row["passphrase_version"] = 1
	entries := []any{}
	for _, v := range desired[1:] {
		priority := 0
		if v.Key == "multi-psk:4" {
			priority = 4
		}
		entries = append(entries, map[string]any{"priority": priority, "passphrase_type": "ASCII", "passphrase_wo_version": v.Version})
	}
	row["multi_psk_settings"] = entries
	slices.SortFunc(prior, func(a, b cvk.NetworkControllerConfigurationCredentialVersion) int {
		return strings.Compare(a.Path, b.Path)
	})
	slices.SortFunc(desired, func(a, b cvk.NetworkControllerConfigurationCredentialVersion) int {
		return strings.Compare(a.Path, b.Path)
	})
	return identity, &cvk.NetworkControllerConfigurationStatus{Operation: operation, CredentialApplications: []cvk.NetworkControllerConfigurationCredentialApplication{application}}, ssidTestModel(row)
}

func TestWirelessSSIDQualificationRecoveryStages(t *testing.T) {
	for stage := 1; stage <= 4; stage++ {
		for _, phase := range []string{"Accepted", "Verifying", "Unknown"} {
			t.Run(fmt.Sprintf("stage-%d/%s", stage, phase), func(t *testing.T) {
				identity, state, source := ssidQualificationProof(t, stage)
				state.Operation.Phase = phase
				if err := validateSSIDQualificationRecovery(identity, state, source); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
	identity, state, source := ssidQualificationProof(t, 1)
	identity.Retirement = false
	state.CredentialApplications[0].SSIDCredentialHistory = nil
	state.CredentialApplications[0].Resource = "ssid-credential:global:" + identity.Name
	state.Operation.SSIDCredentialHistory = nil
	state.Operation.Resource = state.CredentialApplications[0].Resource
	if err := validateSSIDQualificationRecovery(identity, state, source); err != nil {
		t.Fatal("legacy multi-PSK proof refused", err)
	}
}

func TestWirelessSSIDQualificationRecoveryRefusesUncorrelatedEvidence(t *testing.T) {
	cases := map[string]func(*ssidQualificationIdentity, *cvk.NetworkControllerConfigurationStatus, *[]byte){
		"unrelated name": func(i *ssidQualificationIdentity, _ *cvk.NetworkControllerConfigurationStatus, _ *[]byte) {
			i.Name = "production"
		},
		"duplicate create": func(i *ssidQualificationIdentity, _ *cvk.NetworkControllerConfigurationStatus, _ *[]byte) {
			i.Posts = 2
		},
		"already deleted": func(i *ssidQualificationIdentity, _ *cvk.NetworkControllerConfigurationStatus, _ *[]byte) {
			i.Deletes = 1
		},
		"unbounded updates": func(i *ssidQualificationIdentity, _ *cvk.NetworkControllerConfigurationStatus, _ *[]byte) { i.Puts = 5 },
		"wrong stage":       func(i *ssidQualificationIdentity, _ *cvk.NetworkControllerConfigurationStatus, _ *[]byte) { i.Puts = 3 },
		"missing retirement flag": func(i *ssidQualificationIdentity, _ *cvk.NetworkControllerConfigurationStatus, _ *[]byte) {
			i.Retirement = false
		},
		"missing multi flag": func(i *ssidQualificationIdentity, _ *cvk.NetworkControllerConfigurationStatus, _ *[]byte) {
			i.MultiPSK = false
		},
		"source unavailable": func(_ *ssidQualificationIdentity, _ *cvk.NetworkControllerConfigurationStatus, p *[]byte) { *p = nil },
		"enabled source": func(_ *ssidQualificationIdentity, _ *cvk.NetworkControllerConfigurationStatus, p *[]byte) {
			*p = []byte(strings.Replace(string(*p), `"enabled":false`, `"enabled":true`, 1))
		},
		"task missing": func(_ *ssidQualificationIdentity, s *cvk.NetworkControllerConfigurationStatus, _ *[]byte) {
			s.Operation.Phase = "Unknown"
			s.Operation.TaskID = ""
		},
		"submitting": func(_ *ssidQualificationIdentity, s *cvk.NetworkControllerConfigurationStatus, _ *[]byte) {
			s.Operation.Phase = "Submitting"
		},
		"verified": func(_ *ssidQualificationIdentity, s *cvk.NetworkControllerConfigurationStatus, _ *[]byte) {
			s.Operation.Phase = "Verified"
		},
		"native replaced": func(_ *ssidQualificationIdentity, s *cvk.NetworkControllerConfigurationStatus, _ *[]byte) {
			s.Operation.TargetID = "replacement"
		},
		"site replaced": func(_ *ssidQualificationIdentity, s *cvk.NetworkControllerConfigurationStatus, _ *[]byte) {
			s.Operation.ParentID = "replacement"
		},
		"foreign owner": func(_ *ssidQualificationIdentity, s *cvk.NetworkControllerConfigurationStatus, _ *[]byte) {
			s.CredentialApplications[0].ConfigUID = "foreign"
		},
		"same operation receipt": func(_ *ssidQualificationIdentity, s *cvk.NetworkControllerConfigurationStatus, _ *[]byte) {
			s.CredentialApplications[0].OperationID = s.Operation.ID
		},
		"pruned history": func(_ *ssidQualificationIdentity, s *cvk.NetworkControllerConfigurationStatus, _ *[]byte) {
			s.Operation.SSIDCredentialHistory = nil
		},
		"changed history": func(_ *ssidQualificationIdentity, s *cvk.NetworkControllerConfigurationStatus, _ *[]byte) {
			s.Operation.SSIDCredentialHistory[1].Version++
		},
		"pruned active versions": func(_ *ssidQualificationIdentity, s *cvk.NetworkControllerConfigurationStatus, _ *[]byte) {
			s.Operation.CredentialVersions = nil
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			identity, state, source := ssidQualificationProof(t, 2)
			mutate(&identity, state, &source)
			if err := validateSSIDQualificationRecovery(identity, state, source); err == nil {
				t.Fatal("unsafe recovery evidence accepted")
			}
		})
	}
}
