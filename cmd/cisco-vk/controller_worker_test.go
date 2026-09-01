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

package main

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	controlleradapter "github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
)

func TestLiveControllerIdentityRejectsStaleVolumeGeneration(t *testing.T) {
	controller := &ciskov1.NetworkController{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "primary",
			Namespace:  "campus",
			UID:        types.UID("primary-uid"),
			Generation: 2,
		},
		Spec: ciskov1.NetworkControllerSpec{
			Type:                "test-controller",
			Endpoint:            "https://controller.example.test",
			CredentialSecretRef: ciskov1.NetworkControllerSecretReference{Name: "credentials-v2"},
		},
	}
	bootstrap := controlleradapter.WorkerConfig{Type: "test-controller"}
	if err := validateLiveControllerIdentity(controller, bootstrap, "primary-uid", 2); err != nil {
		t.Fatalf("current generation rejected: %v", err)
	}
	if err := validateLiveControllerIdentity(controller, bootstrap, "primary-uid", 1); err == nil || !strings.Contains(err.Error(), "generation") {
		t.Fatalf("stale worker generation error=%v", err)
	}
}

func TestControllerWorkerCacheIsBoundToEndpointIdentity(t *testing.T) {
	bootstrap := controlleradapter.WorkerConfig{ControllerRef: controlleradapter.WorkerControllerReference{
		Namespace: "campus",
		Name:      "primary",
	}}
	options := controllerWorkerCacheOptions(bootstrap)
	if len(options.DefaultNamespaces) != 1 {
		t.Fatalf("worker namespace cache=%v", options.DefaultNamespaces)
	}
	if _, ok := options.DefaultNamespaces["campus"]; !ok {
		t.Fatalf("worker cache missing bound namespace: %v", options.DefaultNamespaces)
	}
	found := false
	for object, byObject := range options.ByObject {
		if _, ok := object.(*ciskov1.NetworkController); !ok {
			continue
		}
		found = true
		if !byObject.Field.Matches(fields.Set{"metadata.name": "primary"}) ||
			byObject.Field.Matches(fields.Set{"metadata.name": "peer"}) {
			t.Fatalf("NetworkController cache selector=%v", byObject.Field)
		}
	}
	if !found {
		t.Fatal("NetworkController cache has no endpoint-specific selector")
	}
}
