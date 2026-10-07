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
	"reflect"
	"strconv"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
)

func TestVKRBACStrictProfileGatesHighRiskRules(t *testing.T) {
	raw, err := os.ReadFile("../../charts/cisco-virtual-kubelet/templates/vk-rbac.yaml")
	if err != nil {
		t.Fatalf("read vk-rbac template: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		`$strictRBAC := eq .Values.rbac.profile "strict"`,
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

func TestVKRBACStrictMaintenanceReadsBothKindsWithoutEnablingWrites(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is required for rendered chart RBAC regression")
	}
	tests := []struct {
		name            string
		softwareUpgrade bool
		writeClass      bool
	}{
		{name: "both-disabled"},
		{
			name:            "software-upgrade-only",
			softwareUpgrade: true,
		},
		{
			name:       "write-class-only",
			writeClass: true,
		},
		{name: "both-enabled", softwareUpgrade: true, writeClass: true},
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
				t.Fatalf("strict RBAC is missing the gate-independent maintenance safety read:\n%s", text)
			}
			for kind, enabled := range map[string]bool{
				"iosxesoftwareupgrades":   tt.softwareUpgrade,
				"iosxeoperationalactions": tt.writeClass,
			} {
				writeRule := `resources: ["` + kind + `"]` + "\n    verbs: [\"get\", \"watch\", \"update\", \"patch\"]"
				statusRule := `resources: ["` + kind + `/status"]`
				if strings.Contains(text, writeRule) != enabled || strings.Contains(text, statusRule) != enabled {
					t.Fatalf("strict RBAC write/status grant for %s does not match enabled=%v", kind, enabled)
				}
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

func workerClusterRoles(t *testing.T) map[string]rbacv1.ClusterRole {
	t.Helper()
	raw, err := os.ReadFile("../../charts/cisco-virtual-kubelet/templates/controller-worker-rbac.yaml")
	if err != nil {
		t.Fatalf("read controller-worker RBAC template: %v", err)
	}
	roles := map[string]rbacv1.ClusterRole{}
	for _, doc := range strings.Split(string(raw), "\n---\n") {
		var kept []string
		for _, line := range strings.Split(doc, "\n") {
			if !strings.Contains(line, "{{") { // Helm directives and the labels they feed
				kept = append(kept, line)
			}
		}
		var role rbacv1.ClusterRole
		if err := yaml.Unmarshal([]byte(strings.Join(kept, "\n")), &role); err != nil || role.Kind != "ClusterRole" {
			continue
		}
		roles[role.Name] = role
	}
	return roles
}

func TestDeviceAdoptionWorkerRoleExtendsBaseOnlyByCiscoDevices(t *testing.T) {
	roles := workerClusterRoles(t)
	base, ok := roles[controlleradapter.DefaultWorkerClusterRole]
	if !ok {
		t.Fatal("base worker ClusterRole missing from chart")
	}
	adopt, ok := roles[controlleradapter.DeviceAdoptionWorkerClusterRole]
	if !ok {
		t.Fatal("device-adoption worker ClusterRole missing from chart")
	}
	if len(adopt.Rules) != len(base.Rules)+1 {
		t.Fatalf("adoption role has %d rules, want base (%d) + 1", len(adopt.Rules), len(base.Rules))
	}
	for i, rule := range base.Rules {
		if !reflect.DeepEqual(rule, adopt.Rules[i]) {
			t.Fatalf("adoption role drifted from base at rule %d: %+v vs %+v", i, adopt.Rules[i], rule)
		}
	}
	extra := adopt.Rules[len(adopt.Rules)-1]
	want := rbacv1.PolicyRule{
		APIGroups: []string{"cisco.vk"},
		Resources: []string{"ciscodevices"},
		Verbs:     []string{"get", "list", "watch", "create", "update", "patch"},
	}
	if !reflect.DeepEqual(extra, want) {
		t.Fatalf("unexpected extra rule %+v, want %+v", extra, want)
	}
	for _, rule := range adopt.Rules {
		for _, res := range rule.Resources {
			if res == "secrets" || res == "ciscodevices/status" || res == "ciscodevices/finalizers" {
				t.Fatalf("adoption role must not grant %q", res)
			}
		}
		for _, verb := range rule.Verbs {
			if verb == "delete" || verb == "deletecollection" || verb == "*" {
				t.Fatalf("adoption role must not grant verb %q", verb)
			}
		}
	}
}

func TestManagerBindMarkerCoversEveryAuditedWorkerRole(t *testing.T) {
	raw, err := os.ReadFile("networkcontroller_controller.go")
	if err != nil {
		t.Fatal(err)
	}
	var marker string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "resources=clusterroles") && strings.Contains(line, "verbs=bind") {
			marker = line
		}
	}
	for _, role := range []string{controlleradapter.DefaultWorkerClusterRole, controlleradapter.DeviceAdoptionWorkerClusterRole} {
		if !strings.Contains(marker, role) {
			t.Errorf("manager bind marker does not cover %q: %s", role, marker)
		}
	}
}
