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

package mapper

import (
	"testing"
	"time"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

func TestMappedEventsDoNotCarryResourceIdentityAttributes(t *testing.T) {
	notif := &gpb.Notification{
		Prefix: &gpb.Path{Origin: "rfc7951"},
		Update: []*gpb.Update{
			stringUpdate(&gpb.Path{Elem: []*gpb.PathElem{
				{Name: "app-hosting-oper-data"},
				{Name: "app", Key: map[string]string{
					"cisco_device_name": "edge-01",
					"name":              "cvk0000",
					"owner":             "platform",
				}},
				{Name: "details"}, {Name: "state"},
			}}, "DEPLOYED"),
			uintUpdate(&gpb.Path{Elem: []*gpb.PathElem{
				{Name: "app-hosting-oper-data"},
				{Name: "app", Key: map[string]string{
					"cisco_device_name": "edge-01",
					"name":              "cvk0000",
					"owner":             "platform",
				}},
				{Name: "details"}, {Name: "resource-reservation"}, {Name: "cpu"},
			}}, 1480),
		},
	}

	events := New().Process(notif, EventContext{
		Device:       "edge-01",
		Subscription: "app-hosting",
		Mapping: &configv1alpha1.MappingConfig{
			ResourceAttributes: []configv1alpha1.ResourceAttribute{
				{Path: "/app-hosting-oper-data/app/details/state", Key: "service.name"},
			},
			Transitions: []configv1alpha1.Transition{{
				Path:            "/app-hosting-oper-data/app/details/state",
				HealthyValues:   []string{"DEPLOYED"},
				UnhealthyValues: []string{"FAILED"},
			}},
		},
		Output:      configv1alpha1.OutputConfig{},
		ReceiveTime: time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC),
		ResourceAttributes: map[string]string{
			"service.name":      "cisco-vk-telemetry",
			"cisco.device.name": "edge-01",
			"host.name":         "edge-01",
			"k8s.pod.uid":       "pod-uid",
		},
	})
	if len(events) == 0 {
		t.Fatal("Process() returned no events")
	}
	for _, event := range events {
		for _, attr := range event.Attributes {
			if IsForbiddenDataPointAttribute(attr.Key) {
				t.Fatalf("event %s signal=%s carries forbidden data-point attr %s=%q in %+v",
					event.Name, event.Signal, attr.Key, attr.Value, event.Attributes)
			}
		}
	}
}
