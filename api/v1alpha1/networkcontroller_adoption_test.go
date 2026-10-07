// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1alpha1

import "testing"

func adoptionFixture() *NetworkControllerDeviceAdoption {
	return &NetworkControllerDeviceAdoption{
		Enabled: true,
		Defaults: &NetworkControllerDeviceAccess{
			Username:            "admin",
			CredentialSecretRef: NetworkControllerSecretReference{Name: "switch-creds"},
			TLS:                 &TLSConfig{Enabled: true, InsecureSkipVerify: true},
		},
		ScopeOverrides: []NetworkControllerDeviceScopeOverride{
			{Scope: "fab-b", CredentialSecretRef: &NetworkControllerSecretReference{Name: "fab-b-creds"}},
			{Scope: "fab-c", Username: "ops", TLS: &TLSConfig{Enabled: true}},
		},
	}
}

func TestResolveAccessMergesOverridesFieldByField(t *testing.T) {
	a := adoptionFixture()

	def, ok := a.ResolveAccess("fab-a")
	if !ok || def.Username != "admin" || def.CredentialSecretRef.Name != "switch-creds" || !def.TLS.InsecureSkipVerify {
		t.Fatalf("unmatched scope must use defaults, got %+v ok=%v", def, ok)
	}
	b, _ := a.ResolveAccess("fab-b")
	if b.Username != "admin" || b.CredentialSecretRef.Name != "fab-b-creds" || !b.TLS.InsecureSkipVerify {
		t.Fatalf("secret-only override must inherit username and tls: %+v", b)
	}
	c, _ := a.ResolveAccess("fab-c")
	if c.Username != "ops" || c.CredentialSecretRef.Name != "switch-creds" || c.TLS.InsecureSkipVerify {
		t.Fatalf("username/tls override must inherit the secret: %+v", c)
	}
	// Mutating a resolved value must never leak into the spec.
	c.TLS.Enabled = false
	if !a.ScopeOverrides[1].TLS.Enabled {
		t.Fatal("ResolveAccess aliased override TLS")
	}
	def.TLS.InsecureSkipVerify = false
	if !a.Defaults.TLS.InsecureSkipVerify {
		t.Fatal("ResolveAccess aliased default TLS")
	}
}

func TestResolveAccessRefusesWhenDisabledOrIncomplete(t *testing.T) {
	var nilAdoption *NetworkControllerDeviceAdoption
	disabled := adoptionFixture()
	disabled.Enabled = false
	noDefaults := adoptionFixture()
	noDefaults.Defaults = nil
	for name, a := range map[string]*NetworkControllerDeviceAdoption{"nil": nilAdoption, "disabled": disabled, "no defaults": noDefaults} {
		if got, ok := a.ResolveAccess("fab-a"); ok || got.Username != "" {
			t.Errorf("%s: must not resolve credentials, got %+v", name, got)
		}
	}
}

func TestValidateDeviceAdoption(t *testing.T) {
	cases := map[string]struct {
		mutate  func(*NetworkControllerDeviceAdoption)
		wantErr bool
	}{
		"valid":                   {func(*NetworkControllerDeviceAdoption) {}, false},
		"disabled needs nothing":  {func(a *NetworkControllerDeviceAdoption) { a.Enabled, a.Defaults, a.ScopeOverrides = false, nil, nil }, false},
		"enabled without default": {func(a *NetworkControllerDeviceAdoption) { a.Defaults = nil }, true},
		"empty username":          {func(a *NetworkControllerDeviceAdoption) { a.Defaults.Username = "" }, true},
		"empty secret":            {func(a *NetworkControllerDeviceAdoption) { a.Defaults.CredentialSecretRef.Name = "" }, true},
		"bad secret name":         {func(a *NetworkControllerDeviceAdoption) { a.Defaults.CredentialSecretRef.Name = "Bad_Name" }, true},
		"bad override secret": {func(a *NetworkControllerDeviceAdoption) {
			a.ScopeOverrides[0].CredentialSecretRef.Name = "../x"
		}, true},
		"duplicate scope": {func(a *NetworkControllerDeviceAdoption) { a.ScopeOverrides[1].Scope = "fab-b" }, true},
		"path-like scope": {func(a *NetworkControllerDeviceAdoption) { a.ScopeOverrides[0].Scope = "a/b" }, true},
		"empty scope":     {func(a *NetworkControllerDeviceAdoption) { a.ScopeOverrides[0].Scope = "" }, true},
		"too many overrides": {func(a *NetworkControllerDeviceAdoption) {
			a.ScopeOverrides = make([]NetworkControllerDeviceScopeOverride, 65)
		}, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			spec := validNetworkControllerSpec()
			a := adoptionFixture()
			tc.mutate(a)
			spec.DeviceAdoption = a
			errs := ValidateNetworkControllerSpec(&spec)
			if (len(errs) > 0) != tc.wantErr {
				t.Fatalf("wantErr=%v, got %v", tc.wantErr, errs.ToAggregate())
			}
		})
	}
}
