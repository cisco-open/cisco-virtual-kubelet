// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package controller

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestVKRBACStrictProfileGatesHighRiskRules(t *testing.T) {
	raw, err := os.ReadFile("../../charts/cisco-virtual-kubelet/templates/vk-rbac.yaml")
	if err != nil {
		t.Fatalf("read vk-rbac template: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		`$strictRBAC := eq .Values.rbac.profile "strict"`,
		`$mutationControllersEnabled := or .Values.gnoi.enableSoftwareUpgrade .Values.gnoi.enableWriteClass`,
		`if or (not $strictRBAC) $mutationControllersEnabled`,
		`if or (not $strictRBAC) .Values.gnoi.enableSoftwareUpgrade`,
		`if or (not $strictRBAC) .Values.gnoi.enableWriteClass`,
		`if not $strictRBAC`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("vk-rbac template missing strict-profile guard %q", want)
		}
	}
	for _, forbidden := range []string{
		`resources: ["*"]`,
		`verbs: ["*"]`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("vk-rbac template contains wildcard RBAC %q", forbidden)
		}
	}
}

func TestVKRBACStrictSingleMutationControllerReadsBothKinds(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is required for rendered chart RBAC regression")
	}
	tests := []struct {
		name            string
		softwareUpgrade bool
		writeClass      bool
		enabledRule     string
		disabledRule    string
		enabledStatus   string
		disabledStatus  string
	}{
		{
			name:            "software-upgrade-only",
			softwareUpgrade: true,
			enabledRule:     `resources: ["iosxesoftwareupgrades"]`,
			disabledRule:    `resources: ["iosxeoperationalactions"]`,
			enabledStatus:   `resources: ["iosxesoftwareupgrades/status"]`,
			disabledStatus:  `resources: ["iosxeoperationalactions/status"]`,
		},
		{
			name:           "write-class-only",
			writeClass:     true,
			enabledRule:    `resources: ["iosxeoperationalactions"]`,
			disabledRule:   `resources: ["iosxesoftwareupgrades"]`,
			enabledStatus:  `resources: ["iosxeoperationalactions/status"]`,
			disabledStatus: `resources: ["iosxesoftwareupgrades/status"]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("helm", "template", "cvk", "../../charts/cisco-virtual-kubelet",
				"--namespace", "default",
				"--set", "rbac.profile=strict",
				"--set", "gnoi.enableSoftwareUpgrade="+strconv.FormatBool(tt.softwareUpgrade),
				"--set", "gnoi.enableWriteClass="+strconv.FormatBool(tt.writeClass))
			raw, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("helm template: %v\n%s", err, raw)
			}
			text := string(raw)
			sharedRead := `resources: ["iosxesoftwareupgrades", "iosxeoperationalactions"]
    verbs: ["list"]`
			if !strings.Contains(text, sharedRead) {
				t.Fatalf("strict single-controller RBAC is missing the cross-kind compatibility read:\n%s", text)
			}
			if !strings.Contains(text, tt.enabledRule+"\n    verbs: [\"get\", \"watch\", \"update\", \"patch\"]") ||
				!strings.Contains(text, tt.enabledStatus) {
				t.Fatalf("strict RBAC is missing enabled-controller write/status rules")
			}
			if strings.Contains(text, tt.disabledRule+"\n    verbs: [\"get\", \"watch\", \"update\", \"patch\"]") ||
				strings.Contains(text, tt.disabledStatus) {
				t.Fatalf("strict RBAC grants controller writes to disabled peer kind")
			}
		})
	}
}

func TestNetworkControllerWorkerRBACStaysSecretlessAndStatusOnly(t *testing.T) {
	raw, err := os.ReadFile("../../charts/cisco-virtual-kubelet/templates/controller-worker-rbac.yaml")
	if err != nil {
		t.Fatalf("read controller-worker RBAC template: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		`resources: ["networkcontrollers"]
    verbs: ["get", "list", "watch"]`,
		`resources: ["networkcontrollers/status"]
    verbs: ["get", "update", "patch"]`,
		`resources: ["networkcontrollerconfigs"]
    verbs: ["get", "list", "watch"]`,
		`resources: ["networkcontrollerconfigs/status"]
    verbs: ["get", "update", "patch"]`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("controller-worker RBAC missing least-privilege rule %q", want)
		}
	}
	for _, forbidden := range []string{
		`resources: ["secrets"]`,
		`resources: ["pods"]`,
		`resources: ["deployments"]`,
		`"leases"`,
		`"coordination.k8s.io"`,
		`verbs: ["*"]`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("controller-worker RBAC contains forbidden grant %q", forbidden)
		}
	}
}
