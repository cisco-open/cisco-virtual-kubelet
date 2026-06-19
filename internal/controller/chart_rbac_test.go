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
