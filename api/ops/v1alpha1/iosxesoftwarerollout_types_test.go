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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func rolloutFixture() *IOSXESoftwareRollout {
	now := metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())
	one := int32(1)
	hash := "sha256:" + strings.Repeat("a", 64)
	return &IOSXESoftwareRollout{
		TypeMeta: metav1.TypeMeta{APIVersion: GroupVersion.String(), Kind: "IOSXESoftwareRollout"},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "devices",
			Name:      "campus-upgrade",
		},
		Spec: IOSXESoftwareRolloutSpec{
			Plan: IOSXESoftwareRolloutPlan{
				RequestedBy: "alice@example.test",
				RequestedAt: now,
				Targets: IOSXESoftwareRolloutTargetSpec{
					Selector:   IOSXESoftwareRolloutLabelSelector{MatchLabels: map[string]string{"role": "access"}},
					MaxTargets: 10,
				},
				Source: IOSXESoftwareRolloutSourceSpec{
					URL:          "sftp://images.example.test/cat9k.bin",
					SHA256:       strings.Repeat("b", 64),
					ImageFamily:  "cat9k",
					URLSecretRef: &corev1.LocalObjectReference{Name: "image-source"},
				},
				TargetVersion:         "17.18.4",
				Strategy:              IOSXESoftwareRolloutStrategyReload,
				InstallTimeoutSeconds: 3600,
				RebootTimeoutSeconds:  1800,
				Canaries: []IOSXESoftwareRolloutCanaryCohort{{
					Name: "c9300", Devices: []string{"edge-01"},
				}},
				Budgets: IOSXESoftwareRolloutBudgetSpec{
					MaxConcurrentTransfers: 2,
					MaxUnavailable:         1,
					Domains: []IOSXESoftwareRolloutDomainBudget{{
						TopologyKey:            "topology.cisco.vk/redundancy-group",
						MaxConcurrentTransfers: &one,
						MaxUnavailable:         &one,
					}},
				},
				Workloads: IOSXESoftwareRolloutWorkloadSpec{Policy: IOSXESoftwareRolloutWorkloadBlockIfRunning},
				Health: IOSXESoftwareRolloutHealthSpec{
					MaxObservationAgeSeconds: 300,
					CanarySoakSeconds:        600,
					WaveSoakSeconds:          300,
				},
			},
			Approval: &IOSXESoftwareRolloutApproval{
				PlanHash: hash, ApprovedBy: "bob@example.test", ApprovedAt: now,
			},
			Control: IOSXESoftwareRolloutControl{Revision: 0},
		},
		Status: IOSXESoftwareRolloutStatus{
			Phase: IOSXESoftwareRolloutPhaseAwaitingApproval,
			FrozenPlan: &IOSXESoftwareRolloutFrozenPlanStatus{
				Hash:               hash,
				EncodedSizeBytes:   2048,
				CreatedAt:          now,
				CampaignGeneration: 1,
				Policy: IOSXESoftwareRolloutPolicySnapshot{
					Name:                   "topology-rollout-policy",
					Namespace:              "cvk-system",
					UID:                    "policy-uid",
					ResourceVersion:        "20",
					MaxTargets:             100,
					MaxConcurrentTransfers: 4,
					MaxUnavailable:         1,
					Domains: []IOSXESoftwareRolloutDomainBudget{{
						TopologyKey: "topology.cisco.vk/redundancy-group", MaxConcurrentTransfers: &one, MaxUnavailable: &one,
					}},
					HealthFreshnessSeconds: 300,
					LedgerNamespace:        "cvk-system",
					LedgerName:             "topology-rollout-ledger",
					LedgerUID:              "ledger-uid",
					MaxActiveReservations:  256,
					MaxLedgerSizeBytes:     256 * 1024,
				},
				Source: IOSXESoftwareRolloutSourceSnapshot{
					URL: "sftp://images.example.test/cat9k.bin", SHA256: strings.Repeat("b", 64), SecretName: "image-source", SecretUID: "secret-uid",
				},
				Targets: []IOSXESoftwareRolloutPlannedTarget{{
					DeviceName:            "edge-01",
					DeviceUID:             "device-uid",
					DeviceGeneration:      7,
					PhysicalIdentity:      "FCW00000001",
					NodeName:              "edge-01",
					NodeUID:               "node-uid",
					Driver:                "XE",
					ImageFamily:           "cat9k",
					QualificationCohort:   "c9300",
					WorkerProtocolVersion: string(ManagedUpgradeProtocolRolloutV1),
					ProjectionHash:        hash,
					Topology: []IOSXESoftwareRolloutTopologyValue{{
						Key: "topology.cisco.vk/site", Value: "berlin",
					}},
					CanaryCohort: "c9300",
					Wave:         0,
					ChildName:    "campus-upgrade-edge-01",
				}},
			},
			Control: &IOSXESoftwareRolloutControlStatus{RequestedRevision: 0, EffectiveRevision: 0},
			Targets: []IOSXESoftwareRolloutTargetStatus{{
				DeviceName: "edge-01", DeviceUID: "device-uid", Phase: IOSXESoftwareRolloutTargetPlanned, LastTransitionTime: now,
			}},
		},
	}
}

