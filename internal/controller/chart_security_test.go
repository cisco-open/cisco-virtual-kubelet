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

	"sigs.k8s.io/yaml"
)

// TestControllerDeploymentRendersSecurityContexts guards the restricted-
// profile hardening of the controller Deployment template: the pod and
// container securityContext hooks must stay wired to their values, and the
// writable /tmp emptyDir that makes readOnlyRootFilesystem viable must stay
// unconditional. Template-text assertions, matching the vk-rbac chart test.
func TestControllerDeploymentRendersSecurityContexts(t *testing.T) {
	raw, err := os.ReadFile("../../charts/cisco-virtual-kubelet/templates/deployment.yaml")
	if err != nil {
		t.Fatalf("read deployment template: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		`{{- with .Values.podSecurityContext }}`,
		`{{- with .Values.containerSecurityContext }}`,
		`- name: tmp`,
		`emptyDir: {}`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("deployment template missing security hardening construct %q", want)
		}
	}
}

// TestChartDefaultsMeetRestrictedProfile asserts the shipped values keep the
// Pod Security Standards "restricted" defaults; loosening them must be a
// deliberate, reviewed change to values.yaml.
func TestChartDefaultsMeetRestrictedProfile(t *testing.T) {
	raw, err := os.ReadFile("../../charts/cisco-virtual-kubelet/values.yaml")
	if err != nil {
		t.Fatalf("read chart values: %v", err)
	}
	var values struct {
		PodSecurityContext struct {
			RunAsNonRoot   bool  `yaml:"runAsNonRoot"`
			RunAsUser      int64 `yaml:"runAsUser"`
			RunAsGroup     int64 `yaml:"runAsGroup"`
			FSGroup        int64 `yaml:"fsGroup"`
			SeccompProfile struct {
				Type string `yaml:"type"`
			} `yaml:"seccompProfile"`
		} `yaml:"podSecurityContext"`
		ContainerSecurityContext struct {
			AllowPrivilegeEscalation *bool `yaml:"allowPrivilegeEscalation"`
			ReadOnlyRootFilesystem   bool  `yaml:"readOnlyRootFilesystem"`
			Capabilities             struct {
				Drop []string `yaml:"drop"`
			} `yaml:"capabilities"`
		} `yaml:"containerSecurityContext"`
	}
	if err := yaml.Unmarshal(raw, &values); err != nil {
		t.Fatalf("parse chart values: %v", err)
	}

	pod := values.PodSecurityContext
	if !pod.RunAsNonRoot || pod.RunAsUser != distrolessNonRootUID ||
		pod.RunAsGroup != distrolessNonRootGID || pod.FSGroup != distrolessNonRootGID ||
		pod.SeccompProfile.Type != "RuntimeDefault" {
		t.Fatalf("podSecurityContext defaults do not match the restricted numeric identity: %#v", pod)
	}
	container := values.ContainerSecurityContext
	if container.AllowPrivilegeEscalation == nil || *container.AllowPrivilegeEscalation ||
		!container.ReadOnlyRootFilesystem || len(container.Capabilities.Drop) != 1 ||
		container.Capabilities.Drop[0] != "ALL" {
		t.Fatalf("containerSecurityContext defaults do not meet the restricted profile: %#v", container)
	}
}

// TestRuntimeImagesUseNumericNonRootUser guards the kubelet compatibility
// requirement behind runAsNonRoot: named OCI users cannot be verified.
func TestRuntimeImagesUseNumericNonRootUser(t *testing.T) {
	for _, path := range []string{"../../Dockerfile", "../../Dockerfile.config-lint"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(raw), "USER 65532:65532") {
			t.Errorf("%s must use the numeric distroless nonroot UID/GID", path)
		}
	}
}
