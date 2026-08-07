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
	"reflect"
	"testing"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestRequiredManagerCRDsIncludesControllerFoundation(t *testing.T) {
	for _, want := range []struct {
		name string
		gvr  schema.GroupVersionResource
	}{
		{name: "NetworkController", gvr: ciskov1.GroupVersion.WithResource("networkcontrollers")},
		{name: "NetworkControllerConfig", gvr: configv1alpha1.GroupVersion.WithResource("networkcontrollerconfigs")},
	} {
		found := false
		for _, resource := range requiredManagerCRDs {
			if resource == want.gvr {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("requiredManagerCRDs does not contain %s (%s)", want.name, want.gvr.String())
		}
	}
}

func TestPartitionMissingRequiredCRDsPreservesExistingReconcilersDuringScaffoldUpgrade(t *testing.T) {
	networkController := crdNameForGVR(ciskov1.GroupVersion.WithResource("networkcontrollers"))
	networkControllerConfig := crdNameForGVR(configv1alpha1.GroupVersion.WithResource("networkcontrollerconfigs"))
	blocking, foundation := partitionMissingRequiredCRDs([]string{
		"ciscodevices.cisco.vk",
		networkController,
		networkControllerConfig,
	})
	if !reflect.DeepEqual(blocking, []string{"ciscodevices.cisco.vk"}) {
		t.Fatalf("blocking CRDs=%v", blocking)
	}
	if !reflect.DeepEqual(foundation, []string{networkController, networkControllerConfig}) {
		t.Fatalf("controller foundation CRDs=%v", foundation)
	}
}