func TestIOSXESoftwareRolloutSchemeRegistration(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	for _, kind := range []string{"IOSXESoftwareRollout", "IOSXESoftwareRolloutList"} {
		gvk := schema.GroupVersionKind{Group: GroupVersion.Group, Version: GroupVersion.Version, Kind: kind}
		if _, err := scheme.New(gvk); err != nil {
			t.Fatalf("scheme.New(%s) error = %v", gvk, err)
		}
	}
}

func TestIOSXESoftwareRolloutDeepCopyDoesNotAlias(t *testing.T) {
	original := rolloutFixture()
	copy := original.DeepCopy()

	copy.Spec.Plan.Targets.Selector.MatchLabels["role"] = "distribution"
	copy.Spec.Plan.Canaries[0].Devices[0] = "edge-02"
	*copy.Spec.Plan.Budgets.Domains[0].MaxUnavailable = 2
	copy.Spec.Plan.Source.URLSecretRef.Name = "other-source"
	copy.Status.FrozenPlan.Targets[0].Topology[0].Value = "munich"
	*copy.Status.FrozenPlan.Policy.Domains[0].MaxUnavailable = 2
	copy.Status.Targets[0].Message = "changed"

	if original.Spec.Plan.Targets.Selector.MatchLabels["role"] != "access" {
		t.Fatal("DeepCopy() aliased target selector")
	}
	if original.Spec.Plan.Canaries[0].Devices[0] != "edge-01" {
		t.Fatal("DeepCopy() aliased canary devices")
	}
	if *original.Spec.Plan.Budgets.Domains[0].MaxUnavailable != 1 {
		t.Fatal("DeepCopy() aliased domain budget pointer")
	}
	if original.Spec.Plan.Source.URLSecretRef.Name != "image-source" {
		t.Fatal("DeepCopy() aliased source Secret reference")
	}
	if original.Status.FrozenPlan.Targets[0].Topology[0].Value != "berlin" {
		t.Fatal("DeepCopy() aliased frozen topology")
	}
	if *original.Status.FrozenPlan.Policy.Domains[0].MaxUnavailable != 1 {
		t.Fatal("DeepCopy() aliased frozen policy domain budget")
	}
}

func TestIOSXESoftwareRolloutLabelSelectorConversionDoesNotAlias(t *testing.T) {
	selector := IOSXESoftwareRolloutLabelSelector{
		MatchLabels: map[string]string{"role": "access"},
		MatchExpressions: []IOSXESoftwareRolloutLabelSelectorRequirement{{
			Key: "topology.cisco.vk/site", Operator: metav1.LabelSelectorOpIn, Values: []string{"berlin"},
		}},
	}
	converted := selector.AsLabelSelector()
	converted.MatchLabels["role"] = "distribution"
	converted.MatchExpressions[0].Values[0] = "munich"

	if selector.MatchLabels["role"] != "access" || selector.MatchExpressions[0].Values[0] != "berlin" {
		t.Fatal("AsLabelSelector() returned aliased selector storage")
	}
}

