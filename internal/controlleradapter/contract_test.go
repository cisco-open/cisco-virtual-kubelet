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

package controlleradapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestValidateConfigContract(t *testing.T) {
	defer resetRegistry(t)()
	reg := validRegistration("contract-test")
	reg.Descriptor.NetAsCode.Format = "netascode-contract-test"
	reg.Descriptor.NetAsCode.ModelVersions = []string{"1.2.0", "1.3.0"}
	reg.Descriptor.NetAsCode.Sections = []string{"inventory", "sites"}
	Register(reg)

	controller, config := validContractObjects()
	intentSecretRoot := t.TempDir()
	writeContractIntentSecret(t, intentSecretRoot, config)
	if err := ValidateConfigContract(controller, config, intentSecretRoot); err != nil {
		t.Fatalf("ValidateConfigContract valid input: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ciskov1.NetworkController, *configv1alpha1.NetworkControllerConfig)
		want   string
	}{
		{name: "wrong controller", mutate: func(_ *ciskov1.NetworkController, c *configv1alpha1.NetworkControllerConfig) {
			c.Spec.ControllerRef.Name = "other"
		}, want: "references"},
		{name: "cross namespace", mutate: func(_ *ciskov1.NetworkController, c *configv1alpha1.NetworkControllerConfig) { c.Namespace = "other" }, want: "worker owns"},
		{name: "paused controller", mutate: func(c *ciskov1.NetworkController, _ *configv1alpha1.NetworkControllerConfig) {
			c.Spec.Paused = true
		}, want: "is paused"},
		{name: "format", mutate: func(_ *ciskov1.NetworkController, c *configv1alpha1.NetworkControllerConfig) {
			c.Spec.ModelSource.Format = "netascode-other"
		}, want: "incompatible"},
		{name: "version", mutate: func(_ *ciskov1.NetworkController, c *configv1alpha1.NetworkControllerConfig) {
			c.Spec.ModelSource.ModelVersion = "9.9.9"
		}, want: "not qualified"},
		{name: "section", mutate: func(_ *ciskov1.NetworkController, c *configv1alpha1.NetworkControllerConfig) {
			c.Spec.ManagedSections = []string{"fabric"}
			c.Spec.SecretRefs[0].Section = "fabric"
		}, want: "not supported"},
		{name: "unauthorized secret alias", mutate: func(_ *ciskov1.NetworkController, c *configv1alpha1.NetworkControllerConfig) {
			c.Spec.SecretRefs[0].Source = "other-credentials"
		}, want: "not authorized"},
		{name: "unresolved", mutate: func(_ *ciskov1.NetworkController, c *configv1alpha1.NetworkControllerConfig) {
			c.Spec.ModelSource.Resolved = false
		}, want: "must be resolved"},
		{name: "unknown adapter", mutate: func(c *ciskov1.NetworkController, _ *configv1alpha1.NetworkControllerConfig) {
			c.Spec.Type = "unknown-contract"
		}, want: "not registered"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotController := controller.DeepCopy()
			gotConfig := config.DeepCopy()
			test.mutate(gotController, gotConfig)
			if err := ValidateConfigContract(gotController, gotConfig, intentSecretRoot); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateConfigContract error=%v, want substring %q", err, test.want)
			}
		})
	}
	if err := ValidateConfigContract(controller, config, t.TempDir()); err == nil || !strings.Contains(err.Error(), "not projected") {
		t.Fatalf("missing projected file error=%v", err)
	}
	remappedController := controller.DeepCopy()
	remappedController.Spec.IntentSecretSources[0].Name = "replacement-site-secret"
	if err := ValidateConfigContract(remappedController, config, intentSecretRoot); err == nil || !strings.Contains(err.Error(), "not projected") {
		t.Fatalf("stale projection after alias remap error=%v", err)
	}
}

func validContractObjects() (*ciskov1.NetworkController, *configv1alpha1.NetworkControllerConfig) {
	controller := &ciskov1.NetworkController{
		ObjectMeta: metav1.ObjectMeta{Name: "primary", Namespace: "campus"},
		Spec: ciskov1.NetworkControllerSpec{
			Type:                "contract-test",
			Endpoint:            "https://controller.example.test",
			CredentialSecretRef: ciskov1.NetworkControllerSecretReference{Name: "credentials"},
			IntentSecretSources: []ciskov1.NetworkControllerIntentSecretSource{{Alias: "site-credentials", Name: "site-secret", Key: "password"}},
		},
	}
	config := &configv1alpha1.NetworkControllerConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "intent", Namespace: "campus"},
		Spec: configv1alpha1.NetworkControllerConfigSpec{
			ControllerRef:   configv1alpha1.NetworkControllerRef{Name: controller.Name},
			ManagedSections: []string{"sites"},
			Source: configv1alpha1.NetworkControllerConfigurationSource{
				Inline: &runtime.RawExtension{Raw: []byte(`{"sites": []}`)},
			},
			ModelSource: configv1alpha1.NetworkControllerNetAsCodeModelSource{
				Format:       "netascode-contract-test",
				ModelVersion: "1.2.0",
				Resolved:     true,
			},
			SecretRefs: []configv1alpha1.NetworkControllerSecretRef{{Section: "sites", Path: "/credentials/password", Source: "site-credentials"}},
		},
	}
	return controller, config
}

func writeContractIntentSecret(t *testing.T, root string, config *configv1alpha1.NetworkControllerConfig) {
	t.Helper()
	ref := config.Spec.SecretRefs[0]
	relativePath, err := IntentSecretRelativePath(IntentSecretPathInput{
		ConfigName:  config.Name,
		Section:     ref.Section,
		JSONPointer: ref.Path,
		SourceAlias: ref.Source,
		SecretName:  "site-secret",
		SecretKey:   "password",
	})
	if err != nil {
		t.Fatalf("IntentSecretRelativePath: %v", err)
	}
	path := filepath.Join(root, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create intent Secret directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("test-only-secret"), 0o600); err != nil {
		t.Fatalf("write intent Secret fixture: %v", err)
	}
}
