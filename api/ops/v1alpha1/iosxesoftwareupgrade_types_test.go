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
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUpgradeImageSourceIntentJSON(t *testing.T) {
	validSHA := strings.Repeat("a", 64)
	tests := []struct {
		name   string
		source UpgradeImageSource
		want   string
	}{
		{
			name:   "preinstalled marker remains present",
			source: UpgradeImageSource{Preinstalled: &PreinstalledImageSource{}},
			want:   `{"preinstalled":{}}`,
		},
		{
			name: "device file carries authenticated path",
			source: UpgradeImageSource{DeviceFile: &DeviceFileImageSource{
				Path: "flash:cat9k.bin", SHA256: validSHA,
			}},
			want: `{"deviceFile":{"path":"flash:cat9k.bin","sha256":"` + validSHA + `"}}`,
		},
		{
			name:   "legacy local path remains wire compatible",
			source: UpgradeImageSource{LocalPath: "flash:cat9k.bin"},
			want:   `{"localPath":"flash:cat9k.bin"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.source)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("Marshal() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestUpgradeImageSourceDeepCopyDoesNotAliasIntent(t *testing.T) {
	original := &UpgradeImageSource{
		DeviceFile: &DeviceFileImageSource{
			Path:   "flash:cat9k.bin",
			SHA256: strings.Repeat("a", 64),
		},
	}
	copy := original.DeepCopy()
	copy.DeviceFile.Path = "bootflash:other.bin"

	if original.DeviceFile.Path != "flash:cat9k.bin" {
		t.Fatalf("DeepCopy() aliased DeviceFile: original path = %q", original.DeviceFile.Path)
	}
}

func TestIOSXESoftwareUpgradeLegacyWireFieldsRemainCompatible(t *testing.T) {
	legacy := []byte(`{"spec":{"resumePolicy":"Abort","maxRetries":7},"status":{"phase":"Cancelled","retryCount":3}}`)
	var upgrade IOSXESoftwareUpgrade
	if err := json.Unmarshal(legacy, &upgrade); err != nil {
		t.Fatalf("Unmarshal() legacy object error = %v", err)
	}
	if upgrade.Spec.ResumePolicy != "Abort" || upgrade.Spec.MaxRetries != 7 {
		t.Fatalf("legacy spec fields lost: %+v", upgrade.Spec)
	}
	if upgrade.Status.Phase != UpgradePhaseCancelled || upgrade.Status.RetryCount != 3 {
		t.Fatalf("legacy status fields lost: %+v", upgrade.Status)
	}

	roundTrip, err := json.Marshal(&upgrade)
	if err != nil {
		t.Fatalf("Marshal() legacy object error = %v", err)
	}
	for _, field := range []string{`"resumePolicy":"Abort"`, `"maxRetries":7`, `"phase":"Cancelled"`, `"retryCount":3`} {
		if !strings.Contains(string(roundTrip), field) {
			t.Fatalf("Marshal() = %s, want retained field %s", roundTrip, field)
		}
	}

	withoutLegacyFields, err := json.Marshal(IOSXESoftwareUpgradeSpec{})
	if err != nil {
		t.Fatalf("Marshal() empty spec error = %v", err)
	}
	if strings.Contains(string(withoutLegacyFields), "resumePolicy") || strings.Contains(string(withoutLegacyFields), "maxRetries") {
		t.Fatalf("empty spec unexpectedly emits deprecated fields: %s", withoutLegacyFields)
	}
}

func TestIOSXESoftwareUpgradeDrainSnapshotsAreOptionalAndAuditable(t *testing.T) {
	empty, err := json.Marshal(IOSXESoftwareUpgradeStatus{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(empty), "managerDrain") || strings.Contains(string(empty), "workerDrain") {
		t.Fatalf("default status activated drain fields: %s", empty)
	}

	now := metav1.NewTime(time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC))
	deadline := metav1.NewTime(now.Add(10 * time.Minute))
	status := IOSXESoftwareUpgradeStatus{
		ManagerDrain: &UpgradeManagerDrainStatus{
			ProtocolVersion:               ManagedDrainProtocolPDBV1,
			State:                         UpgradeManagerDrainEvicting,
			SessionToken:                  "11111111-1111-4111-8111-111111111111",
			ReservationID:                 "reservation-a",
			PolicyEpoch:                   3,
			ControlRevision:               7,
			NodeUID:                       "node-uid",
			NodeUnschedulableBefore:       false,
			MaintenanceTaintPresentBefore: false,
			StartedAt:                     now,
			DrainDeadline:                 deadline,
			UpdatedAt:                     now,
			Pods: []UpgradeDrainPodStatus{{
				Namespace: "apps", Name: "web-0", UID: "pod-uid",
				EligibilityHash: "sha256:" + strings.Repeat("a", 64),
				Controller: UpgradeDrainObjectReference{
					APIVersion: "apps/v1", Kind: "ReplicaSet", Namespace: "apps", Name: "web", UID: "rs-uid", Generation: 2,
				},
				PDBs: []UpgradeDrainPDBStatus{{
					UpgradeDrainObjectReference: UpgradeDrainObjectReference{
						APIVersion: "policy/v1", Kind: "PodDisruptionBudget", Namespace: "apps", Name: "web", UID: "pdb-uid", Generation: 4,
					},
					ObservedGeneration: 4, DisruptionsAllowed: 1, CurrentHealthy: 3, DesiredHealthy: 2, ExpectedPods: 3,
				}},
				TerminationGracePeriodSeconds: 60,
				Phase:                         UpgradeDrainPodEvictionRequested,
			}},
		},
		WorkerDrain: &UpgradeWorkerDrainStatus{
			ProtocolVersion:              ManagedDrainProtocolPDBV1,
			ObservedSessionToken:         "11111111-1111-4111-8111-111111111111",
			ObservedPolicyEpoch:          3,
			ObservedControlRevision:      7,
			ObservedWorkerConfigRevision: "sha256:" + strings.Repeat("b", 64),
			InventoryRevision:            9,
			InventoryObservedAt:          now,
			InventoryComplete:            true,
			RemainingAuthorizedPodUIDs:   []string{"pod-uid"},
			UpdatedAt:                    now,
		},
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, evidence := range []string{
		`"managerDrain"`, `"workerDrain"`, `"nodeUnschedulableBefore":false`,
		`"maintenanceTaintPresentBefore":false`, `"observedGeneration":4`,
		`"disruptionsAllowed":1`, `"currentHealthy":3`, `"desiredHealthy":2`, `"expectedPods":3`,
	} {
		if !strings.Contains(string(raw), evidence) {
			t.Fatalf("Marshal() = %s, missing audit evidence %s", raw, evidence)
		}
	}
	var roundTrip IOSXESoftwareUpgradeStatus
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.ManagerDrain == nil || len(roundTrip.ManagerDrain.Pods) != 1 ||
		len(roundTrip.ManagerDrain.Pods[0].PDBs) != 1 || roundTrip.WorkerDrain == nil {
		t.Fatalf("drain snapshot did not round trip: %+v", roundTrip)
	}
}
