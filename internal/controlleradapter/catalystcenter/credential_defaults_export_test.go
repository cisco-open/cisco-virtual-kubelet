// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"strings"
	"testing"

	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

func missingCredentialDefaults(t *testing.T, raw []byte) []byte {
	t.Helper()
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		t.Fatal("fixture JSON")
	}
	cc := root["catalyst_center"].(map[string]any)
	groups := cc["network_settings"].(map[string]any)["device_credentials"].(map[string]any)
	for family, rows := range groups {
		for _, v := range rows.([]any) {
			entry := v.(map[string]any)
			switch family {
			case "https_read_credentials", "https_write_credentials":
				delete(entry, "port")
			case "snmpv3_credentials":
				delete(entry, "auth_type")
				delete(entry, "privacy_type")
			}
		}
	}
	out, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestCredentialDefaultsExport(t *testing.T) {
	for _, kind := range globalCredentialKinds {
		t.Run(kind.model, func(t *testing.T) {
			original := globalCredentialModel(kind.model, globalCredentialFixtureEntries[kind.model])
			raw := missingCredentialDefaults(t, original)
			out, meta, err := ExportCredentialDefaults(raw)
			if err != nil {
				t.Fatal(err)
			}
			wants, err := parseGlobalCredentials(out, true)
			expected, _ := parseGlobalCredentials(original, true)
			if err != nil || len(wants) != 1 || !globalCredentialMatches(expected[0].metadata, wants[0].metadata) {
				t.Fatal("wrong pinned resolution", err)
			}
			if meta.ConfigurationParity || meta.LeafSchemaValidated || !meta.ExecutionSyntaxValidated || meta.DefaultsSHA256 != nacmodel.DefaultsSHA256 || meta.OutputSHA256 != networkExportDigest(out) || meta.Exporter != CredentialDefaultsExporter {
				t.Fatal("incorrect provenance")
			}
			again, m, err := ExportCredentialDefaults(out)
			if err != nil || string(out) != string(again) || m.DefaultedFields != 0 {
				t.Fatal("export not idempotent", err)
			}
		})
	}
	for _, mode := range []string{"NOAUTHNOPRIV", "AUTHNOPRIV"} {
		entry := `{"name":"fixture","username":"tester","snmp_mode":"` + mode + `"`
		if mode == "AUTHNOPRIV" {
			entry += `,"auth_password_version":1`
		}
		entry += "}"
		out, _, err := ExportCredentialDefaults(globalCredentialModel("snmpv3_credentials", entry))
		if err != nil {
			t.Fatal("inactive defaults introduced contradictory fields", err)
		}
		if strings.Contains(string(out), "privacy_type") || mode == "NOAUTHNOPRIV" && strings.Contains(string(out), "auth_type") {
			t.Fatal("inactive protocol emitted")
		}
	}
	for _, entry := range []string{`{"name":"fixture","username":"tester","port":8443,"password_version":1}`, `{"name":"fixture","username":"tester","port":443,"password_version":1}`} {
		out, m, err := ExportCredentialDefaults(globalCredentialModel("https_read_credentials", entry))
		if err != nil || m.DefaultedFields != 0 {
			t.Fatal("explicit port replaced", err)
		}
		a, _ := parseGlobalCredentials(out, true)
		b, _ := parseGlobalCredentials(globalCredentialModel("https_read_credentials", entry), true)
		if !globalCredentialMatches(a[0].metadata, b[0].metadata) {
			t.Fatal("explicit value changed")
		}
	}
}
func TestCredentialDefaultsRefusesAmbiguity(t *testing.T) {
	good := string(globalCredentialModel("https_read_credentials", `{"name":"fixture","username":"tester","password_version":1}`))
	for _, raw := range []string{
		strings.Replace(good, `"password_version":1`, `"password_version":1,"port":null`, 1),
		strings.Replace(good, `"password_version":1`, `"password_version":1,"password":"PRIVATE_VALUE"`, 1),
		strings.Replace(good, `"password_version":1`, `"password_version":0`, 1),
		strings.Replace(good, `"password_version":1`, `"port":443`, 1),
		strings.Replace(good, `"https_read_credentials"`, `"unknown"`, 1),
		strings.Replace(good, `"username":"tester"`, `"username":"tester","username":"duplicate"`, 1),
		strings.Replace(good, `"network_settings":`, `"inventory":{},"network_settings":`, 1),
		good + "\n---\ncatalyst_center: {}",
		`catalyst_center: {network_settings: {device_credentials: {https_read_credentials: null}}}`,
	} {
		out, m, err := ExportCredentialDefaults([]byte(raw))
		if err == nil || out != nil || m.Exporter != "" || strings.Contains(err.Error(), "PRIVATE_VALUE") {
			t.Fatal("ambiguous input escaped")
		}
	}
}
func TestCredentialDefaultsPublicManifestRecovery(t *testing.T) {
	exerciseGlobalCredentialManifest(t, true, resolveManifestCredentialDefaults)
}
func resolveManifestCredentialDefaults(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig) {
	resolveManifestCredentialDefaultsWithOverrides(t, a, config, nil)
}
func resolveManifestCredentialDefaultsWithOverrides(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, overrides []byte) {
	t.Helper()
	var root map[string]any
	if json.Unmarshal(config.Spec.Source.Inline.Raw, &root) != nil {
		t.Fatal("fixture JSON")
	}
	network := root["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)
	scoped, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_settings": map[string]any{"device_credentials": network["device_credentials"]}}})
	out, _, err := ExportCredentialDefaultsWithOverrides(missingCredentialDefaults(t, scoped), overrides)
	if err != nil {
		t.Fatal(err)
	}
	var resolved map[string]any
	json.Unmarshal(out, &resolved)
	network["device_credentials"] = resolved["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)["device_credentials"]
	config.Spec.Source.Inline.Raw, _ = json.Marshal(root)
	if err := a.statusWriter.Update(context.Background(), config); err != nil {
		t.Fatal(err)
	}
}

const credentialOverrideFixture = `defaults:
  catalyst_center:
    network_settings:
      device_credentials:
        https_read_credentials:
          port: 8443
        snmpv3_credentials:
          privacy_type: AES256
`

func TestCredentialDefaultsOverrides(t *testing.T) {
	raw := missingCredentialDefaults(t, globalCredentialModel("https_read_credentials", globalCredentialFixtureEntries["https_read_credentials"]))
	out, m, err := ExportCredentialDefaultsWithOverrides(raw, []byte(credentialOverrideFixture))
	if err != nil {
		t.Fatal(err)
	}
	wants, err := parseGlobalCredentials(out, true)
	if err != nil || *wants[0].metadata.Port != 8443 || m.OverridesSHA256 != networkExportDigest([]byte(credentialOverrideFixture)) {
		t.Fatal("override lost", err)
	}
	explicit := globalCredentialModel("https_read_credentials", strings.Replace(globalCredentialFixtureEntries["https_read_credentials"], `"port":443`, `"port":9443`, 1))
	out, m, err = ExportCredentialDefaultsWithOverrides(explicit, []byte(credentialOverrideFixture))
	wants, _ = parseGlobalCredentials(out, true)
	if err != nil || *wants[0].metadata.Port != 9443 || m.DefaultedFields != 0 {
		t.Fatal("explicit field lost precedence")
	}
	snmp := missingCredentialDefaults(t, globalCredentialModel("snmpv3_credentials", globalCredentialFixtureEntries["snmpv3_credentials"]))
	out, _, err = ExportCredentialDefaultsWithOverrides(snmp, []byte(credentialOverrideFixture))
	wants, _ = parseGlobalCredentials(out, true)
	if err != nil || wants[0].metadata.AuthType != "SHA" || wants[0].metadata.PrivacyType != "AES256" {
		t.Fatal("partial overlay replaced pinned fallback", err)
	}
	for _, bad := range []string{
		strings.Replace(credentialOverrideFixture, "port: 8443", "port: 0", 1),
		strings.Replace(credentialOverrideFixture, "port: 8443", "port: .inf", 1),
		strings.Replace(credentialOverrideFixture, "port: 8443", "port: null", 1),
		strings.Replace(credentialOverrideFixture, "port: 8443", "password: PRIVATE_OVERRIDE", 1),
		strings.Replace(credentialOverrideFixture, "port: 8443", "password_version: 1", 1),
		strings.Replace(credentialOverrideFixture, "AES256", "INVALID_PROTOCOL", 1),
		strings.Replace(credentialOverrideFixture, "https_read_credentials:", "unknown:", 1),
		strings.Replace(credentialOverrideFixture, "port: 8443", "port: 8443\n          port: 9443", 1),
		strings.Replace(credentialOverrideFixture, "network_settings:", "sites: {}\n    network_settings:", 1),
		credentialOverrideFixture + "\n---\nother: {}",
	} {
		// Invalid unused SNMP defaults and shadowed HTTPS values must also refuse.
		result, meta, err := ExportCredentialDefaultsWithOverrides(explicit, []byte(bad))
		if err == nil || result != nil || meta.Exporter != "" || strings.Contains(err.Error(), "PRIVATE_OVERRIDE") {
			t.Fatal("invalid/shadowed override escaped")
		}
	}
}

func TestCredentialDefaultsOverridePublicManifestRecovery(t *testing.T) {
	exerciseGlobalCredentialManifest(t, true, func(child *testing.T, a *adapter, config *configapi.NetworkControllerConfig) {
		resolveManifestCredentialDefaultsWithOverrides(child, a, config, []byte(credentialOverrideFixture))
	})
}
