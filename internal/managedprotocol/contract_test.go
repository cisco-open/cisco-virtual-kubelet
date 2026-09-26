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

package managedprotocol

import "testing"

func TestNetworkObjectBindingMatches(t *testing.T) {
	owner := map[string]string{
		AnnotationManaged:               "true",
		AnnotationDeviceNamespace:       "network",
		AnnotationDeviceName:            "switch-b",
		AnnotationDeviceUID:             "device-b-uid",
		AnnotationNetworkWorkerUsername: "system:serviceaccount:network:network",
		AnnotationNetworkWorkerPodName:  "switch-b-network-pod",
		AnnotationNetworkWorkerPodUID:   "pod-b-uid",
	}
	child := CopyNetworkObjectBinding(owner)
	child["unrelated"] = "preserved"
	if !NetworkObjectBindingMatches(owner, child) {
		t.Fatal("complete matching binding was rejected")
	}

	for _, tc := range []struct {
		name   string
		mutate func(map[string]string, map[string]string)
	}{
		{
			name: "incomplete owner",
			mutate: func(owner, _ map[string]string) {
				delete(owner, AnnotationNetworkWorkerPodUID)
			},
		},
		{
			name: "unmanaged owner",
			mutate: func(owner, _ map[string]string) {
				owner[AnnotationManaged] = "false"
			},
		},
		{
			name: "different device",
			mutate: func(_, child map[string]string) {
				child[AnnotationDeviceUID] = "device-a-uid"
			},
		},
		{
			name: "missing child pod",
			mutate: func(_, child map[string]string) {
				delete(child, AnnotationNetworkWorkerPodUID)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotOwner := cloneAnnotations(owner)
			gotChild := cloneAnnotations(child)
			tc.mutate(gotOwner, gotChild)
			if NetworkObjectBindingMatches(gotOwner, gotChild) {
				t.Fatal("mismatched binding was accepted")
			}
		})
	}
}

func cloneAnnotations(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
