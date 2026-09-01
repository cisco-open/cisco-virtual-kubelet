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
	"strings"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
)

func TestNewWorkerConfigContainsOnlyBootstrapIdentityAndPaths(t *testing.T) {
	controller := &ciskov1.NetworkController{}
	controller.Namespace = "campus"
	controller.Name = "primary"
	controller.UID = types.UID("primary-uid")
	controller.Spec.Type = "test-controller"
	controller.Spec.Endpoint = "https://controller.example.test"
	controller.Spec.CredentialSecretRef.Name = "highly-sensitive-secret-name"

	config, err := NewWorkerConfig(controller)
	if err != nil {
		t.Fatalf("NewWorkerConfig: %v", err)
	}
	if config.ControllerRef.Namespace != "campus" || config.ControllerRef.Name != "primary" || config.ControllerRef.UID != "primary-uid" || config.Type != "test-controller" {
		t.Fatalf("config identity=%+v", config)
	}
	// WorkerConfig has no endpoint or Secret-reference fields by design. This
	// string guard also catches accidental future serialization through a
	// generic metadata map.
	serialized := strings.Join([]string{config.APIVersion, config.Kind, config.ControllerRef.Namespace, config.ControllerRef.Name, config.ControllerRef.UID, config.Type}, "\n")
	for _, forbidden := range []string{controller.Spec.Endpoint, controller.Spec.CredentialSecretRef.Name} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("bootstrap config leaked %q: %s", forbidden, serialized)
		}
	}
}

func TestWorkerConfigValidation(t *testing.T) {
	controller := &ciskov1.NetworkController{}
	controller.Namespace = "campus"
	controller.Name = "primary"
	controller.UID = types.UID("primary-uid")
	controller.Spec.Type = "test-controller"
	valid, err := NewWorkerConfig(controller)
	if err != nil {
		t.Fatalf("NewWorkerConfig: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*WorkerConfig)
		want   string
	}{
		{name: "version", mutate: func(c *WorkerConfig) { c.APIVersion = "v2" }, want: "apiVersion"},
		{name: "kind", mutate: func(c *WorkerConfig) { c.Kind = "Other" }, want: "kind"},
		{name: "namespace", mutate: func(c *WorkerConfig) { c.ControllerRef.Namespace = "Bad_NS" }, want: "namespace"},
		{name: "name", mutate: func(c *WorkerConfig) { c.ControllerRef.Name = "Bad_Name" }, want: "name"},
		{name: "UID", mutate: func(c *WorkerConfig) { c.ControllerRef.UID = "" }, want: "UID"},
		{name: "type", mutate: func(c *WorkerConfig) { c.Type = "Bad_Type" }, want: "type"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := valid
			test.mutate(&got)
			if err := got.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestWorkerConfigIdentityBinding(t *testing.T) {
	controller := &ciskov1.NetworkController{}
	controller.Namespace = "campus"
	controller.Name = "primary"
	controller.UID = types.UID("primary-uid")
	controller.Spec.Type = "test-controller"
	config, err := NewWorkerConfig(controller)
	if err != nil {
		t.Fatalf("NewWorkerConfig: %v", err)
	}
	if err := config.ValidateIdentity("campus", "primary", "primary-uid", "test-controller"); err != nil {
		t.Fatalf("ValidateIdentity: %v", err)
	}
	for _, test := range []struct {
		name, namespace, controller, uid, typeName, want string
	}{
		{name: "namespace", namespace: "other", controller: "primary", uid: "primary-uid", typeName: "test-controller", want: "namespace"},
		{name: "name", namespace: "campus", controller: "other", uid: "primary-uid", typeName: "test-controller", want: "name"},
		{name: "UID", namespace: "campus", controller: "primary", uid: "other-uid", typeName: "test-controller", want: "UID"},
		{name: "type", namespace: "campus", controller: "primary", uid: "primary-uid", typeName: "other-controller", want: "type"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := config.ValidateIdentity(test.namespace, test.controller, test.uid, test.typeName); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateIdentity error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestIntentSecretRelativePathIsDeterministicAndSafe(t *testing.T) {
	input := IntentSecretPathInput{
		ConfigName:  "campus-intent",
		Section:     "network_settings",
		JSONPointer: "/device_credentials/0/password",
		SourceAlias: "device-credentials",
		SecretName:  "controller-secret",
		SecretKey:   "password",
	}
	first, err := IntentSecretRelativePath(input)
	if err != nil {
		t.Fatalf("IntentSecretRelativePath: %v", err)
	}
	second, err := IntentSecretRelativePath(input)
	if err != nil {
		t.Fatalf("IntentSecretRelativePath second call: %v", err)
	}
	if first != second || !strings.HasPrefix(first, "campus-intent/network_settings/") || strings.Contains(first, "password") {
		t.Fatalf("relative path=%q second=%q", first, second)
	}
	changed := input
	changed.SecretKey = "new-password"
	if changedPath, err := IntentSecretRelativePath(changed); err != nil || changedPath == first {
		t.Fatalf("authorization mapping change path=%q error=%v, want a distinct path", changedPath, err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*IntentSecretPathInput)
	}{
		{name: "config", mutate: func(input *IntentSecretPathInput) { input.ConfigName = "Bad_Name" }},
		{name: "section", mutate: func(input *IntentSecretPathInput) { input.Section = "Bad-Section" }},
		{name: "pointer", mutate: func(input *IntentSecretPathInput) { input.JSONPointer = "password" }},
		{name: "source", mutate: func(input *IntentSecretPathInput) { input.SourceAlias = "Bad_Source" }},
		{name: "Secret name", mutate: func(input *IntentSecretPathInput) { input.SecretName = "Bad_Secret" }},
		{name: "Secret key", mutate: func(input *IntentSecretPathInput) { input.SecretKey = "bad/key" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := input
			test.mutate(&got)
			if _, err := IntentSecretRelativePath(got); err == nil {
				t.Fatal("expected invalid path input to fail")
			}
		})
	}
}
