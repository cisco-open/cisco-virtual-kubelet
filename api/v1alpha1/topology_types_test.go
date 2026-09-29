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

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

func TestCiscoDeviceNodeIdentityWireContract(t *testing.T) {
	now := metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())
	device := CiscoDevice{
		Spec: DeviceSpec{NodeName: "berlin-edge-01", PhysicalIdentity: "FOC2416U0MV"},
		Status: DeviceStatus{
			NodeIdentity: &DeviceNodeIdentityStatus{
				NodeName:         "berlin-edge-01",
				NodeUID:          "node-uid",
				DeviceUID:        "device-uid",
				PhysicalIdentity: "foc2416u0mv",
			},
			TopologyProjection: &DeviceTopologyProjectionStatus{
				EffectiveLabelHash:    "sha256:" + strings.Repeat("a", 64),
				SourceResourceVersion: "42",
				LastSuccessfulTime:    now,
			},
		},
	}

	raw, err := json.Marshal(&device)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, field := range []string{
		`"nodeName":"berlin-edge-01"`,
		`"physicalIdentity":"FOC2416U0MV"`,
		`"nodeIdentity"`,
		`"physicalIdentity":"foc2416u0mv"`,
		`"topologyProjection"`,
		`"effectiveLabelHash":"sha256:`,
		`"sourceResourceVersion":"42"`,
	} {
		if !strings.Contains(string(raw), field) {
			t.Fatalf("Marshal() = %s, missing %s", raw, field)
		}
	}
}

func TestCiscoDeviceTopologyAndMaintenanceDeepCopyDoNotAlias(t *testing.T) {
	requested := metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())
	acknowledged := metav1.NewTime(time.Unix(1_700_000_100, 0).UTC())
	original := &CiscoDevice{
		Status: DeviceStatus{
			NodeIdentity: &DeviceNodeIdentityStatus{
				NodeName:         "edge-01",
				NodeUID:          "node-uid",
				DeviceUID:        "device-uid",
				PhysicalIdentity: "serial-edge-01",
			},
			TopologyProjection: &DeviceTopologyProjectionStatus{
				EffectiveLabelHash:    "sha256:" + strings.Repeat("a", 64),
				SourceResourceVersion: "10",
				LastSuccessfulTime:    requested,
			},
			MaintenanceSession: &DeviceMaintenanceSessionStatus{
				Phase:        DeviceMaintenanceSessionActive,
				SessionToken: "session-token-0001",
				Lease: DeviceMaintenanceLeaseReference{
					DeviceMaintenanceObjectReference: DeviceMaintenanceObjectReference{
						Namespace: "devices", Name: "edge-01-mutation", UID: "lease-uid",
					},
					Holder: "worker-edge-01",
				},
				Operation: DeviceMaintenanceObjectReference{
					Namespace: "devices", Name: "upgrade-edge-01", UID: "operation-uid",
				},
				DeviceUID:       "device-uid",
				NodeName:        "edge-01",
				NodeUID:         "node-uid",
				RequestedAt:     requested,
				AcknowledgedAt:  &acknowledged,
				ControlRevision: 7,
			},
		},
	}

	copy := original.DeepCopy()
	copy.Status.NodeIdentity.NodeUID = "different-node"
	copy.Status.TopologyProjection.SourceResourceVersion = "11"
	copy.Status.MaintenanceSession.AcknowledgedAt.Time = copy.Status.MaintenanceSession.AcknowledgedAt.Add(time.Minute)
	copy.Status.MaintenanceSession.Lease.Holder = "different-worker"

	if original.Status.NodeIdentity.NodeUID != "node-uid" {
		t.Fatal("DeepCopy() aliased NodeIdentity")
	}
	if original.Status.TopologyProjection.SourceResourceVersion != "10" {
		t.Fatal("DeepCopy() aliased TopologyProjection")
	}
	if original.Status.MaintenanceSession.AcknowledgedAt.Equal(copy.Status.MaintenanceSession.AcknowledgedAt) {
		t.Fatal("DeepCopy() aliased MaintenanceSession.AcknowledgedAt")
	}
	if original.Status.MaintenanceSession.Lease.Holder != "worker-edge-01" {
		t.Fatal("DeepCopy() aliased MaintenanceSession.Lease")
	}
}

func TestCiscoDeviceOmittedNodeNameRemainsOmitted(t *testing.T) {
	raw, err := json.Marshal(DeviceSpec{})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(raw), "nodeName") {
		t.Fatalf("omitted nodeName was defaulted client-side: %s", raw)
	}
	if strings.Contains(string(raw), "physicalIdentity") {
		t.Fatalf("omitted physicalIdentity was defaulted client-side: %s", raw)
	}
}

func TestDeviceWorkerRevisionPendingFenceOmitsUnprovenRuntimeIdentity(t *testing.T) {
	status := DeviceWorkerRevisionStatus{
		DesiredRevision: "sha256:" + strings.Repeat("a", 64),
		ObservedAt:      metav1.NewTime(time.Unix(1_700_000_000, 0).UTC()),
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"deploymentUID", "deploymentGeneration", "observedRevision", "podUID", "podStartTime", "readyHeartbeatTime"} {
		if strings.Contains(string(raw), absent) {
			t.Fatalf("pending worker fence serialized unproven %s: %s", absent, raw)
		}
	}
}

func TestCiscoDeviceNodeNameCRDBoundaryMatchesHostnameLabel(t *testing.T) {
	path := filepath.Join("..", "..", "config", "crd", "cisco.vk_ciscodevices.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated CiscoDevice CRD: %v", err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatalf("parse generated CiscoDevice CRD: %v", err)
	}
	if len(crd.Spec.Versions) != 1 || crd.Spec.Versions[0].Schema == nil || crd.Spec.Versions[0].Schema.OpenAPIV3Schema == nil {
		t.Fatal("CiscoDevice CRD has no v1alpha1 OpenAPI schema")
	}
	specSchema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
	nodeNameSchema, ok := specSchema.Properties["nodeName"]
	if !ok {
		t.Fatal("CiscoDevice CRD has no spec.nodeName schema")
	}
	if nodeNameSchema.MaxLength == nil || *nodeNameSchema.MaxLength != 63 {
		t.Fatalf("spec.nodeName maxLength = %v, want 63", nodeNameSchema.MaxLength)
	}
	pattern, err := regexp.Compile(nodeNameSchema.Pattern)
	if err != nil {
		t.Fatalf("compile generated nodeName pattern: %v", err)
	}

	boundary := strings.Repeat("a", 63)
	if len(boundary) != 63 || !pattern.MatchString(boundary) {
		t.Fatalf("63-byte Node/hostname label name rejected: length=%d pattern=%q", len(boundary), nodeNameSchema.Pattern)
	}
	tooLong := boundary + "e"
	if int64(len(tooLong)) <= *nodeNameSchema.MaxLength {
		t.Fatalf("boundary fixture length = %d, want over maxLength", len(tooLong))
	}
}
