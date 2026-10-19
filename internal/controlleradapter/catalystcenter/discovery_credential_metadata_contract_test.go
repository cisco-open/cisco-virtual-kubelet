// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"os"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
)

// Acknowledged observation keeps every original Secret binding even when no
// projected bytes remain. Clearing SecretRefs to bypass the mount gate would
// lose these metadata checks, particularly for terminal prune with no versions.
func TestDiscoveryCredentialRetainedMetadataContract(t *testing.T) {
	raw := discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1}, false)
	a, nc, c, _, paths := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
	for _, path := range paths {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if controlleradapter.ValidateConfigContract(nc, c, a.intentSecretPath) == nil {
		t.Fatal("missing projection passed dispatch contract")
	}
	if !discoveryCredentialRetainedContract(nc, c, &cvk.NetworkControllerConfigurationOperation{}) {
		t.Fatal("metadata-only observation requires projected bytes")
	}
	cases := []struct {
		name   string
		change func(*cvk.NetworkController, *configapi.NetworkControllerConfig)
	}{
		{"unauthorized-alias", func(_ *cvk.NetworkController, c *configapi.NetworkControllerConfig) {
			c.Spec.SecretRefs[0].Source = "unauthorized"
		}},
		{"unsupported-use", func(_ *cvk.NetworkController, c *configapi.NetworkControllerConfig) {
			c.Spec.SecretRefs[0].Use = "Unsupported"
		}},
		{"unmanaged-section", func(_ *cvk.NetworkController, c *configapi.NetworkControllerConfig) {
			c.Spec.SecretRefs[0].Section = "sites"
		}},
		{"invalid-pointer", func(_ *cvk.NetworkController, c *configapi.NetworkControllerConfig) {
			c.Spec.SecretRefs[0].Path = "not-a-pointer"
		}},
		{"controller-mismatch", func(_ *cvk.NetworkController, c *configapi.NetworkControllerConfig) {
			c.Spec.ControllerRef.Name = "other"
		}},
		{"paused", func(nc *cvk.NetworkController, _ *configapi.NetworkControllerConfig) { nc.Spec.Paused = true }},
		{"model-version", func(_ *cvk.NetworkController, c *configapi.NetworkControllerConfig) {
			c.Spec.ModelSource.ModelVersion = "0.0.0"
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			n, c := nc.DeepCopy(), c.DeepCopy()
			test.change(n, c)
			if discoveryCredentialRetainedContract(n, c, &cvk.NetworkControllerConfigurationOperation{}) {
				t.Fatal("invalid metadata authorized retained observation")
			}
		})
	}
	// Valid metadata alone cannot authorize an old operation after alias remapping.
	deletingPolicy := c.DeepCopy()
	deletingPolicy.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	if !discoveryDeletionEligible(nc, deletingPolicy) || discoveryConfigSpecShape(deletingPolicy) || supportedConfigurationApply(deletingPolicy) {
		t.Fatal("metadata enrollment must remain distinct from unenrolled mutation admission")
	}
	unsupported := deletingPolicy.DeepCopy()
	unsupported.Spec.ModelSource.SchemaDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if discoveryDeletionEligible(nc, unsupported) {
		t.Fatal("unqualified schema digest enrolled cleanup")
	}
	ref := c.Spec.SecretRefs[0]
	var binding cvk.NetworkControllerConfigurationCredentialVersion
	binding.Path = ref.Path
	binding.SourceAlias = ref.Source
	binding.Version = 1
	for _, source := range nc.Spec.IntentSecretSources {
		if source.Alias == ref.Source {
			binding.SecretName = source.Name
			binding.SecretKey = source.Key
		}
	}
	op := &cvk.NetworkControllerConfigurationOperation{CredentialVersions: []cvk.NetworkControllerConfigurationCredentialVersion{binding}}
	if !discoveryCredentialRetainedContract(nc, c, op) {
		t.Fatal("original retained binding refused")
	}
	changed := nc.DeepCopy()
	for i := range changed.Spec.IntentSecretSources {
		if changed.Spec.IntentSecretSources[i].Alias == ref.Source {
			changed.Spec.IntentSecretSources[i].Name = "different-secret"
		}
	}
	if controlleradapter.ValidateConfigMetadataContract(changed, c) != nil {
		t.Fatal("replacement binding is not valid current metadata")
	}
	if discoveryCredentialRetainedContract(changed, c, op) {
		t.Fatal("current metadata relabelled original operation binding")
	}
}
