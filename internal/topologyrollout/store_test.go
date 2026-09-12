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
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestStoreReadsAndMutatesUIDBoundLedger(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	ledger, err := NewLedger("ledger-uid")
	if err != nil {
		t.Fatal(err)
	}
	data, err := Encode(ledger, 0)
	if err != nil {
		t.Fatal(err)
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "system", Name: "fleet-ledger", UID: types.UID("ledger-uid"), ResourceVersion: "1"},
		Data:       map[string]string{LedgerDataKey: string(data)},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
	store := Store{Client: client, APIReader: client, Key: types.NamespacedName{Namespace: cm.Namespace, Name: cm.Name}, ExpectedUID: cm.UID}

	if err := store.Mutate(ctx, func(current *Ledger) error {
		current.Reservations["r1"] = Reservation{ReservationRequest: ReservationRequest{
			ID: "r1", CampaignUID: "campaign-uid", PlanHash: "sha256:" + strings.Repeat("a", 64),
			PolicyUID: "policy-uid", PolicyVersion: "1", PolicyEpoch: 1, TopologyLockID: strings.Repeat("1", 32), PhysicalID: "serial-1",
			DeviceUID: "device-uid", NodeUID: "node-uid", ChildNamespace: "devices", ChildName: "upgrade-r1",
			Domains: map[string]string{"topology.cisco.vk/site": "site-a"},
		}, State: ReservationReserved}
		return nil
	}); err != nil {
		t.Fatalf("Mutate() error = %v", err)
	}
	_, got, err := store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reservations["r1"].State != ReservationReserved {
		t.Fatalf("stored ledger = %+v", got)
	}
	if err := store.Mutate(ctx, func(current *Ledger) error {
		current.UID = "substituted-ledger-uid"
		return nil
	}); !errors.Is(err, ErrLedgerIdentity) {
		t.Fatalf("Mutate() changed-UID error = %v, want ErrLedgerIdentity", err)
	}
}

func TestStoreFailsClosedForMissingOrRecreatedLedger(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	missing := Store{Client: client, Key: types.NamespacedName{Namespace: "system", Name: "fleet-ledger"}, ExpectedUID: "old-uid"}
	if _, _, err := missing.Read(ctx); err == nil {
		t.Fatal("Read() accepted missing ledger")
	}

	ledger, err := NewLedger("new-uid")
	if err != nil {
		t.Fatal(err)
	}
	data, err := Encode(ledger, 0)
	if err != nil {
		t.Fatal(err)
	}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "system", Name: "fleet-ledger", UID: "new-uid"}, Data: map[string]string{LedgerDataKey: string(data)}}
	if err := client.Create(ctx, cm); err != nil {
		t.Fatal(err)
	}
	if _, _, err := missing.Read(ctx); !errors.Is(err, ErrLedgerIdentity) {
		t.Fatalf("Read() recreated ledger error = %v, want ErrLedgerIdentity", err)
	}
}

func TestStoreRejectsNilMutation(t *testing.T) {
	if err := (Store{}).Mutate(context.Background(), nil); err == nil {
		t.Fatal("Mutate(nil) succeeded")
	}
}

func TestReleaseFenceConflictsInFlightReserveAndForcesAuthorizationRetry(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	ledger, err := NewLedger("ledger-uid")
	if err != nil {
		t.Fatal(err)
	}
	data, err := Encode(ledger, 0)
	if err != nil {
		t.Fatal(err)
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "system", Name: "fleet-ledger", UID: "ledger-uid", ResourceVersion: "1"},
		Data:       map[string]string{LedgerDataKey: string(data)},
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
	store := Store{Client: apiClient, APIReader: apiClient,
		Key: types.NamespacedName{Namespace: cm.Namespace, Name: cm.Name}, ExpectedUID: cm.UID}
	const lockID = "11111111111111111111111111111111"
	request := ReservationRequest{
		ID: "reservation-a", CampaignUID: "campaign-uid", PlanHash: "sha256:" + strings.Repeat("a", 64),
		PolicyUID: "policy-uid", PolicyVersion: "1", PolicyEpoch: 1, TopologyLockID: lockID,
		PhysicalID: "serial-a", DeviceUID: "device-a", NodeUID: "node-a",
		ChildNamespace: "devices", ChildName: "upgrade-a",
		Domains: map[string]string{"topology.cisco.vk/site": "site-a"},
	}
	reservePrepared := make(chan struct{})
	releaseCommitted := make(chan struct{})
	reserveDone := make(chan error, 1)
	errLockReleasing := errors.New("device topology lock is Releasing")
	attempts := 0
	go func() {
		reserveDone <- store.Mutate(ctx, func(current *Ledger) error {
			attempts++
			if attempts > 1 {
				// A real rollout callback reaches this result by re-reading the
				// CiscoDevice after the release fence wins the ledger CAS.
				return errLockReleasing
			}
			current.Reservations[request.ID] = Reservation{
				ReservationRequest: request, State: ReservationReserved,
			}
			close(reservePrepared)
			<-releaseCommitted
			return nil
		})
	}()
	<-reservePrepared
	if err := store.Mutate(ctx, func(current *Ledger) error {
		return FenceUnboundAcquisition(current, request.ID, request.PolicyEpoch, request.TopologyLockID)
	}); err != nil {
		t.Fatalf("commit release fence: %v", err)
	}
	close(releaseCommitted)
	if err := <-reserveDone; !errors.Is(err, errLockReleasing) {
		t.Fatalf("in-flight Reserve result=%v, want lock revalidation failure", err)
	}
	if attempts < 2 {
		t.Fatalf("Reserve callback ran %d time(s), want a retry after the fence CAS", attempts)
	}
	_, current, err := store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Reservations) != 0 || !HasReleaseFence(current, request.ID, request.PolicyEpoch, request.TopologyLockID) {
		t.Fatalf("post-race ledger=%#v, want exact release fence and no reservation", current)
	}
}