func TestIOSXESoftwareRolloutWireContractUsesOneReloadSource(t *testing.T) {
	raw, err := json.Marshal(rolloutFixture())
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	text := string(raw)
	for _, field := range []string{
		`"source":{"url":"sftp://images.example.test/cat9k.bin"`,
		`"strategy":"Reload"`,
		`"installTimeoutSeconds":3600`,
		`"rebootTimeoutSeconds":1800`,
		`"approval":{"planHash":"sha256:`,
		`"control":{"revision":0}`,
		`"frozenPlan"`,
		`"maxConcurrentTransfers":4`,
		`"healthFreshnessSeconds":300`,
		`"ledgerUID":"ledger-uid"`,
		`"deviceGeneration":7`,
		`"qualificationCohort":"c9300"`,
	} {
		if !strings.Contains(text, field) {
			t.Fatalf("Marshal() = %s, missing %s", text, field)
		}
	}
	if strings.Contains(text, `"sources"`) || strings.Contains(text, `"mirrors"`) {
		t.Fatalf("Phase 2 wire contract unexpectedly exposes source lists: %s", text)
	}
}

func TestManagedUpgradeAdmissionAndClaimsDeepCopyDoNotAlias(t *testing.T) {
	now := metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())
	revision := int64(3)
	original := &IOSXESoftwareUpgrade{Status: IOSXESoftwareUpgradeStatus{
		ManagerAdmission: &UpgradeManagerAdmissionStatus{
			ProtocolVersion:       ManagedUpgradeProtocolRolloutV1,
			State:                 UpgradeManagerAdmissionGranted,
			CampaignUID:           "campaign-uid",
			PlanHash:              "sha256:" + strings.Repeat("a", 64),
			PolicyUID:             "policy-uid",
			PolicyResourceVersion: "20",
			LedgerUID:             "ledger-uid",
			ReservationID:         "reservation-1",
			LeafUID:               "leaf-uid",
			DeviceUID:             "device-uid",
			DeviceGeneration:      7,
			PhysicalIdentity:      "FCW00000001",
			NodeUID:               "node-uid",
			ControlRevision:       &revision,
			UpdatedAt:             now,
		},
		ManagerControl: &UpgradeManagerControlStatus{Revision: 3, UpdatedAt: now},
		WorkerControl: &UpgradeWorkerControlStatus{
			ObservedAdmissionState:  UpgradeManagerAdmissionGranted,
			ObservedControlRevision: 3,
			EffectiveState:          UpgradeWorkerControlReady,
			UpdatedAt:               now,
		},
		ManagedMutationClaims: []UpgradeManagedMutationClaimStatus{{
			Stage: UpgradeManagedMutationPrimaryInstall, ReservationID: "reservation-1", ControlRevision: 3, ClaimedAt: now,
		}},
	}}

	copy := original.DeepCopy()
	*copy.Status.ManagerAdmission.ControlRevision = 4
	copy.Status.ManagedMutationClaims[0].ReservationID = "reservation-2"
	copy.Status.WorkerControl.Message = "different"

	if *original.Status.ManagerAdmission.ControlRevision != 3 {
		t.Fatal("DeepCopy() aliased manager admission control revision")
	}
	if original.Status.ManagedMutationClaims[0].ReservationID != "reservation-1" {
		t.Fatal("DeepCopy() aliased managed mutation claims")
	}
}

func TestIOSXESoftwareRolloutHardLimits(t *testing.T) {
	if MaxIOSXESoftwareRolloutTargets != 100 {
		t.Fatalf("target cap = %d, want 100", MaxIOSXESoftwareRolloutTargets)
	}
	if MaxIOSXESoftwareRolloutPlanBytes != 256*1024 {
		t.Fatalf("plan cap = %d, want 256 KiB", MaxIOSXESoftwareRolloutPlanBytes)
	}
	if MaxIOSXESoftwareRolloutTopologyValues != 16 {
		t.Fatalf("topology value cap = %d, want 16", MaxIOSXESoftwareRolloutTopologyValues)
	}
}
