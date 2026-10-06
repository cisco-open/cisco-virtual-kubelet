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

package topologyrollout

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type e12ScaleProfile struct {
	SchemaVersion                 string `json:"schemaVersion"`
	FleetMembers                  int    `json:"fleetMembers"`
	CampaignTargets               []int  `json:"campaignTargets"`
	MaxActiveReservations         int    `json:"maxActiveReservations"`
	MaxLedgerBytes                int    `json:"maxLedgerBytes"`
	ReconcileLatencyBudgetSeconds struct {
		P50 float64 `json:"p50"`
		P95 float64 `json:"p95"`
		P99 float64 `json:"p99"`
	} `json:"reconcileLatencyBudgetSeconds"`
	MaxManagerResidentBytes             int64 `json:"maxManagerResidentBytes"`
	MaxAPIRequestsPerReconcile          int   `json:"maxAPIRequestsPerReconcile"`
	MaxLedgerConflictRetriesPerMutation int   `json:"maxLedgerConflictRetriesPerMutation"`
	MaxControllerWatchKinds             int   `json:"maxControllerWatchKinds"`
}

func readE12ScaleProfile(tb testing.TB) e12ScaleProfile {
	tb.Helper()
	raw, err := os.ReadFile("testdata/e12-scale-profile-v1.json")
	if err != nil {
		tb.Fatal(err)
	}
	var profile e12ScaleProfile
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&profile); err != nil {
		tb.Fatalf("decode E12 scale profile: %v", err)
	}
	return profile
}

func TestE12ScaleProfileMatchesHardSafetyEnvelope(t *testing.T) {
	t.Parallel()
	profile := readE12ScaleProfile(t)
	if profile.SchemaVersion != "cisco.vk/e12-scale-profile/v1" {
		t.Fatalf("schemaVersion = %q", profile.SchemaVersion)
	}
	if profile.FleetMembers != 1000 || !reflect.DeepEqual(profile.CampaignTargets, []int{1, 10, 50, 100}) {
		t.Fatalf("fleet/target matrix = %d/%v", profile.FleetMembers, profile.CampaignTargets)
	}
	if profile.MaxActiveReservations != DefaultMaxActiveRecords || profile.MaxLedgerBytes != DefaultMaxSerializedBytes {
		t.Fatalf("profile ledger limits = %d/%d, hard limits = %d/%d",
			profile.MaxActiveReservations, profile.MaxLedgerBytes, DefaultMaxActiveRecords, DefaultMaxSerializedBytes)
	}
	if profile.ReconcileLatencyBudgetSeconds.P50 <= 0 ||
		profile.ReconcileLatencyBudgetSeconds.P50 > profile.ReconcileLatencyBudgetSeconds.P95 ||
		profile.ReconcileLatencyBudgetSeconds.P95 > profile.ReconcileLatencyBudgetSeconds.P99 ||
		profile.MaxManagerResidentBytes <= 0 || profile.MaxAPIRequestsPerReconcile <= 0 ||
		profile.MaxLedgerConflictRetriesPerMutation <= 0 || profile.MaxControllerWatchKinds <= 0 {
		t.Fatalf("profile budgets are incomplete: %+v", profile)
	}
}

func BenchmarkE12ScaleReserveEncodeDecode(b *testing.B) {
	profile := readE12ScaleProfile(b)
	members := e12ScaleMembers(profile.FleetMembers)
	policy := e12ScalePolicy(profile)
	for _, targets := range profile.CampaignTargets {
		b.Run(fmt.Sprintf("targets-%03d", targets), func(b *testing.B) {
			b.ReportAllocs()
			var encodedBytes int
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				ledger, err := NewLedger("ledger-uid")
				if err != nil {
					b.Fatal(err)
				}
				for target := 0; target < targets; target++ {
					request := e12ScaleRequest(target, members[target], targets)
					if err := Reserve(ledger, policy, members, request); err != nil {
						b.Fatalf("reserve target %d: %v", target, err)
					}
				}
				encoded, err := Encode(ledger, profile.MaxLedgerBytes)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := Decode(encoded, ledger.UID); err != nil {
					b.Fatal(err)
				}
				encodedBytes = len(encoded)
			}
			b.StopTimer()
			b.ReportMetric(float64(targets), "targets")
			b.ReportMetric(float64(profile.FleetMembers), "fleet-members")
			b.ReportMetric(float64(encodedBytes), "ledger-bytes")
		})
	}
}

