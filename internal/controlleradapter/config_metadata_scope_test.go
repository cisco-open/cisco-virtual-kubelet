// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controlleradapter

import (
	"fmt"
	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"strings"
	"testing"
)

func TestConfigurationMetadataScopeRegistration(t *testing.T) {
	defer resetRegistry(t)()
	reg := validRegistration("scope-test")
	if ConfigurationMetadataScopeFor("unknown") != nil {
		t.Fatal("unknown scope")
	}
	scope := &ConfigurationMetadataScope{Selected: func(*cvk.NetworkController) bool { return true }, PhysicalIdentities: func(*cvk.NetworkController, *config.NetworkControllerConfig, ConfigurationMetadataSource) ([]string, error) {
		return []string{"serial"}, nil
	}}
	reg.ConfigurationMetadataScope = scope
	Register(reg)
	scope.Selected = nil
	first := ConfigurationMetadataScopeFor("scope-test")
	if first == nil || first.Selected == nil {
		t.Fatal("registration aliases caller callback struct")
	}
	first.Selected = nil
	if ConfigurationMetadataScopeFor("scope-test").Selected == nil {
		t.Fatal("lookup aliases registry callback struct")
	}
	for _, scope := range []*ConfigurationMetadataScope{{Selected: func(*cvk.NetworkController) bool { return true }}, {PhysicalIdentities: reg.ConfigurationMetadataScope.PhysicalIdentities}} {
		invalid := validRegistration("invalid")
		invalid.ConfigurationMetadataScope = scope
		if validateRegistration(invalid) == nil {
			t.Fatal("incomplete callback accepted")
		}
	}
	raw := ConfigurationMetadataSource{Raw: []byte("fixture-sensitive-intent")}
	if strings.Contains(fmt.Sprintf("%v %#v", raw, raw), "fixture-sensitive-intent") {
		t.Fatal("source contents exposed by formatting")
	}
}