func BenchmarkE12ScaleLedgerAPIRead(b *testing.B) {
	profile := readE12ScaleProfile(b)
	for _, targets := range profile.CampaignTargets {
		b.Run(fmt.Sprintf("targets-%03d", targets), func(b *testing.B) {
			ledger, err := NewLedger("ledger-uid")
			if err != nil {
				b.Fatal(err)
			}
			for target := 0; target < targets; target++ {
				request := e12ScaleRequest(target, e12ScaleMembers(targets)[target], targets)
				ledger.Reservations[request.ID] = Reservation{ReservationRequest: request, State: ReservationReserved}
			}
			encoded, err := Encode(ledger, profile.MaxLedgerBytes)
			if err != nil {
				b.Fatal(err)
			}
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				b.Fatal(err)
			}
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Namespace: "system", Name: "fleet-ledger", UID: types.UID(ledger.UID), ResourceVersion: "1",
			}, Data: map[string]string{LedgerDataKey: string(encoded)}}
			apiClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
			store := Store{Client: apiClient, APIReader: apiClient,
				Key: types.NamespacedName{Namespace: cm.Namespace, Name: cm.Name}, ExpectedUID: cm.UID}

			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				if _, _, err := store.Read(context.Background()); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(targets), "targets")
			b.ReportMetric(float64(len(encoded)), "ledger-bytes")
		})
	}
}

func e12ScaleMembers(count int) []Member {
	now := time.Date(2026, time.October, 2, 8, 0, 0, 0, time.UTC)
	members := make([]Member, 0, count)
	for index := 0; index < count; index++ {
		members = append(members, Member{
			PhysicalID: fmt.Sprintf("serial-%04d", index),
			DeviceUID:  fmt.Sprintf("device-uid-%04d", index),
			NodeUID:    fmt.Sprintf("node-uid-%04d", index),
			Domains: map[string]string{
				"topology.cisco.vk/site": fmt.Sprintf("site-%02d", index%10),
			},
			HealthKnown: true, Healthy: true, HealthObserved: now,
		})
	}
	return members
}

func e12ScalePolicy(profile e12ScaleProfile) Policy {
	return Policy{
		UID: "policy-uid", Version: "1", Epoch: 1,
		GlobalMaxConcurrentTransfers: profile.MaxActiveReservations,
		GlobalMaxUnavailable:         profile.MaxActiveReservations,
		DomainTransferBudgets:        map[string]int{"topology.cisco.vk/site": profile.MaxActiveReservations},
		DomainBudgets:                map[string]int{"topology.cisco.vk/site": profile.MaxActiveReservations},
		MaxActiveRecords:             profile.MaxActiveReservations,
		MaxSerializedBytes:           profile.MaxLedgerBytes,
		RequiredHealthFreshBy:        time.Date(2026, time.October, 2, 7, 59, 0, 0, time.UTC),
	}
}

func e12ScaleRequest(index int, member Member, campaignTargets int) ReservationRequest {
	return ReservationRequest{
		ID: fmt.Sprintf("reservation-%03d", index), CampaignUID: "campaign-uid",
		PlanHash:  "sha256:" + strings.Repeat("a", 64),
		PolicyUID: "policy-uid", PolicyVersion: "1", PolicyEpoch: 1,
		TopologyLockID: fmt.Sprintf("%032x", index+1),
		PhysicalID:     member.PhysicalID, DeviceUID: member.DeviceUID, NodeUID: member.NodeUID,
		ChildNamespace: "devices", ChildName: fmt.Sprintf("upgrade-%03d", index),
		Domains: member.Domains, CampaignMaxConcurrentTransfers: campaignTargets,
		CampaignMaxUnavailable: campaignTargets,
	}
}
