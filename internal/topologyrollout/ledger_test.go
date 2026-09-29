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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestReserveCountsUnhealthyNonTargetAndAllDimensions(t *testing.T) {
	policy := testPolicy()
	ledger := testLedger(t)
	members := []Member{
		testMember("serial-a", "device-a", "node-a", "site-a", "pair-a", true),
		testMember("serial-b", "device-b", "node-b", "site-a", "pair-a", false),
		testMember("serial-c", "device-c", "node-c", "site-a", "pair-c", true),
	}

	err := Reserve(ledger, policy, members, testRequest("reservation-a", members[0]))
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("Reserve() error = %v, want ErrBudgetExceeded", err)
	}
	if len(ledger.Reservations) != 0 {
		t.Fatalf("failed multi-dimension admission mutated ledger: %+v", ledger.Reservations)
	}
}

func TestReserveDeduplicatesSamePhysicalObservationConservatively(t *testing.T) {
	policy := testPolicy()
	ledger := testLedger(t)
	target := testMember("serial-a", "device-a", "node-a", "site-a", "pair-a", true)
	duplicate := testMember("serial-b", "device-b", "node-b", "site-a", "pair-a", true)
	duplicateUnknown := duplicate
	duplicateUnknown.HealthKnown = false

	err := Reserve(ledger, policy, []Member{target, duplicate, duplicateUnknown}, testRequest("reservation-a", target))
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("Reserve() error = %v, want conservative duplicate health to exhaust pair budget", err)
	}
}

func TestReserveRejectsConflictingDuplicatePhysicalIdentity(t *testing.T) {
	policy := testPolicy()
	ledger := testLedger(t)
	target := testMember("serial-a", "device-a", "node-a", "site-a", "pair-a", true)
	duplicate := target
	duplicate.NodeUID = "other-node"

	err := Reserve(ledger, policy, []Member{target, duplicate}, testRequest("reservation-a", target))
	if err == nil || !strings.Contains(err.Error(), "conflicting membership") {
		t.Fatalf("Reserve() error = %v, want conflicting membership", err)
	}
}

func TestReserveRejectsStaleOrUnknownTarget(t *testing.T) {
	now := time.Now().UTC()
	policy := testPolicy()
	policy.RequiredHealthFreshBy = now.Add(-time.Minute)
	for _, mutate := range []func(*Member){
		func(member *Member) { member.HealthKnown = false },
		func(member *Member) { member.Healthy = false },
		func(member *Member) { member.Maintenance = true },
		func(member *Member) { member.HealthObserved = now.Add(-2 * time.Minute) },
	} {
		ledger := testLedger(t)
		target := testMember("serial-a", "device-a", "node-a", "site-a", "pair-a", true)
		target.HealthObserved = now
		mutate(&target)
		if err := Reserve(ledger, policy, []Member{target}, testRequest("reservation-a", target)); !errors.Is(err, ErrTargetUnavailable) {
			t.Fatalf("Reserve() error = %v, want ErrTargetUnavailable for %+v", err, target)
		}
	}
}

func TestReserveBindsTransferOnlyDomainToCurrentTarget(t *testing.T) {
	policy := testPolicy()
	delete(policy.DomainBudgets, "topology.cisco.vk/site")
	target := testMember("serial-a", "device-a", "node-a", "site-a", "pair-a", true)
	request := testRequest("reservation-a", target)
	request.Domains = cloneStringMap(request.Domains)
	request.Domains["topology.cisco.vk/site"] = "site-with-free-capacity"

	err := Reserve(testLedger(t), policy, []Member{target}, request)
	if err == nil || !strings.Contains(err.Error(), "invalid or stale domain") {
		t.Fatalf("Reserve() transfer-only domain error = %v, want stale-domain rejection", err)
	}
}

func TestThousandMemberFleetSupportsBoundedHundredTargetCampaign(t *testing.T) {
	const (
		fleetSize      = 1000
		campaignTarget = 100
		siteKey        = "topology.cisco.vk/site"
	)
	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	members := make([]Member, 0, fleetSize)
	for i := 0; i < fleetSize; i++ {
		members = append(members, Member{
			PhysicalID: fmt.Sprintf("serial-%04d", i),
			DeviceUID:  fmt.Sprintf("device-uid-%04d", i),
			NodeUID:    fmt.Sprintf("node-uid-%04d", i),
			Domains: map[string]string{
				siteKey: fmt.Sprintf("site-%02d", i%10),
			},
			HealthKnown: true, Healthy: true, HealthObserved: now,
		})
	}
	policy := Policy{
		UID: "policy-uid", Version: "42", Epoch: 1,
		GlobalMaxConcurrentTransfers: DefaultMaxActiveRecords,
		GlobalMaxUnavailable:         DefaultMaxActiveRecords,
		DomainTransferBudgets:        map[string]int{siteKey: DefaultMaxActiveRecords},
		DomainBudgets:                map[string]int{siteKey: DefaultMaxActiveRecords},
		MaxActiveRecords:             DefaultMaxActiveRecords,
		MaxSerializedBytes:           DefaultMaxSerializedBytes,
		RequiredHealthFreshBy:        now.Add(-time.Minute),
	}
	ledger := testLedger(t)
	for i := 0; i < campaignTarget; i++ {
		request := ReservationRequest{
			ID: fmt.Sprintf("reservation-%03d", i), CampaignUID: "campaign-uid",
			PlanHash: "sha256:" + strings.Repeat("a", 64), PolicyUID: policy.UID, PolicyVersion: policy.Version, PolicyEpoch: policy.Epoch,
			TopologyLockID: strings.Repeat("1", 32),
			PhysicalID:     members[i].PhysicalID, DeviceUID: members[i].DeviceUID, NodeUID: members[i].NodeUID,
			ChildNamespace: "default", ChildName: fmt.Sprintf("upgrade-%03d", i), Domains: members[i].Domains,
			CampaignMaxConcurrentTransfers: campaignTarget, CampaignMaxUnavailable: campaignTarget,
		}
		if err := Reserve(ledger, policy, members, request); err != nil {
			t.Fatalf("Reserve(target %d) error = %v", i, err)
		}
	}
	if len(ledger.Reservations) != campaignTarget {
		t.Fatalf("ledger records = %d, want %d", len(ledger.Reservations), campaignTarget)
	}
	encoded, err := Encode(ledger, DefaultMaxSerializedBytes)
	if err != nil {
		t.Fatalf("Encode(100-target ledger) error = %v", err)
	}
	if len(encoded) >= DefaultMaxSerializedBytes {
		t.Fatalf("encoded 100-target ledger = %d bytes, limit %d", len(encoded), DefaultMaxSerializedBytes)
	}
	if _, err := Decode(encoded, ledger.UID); err != nil {
		t.Fatalf("Decode(100-target ledger) error = %v", err)
	}

	overLimit := ReservationRequest{
		ID: "reservation-100", CampaignUID: "campaign-uid", PlanHash: "sha256:" + strings.Repeat("a", 64),
		PolicyUID: policy.UID, PolicyVersion: policy.Version, PolicyEpoch: policy.Epoch, TopologyLockID: strings.Repeat("2", 32),
		PhysicalID: members[campaignTarget].PhysicalID, DeviceUID: members[campaignTarget].DeviceUID, NodeUID: members[campaignTarget].NodeUID,
		ChildNamespace: "default", ChildName: "upgrade-100", Domains: members[campaignTarget].Domains,
		CampaignMaxConcurrentTransfers: campaignTarget, CampaignMaxUnavailable: campaignTarget,
	}
	if err := Reserve(ledger, policy, members, overLimit); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("Reserve(101st campaign target) error = %v, want ErrBudgetExceeded", err)
	}
	if len(ledger.Reservations) != campaignTarget {
		t.Fatalf("failed 101st admission changed ledger records to %d", len(ledger.Reservations))
	}
}

func TestReserveIsIdempotentButPhysicalDeviceCannotBeDoubleReserved(t *testing.T) {
	policy := testPolicy()
	policy.DomainBudgets["topology.cisco.vk/redundancy-group"] = 2
	ledger := testLedger(t)
	target := testMember("serial-a", "device-a", "node-a", "site-a", "pair-a", true)
	request := testRequest("reservation-a", target)
	if err := Reserve(ledger, policy, []Member{target}, request); err != nil {
		t.Fatalf("first Reserve() error = %v", err)
	}
	if err := Reserve(ledger, policy, []Member{target}, request); err != nil {
		t.Fatalf("idempotent Reserve() error = %v", err)
	}
	request.ID = "reservation-b"
	request.ChildName = "upgrade-b"
	if err := Reserve(ledger, policy, []Member{target}, request); !errors.Is(err, ErrAlreadyReserved) {
		t.Fatalf("second reservation error = %v, want ErrAlreadyReserved", err)
	}
}

func TestReleaseUnclaimedAtEpochCannotDeleteRearmedReservation(t *testing.T) {
	policy := testPolicy()
	policy.Epoch = 2
	target := testMember("serial-a", "device-a", "node-a", "site-a", "pair-a", true)
	request := testRequest("reservation-a", target)
	request.PolicyEpoch = policy.Epoch
	ledger := testLedger(t)
	if err := Reserve(ledger, policy, []Member{target}, request); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseUnclaimedAtEpoch(ledger, request.ID, request.TopologyLockID, "", 0, 1); !errors.Is(err, ErrStaleControlRevision) {
		t.Fatalf("stale epoch release error = %v, want ErrStaleControlRevision", err)
	}
	if _, exists := ledger.Reservations[request.ID]; !exists {
		t.Fatal("stale cleanup deleted a newer policy-epoch reservation")
	}
}

func TestReleaseUnclaimedCannotDeleteNewAcquisitionAtSamePolicyEpoch(t *testing.T) {
	policy := testPolicy()
	target := testMember("serial-a", "device-a", "node-a", "site-a", "pair-a", true)
	request := testRequest("reservation-a", target)
	request.TopologyLockID = strings.Repeat("2", 32)
	ledger := testLedger(t)
	if err := Reserve(ledger, policy, []Member{target}, request); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseUnclaimedAtEpoch(ledger, request.ID, strings.Repeat("1", 32), "", 0, policy.Epoch); !errors.Is(err, ErrStaleControlRevision) {
		t.Fatalf("stale acquisition release error = %v, want ErrStaleControlRevision", err)
	}
	if got := ledger.Reservations[request.ID].TopologyLockID; got != request.TopologyLockID {
		t.Fatalf("stale cleanup changed newer acquisition %q, want %q", got, request.TopologyLockID)
	}
}

func TestReleaseUnclaimedRequiresExactRevokedStateForBoundChild(t *testing.T) {
	policy := testPolicy()
	target := testMember("serial-a", "device-a", "node-a", "site-a", "pair-a", true)
	request := testRequest("reservation-a", target)
	ledger := testLedger(t)
	if err := Reserve(ledger, policy, []Member{target}, request); err != nil {
		t.Fatal(err)
	}
	if err := BindChild(ledger, request.ID, "leaf-uid"); err != nil {
		t.Fatal(err)
	}
	for _, state := range []ReservationState{ReservationBound, ReservationGranted} {
		reservation := ledger.Reservations[request.ID]
		reservation.State = state
		ledger.Reservations[request.ID] = reservation
		if err := ReleaseUnclaimedAtEpoch(ledger, request.ID, request.TopologyLockID,
			"leaf-uid", request.ControlRevision, request.PolicyEpoch); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("release state %s error = %v, want ErrInvalidTransition", state, err)
		}
		if got := ledger.Reservations[request.ID].State; got != state {
			t.Fatalf("rejected release changed state %s to %s", state, got)
		}
	}
	if err := RevokeUnclaimedAtEpoch(ledger, request.ID, request.TopologyLockID,
		"leaf-uid", request.ControlRevision, request.PolicyEpoch); err != nil {
		t.Fatalf("exact unclaimed revoke: %v", err)
	}
	if got := ledger.Reservations[request.ID].State; got != ReservationRevoked {
		t.Fatalf("unclaimed revoke state = %s, want Revoked", got)
	}
	if err := ReleaseUnclaimedAtEpoch(ledger, request.ID, request.TopologyLockID,
		"leaf-uid", request.ControlRevision, request.PolicyEpoch); err != nil {
		t.Fatalf("release revoked reservation: %v", err)
	}
}

func TestRevokeUnclaimedAtEpochRejectsEveryStaleBinding(t *testing.T) {
	policy := testPolicy()
	target := testMember("serial-a", "device-a", "node-a", "site-a", "pair-a", true)
	request := testRequest("reservation-a", target)
	request.ControlRevision = 7
	for name, mutate := range map[string]func(*Reservation, *string, *string, *uint64, *int64){
		"topology lock": func(_ *Reservation, lock, _ *string, _ *uint64, _ *int64) { *lock = strings.Repeat("2", 32) },
		"child UID":     func(_ *Reservation, _ *string, child *string, _ *uint64, _ *int64) { *child = "other-leaf" },
		"control":       func(_ *Reservation, _ *string, _ *string, revision *uint64, _ *int64) { *revision = 6 },
		"policy epoch":  func(_ *Reservation, _ *string, _ *string, _ *uint64, epoch *int64) { *epoch = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			ledger := testLedger(t)
			if err := Reserve(ledger, policy, []Member{target}, request); err != nil {
				t.Fatal(err)
			}
			if err := BindChild(ledger, request.ID, "leaf-uid"); err != nil {
				t.Fatal(err)
			}
			reservation := ledger.Reservations[request.ID]
			reservation.State = ReservationGranted
			reservation.ControlRevision = request.ControlRevision
			ledger.Reservations[request.ID] = reservation
			lockID, childUID, revision, epoch := request.TopologyLockID, "leaf-uid", request.ControlRevision, request.PolicyEpoch
			mutate(&reservation, &lockID, &childUID, &revision, &epoch)
			if err := RevokeUnclaimedAtEpoch(ledger, request.ID, lockID, childUID, revision, epoch); err == nil {
				t.Fatal("stale unclaimed revocation succeeded")
			}
			if got := ledger.Reservations[request.ID].State; got != ReservationGranted {
				t.Fatalf("stale revocation changed state to %s", got)
			}
		})
	}
}

func TestAbsentReservationReleaseFenceRejectsDelayedReserve(t *testing.T) {
	policy := testPolicy()
	target := testMember("serial-a", "device-a", "node-a", "site-a", "pair-a", true)
	request := testRequest("reservation-a", target)
	ledger := testLedger(t)
	if err := ReleaseUnclaimedAtEpoch(ledger, request.ID, request.TopologyLockID, "", 0, request.PolicyEpoch); err != nil {
		t.Fatal(err)
	}
	if !HasReleaseFence(ledger, request.ID, request.PolicyEpoch, request.TopologyLockID) {
		t.Fatal("absent-reservation cleanup did not publish the exact release fence")
	}
	if err := Reserve(ledger, policy, []Member{target}, request); !errors.Is(err, ErrStaleControlRevision) {
		t.Fatalf("delayed Reserve error = %v, want ErrStaleControlRevision", err)
	}
}

func TestCampaignCanTightenButNotRelaxPolicy(t *testing.T) {
	policy := testPolicy()
	policy.DomainBudgets["topology.cisco.vk/site"] = 2
	targetA := testMember("serial-a", "device-a", "node-a", "site-a", "pair-a", true)
	targetB := testMember("serial-b", "device-b", "node-b", "site-a", "pair-b", true)
	ledger := testLedger(t)
	if err := Reserve(ledger, policy, []Member{targetA, targetB}, testRequest("reservation-a", targetA)); err != nil {
		t.Fatalf("Reserve(first) error = %v", err)
	}
	request := testRequest("reservation-b", targetB)
	request.CampaignLimits = map[string]int{"topology.cisco.vk/site": 1}
	if err := Reserve(ledger, policy, []Member{targetA, targetB}, request); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("tightened budget error = %v, want ErrBudgetExceeded", err)
	}

	ledger = testLedger(t)
	if err := Reserve(ledger, policy, []Member{targetA, targetB}, testRequest("reservation-a", targetA)); err != nil {
		t.Fatalf("Reserve(first relaxed case) error = %v", err)
	}
	request.CampaignLimits["topology.cisco.vk/site"] = 20
	if err := Reserve(ledger, policy, []Member{targetA, targetB}, request); err != nil {
		t.Fatalf("larger campaign value must not override admin limit: %v", err)
	}
}

func TestCampaignLimitsCountOnlyThatCampaignWhilePolicyCountsFleet(t *testing.T) {
	policy := testPolicy()
	policy.GlobalMaxConcurrentTransfers = 3
	policy.GlobalMaxUnavailable = 3
	policy.DomainTransferBudgets["topology.cisco.vk/site"] = 3
	policy.DomainBudgets["topology.cisco.vk/site"] = 3
	members := []Member{
		testMember("serial-a", "device-a", "node-a", "site-a", "pair-a", true),
		testMember("serial-b", "device-b", "node-b", "site-a", "pair-b", true),
		testMember("serial-c", "device-c", "node-c", "site-a", "pair-c", true),
	}
	ledger := testLedger(t)

	other := testRequest("reservation-a", members[0])
	other.CampaignUID = "other-campaign"
	other.CampaignMaxConcurrentTransfers = 1
	other.CampaignMaxUnavailable = 1
	other.CampaignTransferLimits = map[string]int{"topology.cisco.vk/site": 1}
	other.CampaignLimits = map[string]int{"topology.cisco.vk/site": 1}
	if err := Reserve(ledger, policy, members, other); err != nil {
		t.Fatalf("Reserve(other campaign) error = %v", err)
	}

	current := testRequest("reservation-b", members[1])
	current.CampaignUID = "current-campaign"
	current.CampaignMaxConcurrentTransfers = 1
	current.CampaignMaxUnavailable = 1
	current.CampaignTransferLimits = map[string]int{"topology.cisco.vk/site": 1}
	current.CampaignLimits = map[string]int{"topology.cisco.vk/site": 1}
	if err := Reserve(ledger, policy, members, current); err != nil {
		t.Fatalf("another campaign consumed a campaign-local limit: %v", err)
	}

	secondCurrent := testRequest("reservation-c", members[2])
	secondCurrent.CampaignUID = current.CampaignUID
	secondCurrent.CampaignMaxConcurrentTransfers = current.CampaignMaxConcurrentTransfers
	secondCurrent.CampaignMaxUnavailable = current.CampaignMaxUnavailable
	secondCurrent.CampaignTransferLimits = current.CampaignTransferLimits
	secondCurrent.CampaignLimits = current.CampaignLimits
	if err := Reserve(ledger, policy, members, secondCurrent); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("second same-campaign reservation error = %v, want campaign-local ErrBudgetExceeded", err)
	}
}

func TestAdministratorLimitsCountReservationsAcrossCampaigns(t *testing.T) {
	policy := testPolicy()
	policy.GlobalMaxConcurrentTransfers = 1
	policy.GlobalMaxUnavailable = 1
	members := []Member{
		testMember("serial-a", "device-a", "node-a", "site-a", "pair-a", true),
		testMember("serial-b", "device-b", "node-b", "site-b", "pair-b", true),
	}
	ledger := testLedger(t)
	first := testRequest("reservation-a", members[0])
	first.CampaignUID = "campaign-a"
	if err := Reserve(ledger, policy, members, first); err != nil {
		t.Fatalf("Reserve(first campaign) error = %v", err)
	}
	second := testRequest("reservation-b", members[1])
	second.CampaignUID = "campaign-b"
	if err := Reserve(ledger, policy, members, second); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("cross-campaign administrator ceiling error = %v, want ErrBudgetExceeded", err)
	}
}

func TestReservationTransitionsBindUIDAndUseMonotonicControl(t *testing.T) {
	policy := testPolicy()
	policy.DomainBudgets["topology.cisco.vk/redundancy-group"] = 2
	ledger := testLedger(t)
	target := testMember("serial-a", "device-a", "node-a", "site-a", "pair-a", true)
	if err := Reserve(ledger, policy, []Member{target}, testRequest("reservation-a", target)); err != nil {
		t.Fatal(err)
	}
	if err := Grant(ledger, "reservation-a", ledger.UID, "child-uid", 1); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Grant before bind = %v, want ErrInvalidTransition", err)
	}
	if err := BindChild(ledger, "reservation-a", "child-uid"); err != nil {
		t.Fatal(err)
	}
	if err := BindChild(ledger, "reservation-a", "recreated-uid"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("BindChild recreated UID = %v, want ErrInvalidTransition", err)
	}
	if err := Grant(ledger, "reservation-a", "wrong-ledger", "child-uid", 1); !errors.Is(err, ErrLedgerIdentity) {
		t.Fatalf("Grant wrong ledger = %v, want ErrLedgerIdentity", err)
	}
	if err := Grant(ledger, "reservation-a", ledger.UID, "child-uid", 1); err != nil {
		t.Fatal(err)
	}
	if err := Revoke(ledger, "reservation-a", 1); !errors.Is(err, ErrStaleControlRevision) {
		t.Fatalf("same-revision Revoke = %v, want ErrStaleControlRevision", err)
	}
	if err := Revoke(ledger, "reservation-a", 2); err != nil {
		t.Fatal(err)
	}
	if err := Grant(ledger, "reservation-a", ledger.UID, "child-uid", 3); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Grant after revoke = %v, want ErrInvalidTransition", err)
	}
	reservation := ledger.Reservations["reservation-a"]
	if err := Settle(ledger, "reservation-a", reservation.TopologyLockID, "child-uid", reservation.PolicyEpoch, true, true); err != nil {
		t.Fatal(err)
	}
	if len(ledger.Reservations) != 0 {
		t.Fatalf("settled reservation retained: %+v", ledger.Reservations)
	}
}

func TestGrantRecoversAfterLedgerCASBeforeLeafStatusPatch(t *testing.T) {
	policy := testPolicy()
	policy.DomainBudgets["topology.cisco.vk/redundancy-group"] = 2
	ledger := testLedger(t)
	target := testMember("serial-a", "device-a", "node-a", "site-a", "pair-a", true)
	if err := Reserve(ledger, policy, []Member{target}, testRequest("reservation-a", target)); err != nil {
		t.Fatal(err)
	}
	if err := BindChild(ledger, "reservation-a", "child-uid"); err != nil {
		t.Fatal(err)
	}
	if err := Grant(ledger, "reservation-a", ledger.UID, "child-uid", 1); err != nil {
		t.Fatal(err)
	}
	// Simulate a successful ledger write followed by a manager crash before
	// the leaf status was patched, then a pause/resume at a newer revision.
	if err := Grant(ledger, "reservation-a", ledger.UID, "child-uid", 3); err != nil {
		t.Fatalf("newer recovery Grant() error = %v", err)
	}
	if got := ledger.Reservations["reservation-a"].ControlRevision; got != 3 {
		t.Fatalf("recovered control revision = %d, want 3", got)
	}
	if err := Grant(ledger, "reservation-a", ledger.UID, "child-uid", 2); !errors.Is(err, ErrStaleControlRevision) {
		t.Fatalf("stale recovery Grant() error = %v, want ErrStaleControlRevision", err)
	}
}

func TestSettleFailsClosedWithoutOutcomeAndHealthEvidence(t *testing.T) {
	policy := testPolicy()
	policy.DomainBudgets["topology.cisco.vk/redundancy-group"] = 2
	ledger := testLedger(t)
	target := testMember("serial-a", "device-a", "node-a", "site-a", "pair-a", true)
	if err := Reserve(ledger, policy, []Member{target}, testRequest("reservation-a", target)); err != nil {
		t.Fatal(err)
	}
	if err := BindChild(ledger, "reservation-a", "child-uid"); err != nil {
		t.Fatal(err)
	}
	if err := Grant(ledger, "reservation-a", ledger.UID, "child-uid", 1); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		resolved bool
		healthy  bool
	}{{false, false}, {true, false}, {false, true}} {
		reservation := ledger.Reservations["reservation-a"]
		if err := Settle(ledger, "reservation-a", reservation.TopologyLockID, "child-uid", reservation.PolicyEpoch, tc.resolved, tc.healthy); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("Settle(%v,%v) = %v, want ErrInvalidTransition", tc.resolved, tc.healthy, err)
		}
	}
	if len(ledger.Reservations) != 1 {
		t.Fatal("failed settlement released reservation")
	}
}

func TestLedgerIdentityAndSizeLimits(t *testing.T) {
	ledger := testLedger(t)
	encoded, err := Encode(ledger, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(encoded, "different-uid"); !errors.Is(err, ErrLedgerIdentity) {
		t.Fatalf("Decode identity error = %v, want ErrLedgerIdentity", err)
	}
	ledger.Reservations["large"] = Reservation{ReservationRequest: ReservationRequest{ID: strings.Repeat("x", 2048)}}
	if _, err := Encode(ledger, 128); !errors.Is(err, ErrLedgerFull) {
		t.Fatalf("Encode size error = %v, want ErrLedgerFull", err)
	}
	ledger.Reservations["larger"] = Reservation{ReservationRequest: ReservationRequest{ID: strings.Repeat("x", DefaultMaxSerializedBytes)}}
	if _, err := Encode(ledger, DefaultMaxSerializedBytes*4); !errors.Is(err, ErrLedgerFull) {
		t.Fatalf("Encode oversized caller limit error = %v, want absolute ErrLedgerFull", err)
	}
}

func TestDecodeRejectsMalformedPersistedReservations(t *testing.T) {
	valid := persistedLedgerFixture()
	validData, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(validData, valid.UID); err != nil {
		t.Fatalf("Decode(valid) error = %v", err)
	}

	tests := []struct {
		name string
		data func() []byte
	}{
		{
			name: "unknown top-level field",
			data: func() []byte {
				return []byte(strings.Replace(string(validData), `{"version":`, `{"unknown":true,"version":`, 1))
			},
		},
		{
			name: "unknown reservation field",
			data: func() []byte {
				return []byte(strings.Replace(string(validData), `"id":"reservation-a"`, `"id":"reservation-a","unknown":true`, 1))
			},
		},
		{name: "trailing JSON", data: func() []byte { return append(append([]byte(nil), validData...), []byte(` {}`)...) }},
		{
			name: "absolute encoded-size limit",
			data: func() []byte {
				return append(append([]byte(nil), validData...), make([]byte, DefaultMaxSerializedBytes)...)
			},
		},
		{
			name: "null reservations",
			data: func() []byte {
				ledger := persistedLedgerFixture()
				ledger.Reservations = nil
				data, _ := json.Marshal(ledger)
				return data
			},
		},
		{
			name: "map key mismatch",
			data: func() []byte {
				ledger := persistedLedgerFixture()
				reservation := ledger.Reservations["reservation-a"]
				delete(ledger.Reservations, "reservation-a")
				ledger.Reservations["different"] = reservation
				data, _ := json.Marshal(ledger)
				return data
			},
		},
		{
			name: "unknown state",
			data: func() []byte {
				ledger := persistedLedgerFixture()
				reservation := ledger.Reservations["reservation-a"]
				reservation.State = "Future"
				ledger.Reservations["reservation-a"] = reservation
				data, _ := json.Marshal(ledger)
				return data
			},
		},
		{
			name: "reserved record has child",
			data: func() []byte {
				ledger := persistedLedgerFixture()
				reservation := ledger.Reservations["reservation-a"]
				reservation.State = ReservationReserved
				ledger.Reservations["reservation-a"] = reservation
				data, _ := json.Marshal(ledger)
				return data
			},
		},
		{
			name: "bound record lacks child",
			data: func() []byte {
				ledger := persistedLedgerFixture()
				reservation := ledger.Reservations["reservation-a"]
				reservation.ChildUID = ""
				ledger.Reservations["reservation-a"] = reservation
				data, _ := json.Marshal(ledger)
				return data
			},
		},
		{
			name: "noncanonical plan hash",
			data: func() []byte {
				ledger := persistedLedgerFixture()
				reservation := ledger.Reservations["reservation-a"]
				reservation.PlanHash = "sha256:plan"
				ledger.Reservations["reservation-a"] = reservation
				data, _ := json.Marshal(ledger)
				return data
			},
		},
		{
			name: "uppercase plan hash",
			data: func() []byte {
				ledger := persistedLedgerFixture()
				reservation := ledger.Reservations["reservation-a"]
				reservation.PlanHash = "sha256:" + strings.Repeat("A", 64)
				ledger.Reservations["reservation-a"] = reservation
				data, _ := json.Marshal(ledger)
				return data
			},
		},
		{
			name: "whitespace identity",
			data: func() []byte {
				ledger := persistedLedgerFixture()
				reservation := ledger.Reservations["reservation-a"]
				reservation.DeviceUID = "device uid a"
				ledger.Reservations["reservation-a"] = reservation
				data, _ := json.Marshal(ledger)
				return data
			},
		},
		{
			name: "unbounded identity",
			data: func() []byte {
				ledger := persistedLedgerFixture()
				reservation := ledger.Reservations["reservation-a"]
				delete(ledger.Reservations, "reservation-a")
				reservation.ID = strings.Repeat("r", 65)
				ledger.Reservations[reservation.ID] = reservation
				data, _ := json.Marshal(ledger)
				return data
			},
		},
		{
			name: "empty domain value",
			data: func() []byte {
				ledger := persistedLedgerFixture()
				reservation := ledger.Reservations["reservation-a"]
				reservation.Domains = map[string]string{"topology.cisco.vk/site": ""}
				ledger.Reservations["reservation-a"] = reservation
				data, _ := json.Marshal(ledger)
				return data
			},
		},
		{
			name: "control revision outside campaign range",
			data: func() []byte {
				ledger := persistedLedgerFixture()
				reservation := ledger.Reservations["reservation-a"]
				reservation.ControlRevision = uint64(1 << 63)
				ledger.Reservations["reservation-a"] = reservation
				data, _ := json.Marshal(ledger)
				return data
			},
		},
		{
			name: "invalid domain",
			data: func() []byte {
				ledger := persistedLedgerFixture()
				reservation := ledger.Reservations["reservation-a"]
				reservation.Domains = map[string]string{"not a label": "site-a"}
				ledger.Reservations["reservation-a"] = reservation
				data, _ := json.Marshal(ledger)
				return data
			},
		},
		{
			name: "duplicate physical identity",
			data: func() []byte {
				ledger := persistedLedgerFixture()
				reservation := ledger.Reservations["reservation-a"]
				reservation.ID = "reservation-b"
				reservation.DeviceUID = "device-uid-b"
				reservation.NodeUID = "node-uid-b"
				reservation.ChildName = "upgrade-b"
				reservation.ChildUID = "child-uid-b"
				ledger.Reservations[reservation.ID] = reservation
				data, _ := json.Marshal(ledger)
				return data
			},
		},
		{
			name: "duplicate child identity",
			data: func() []byte {
				ledger := persistedLedgerFixture()
				reservation := ledger.Reservations["reservation-a"]
				reservation.ID = "reservation-b"
				reservation.PhysicalID = "serial-b"
				reservation.DeviceUID = "device-uid-b"
				reservation.NodeUID = "node-uid-b"
				reservation.ChildUID = "child-uid-b"
				ledger.Reservations[reservation.ID] = reservation
				data, _ := json.Marshal(ledger)
				return data
			},
		},
		{
			name: "too many reservations",
			data: func() []byte {
				ledger := persistedLedgerFixture()
				base := ledger.Reservations["reservation-a"]
				for i := 1; i <= DefaultMaxActiveRecords; i++ {
					reservation := base
					reservation.ID = fmt.Sprintf("reservation-%03d", i)
					reservation.PhysicalID = fmt.Sprintf("serial-%03d", i)
					reservation.DeviceUID = fmt.Sprintf("device-uid-%03d", i)
					reservation.NodeUID = fmt.Sprintf("node-uid-%03d", i)
					reservation.ChildName = fmt.Sprintf("upgrade-%03d", i)
					reservation.ChildUID = fmt.Sprintf("child-uid-%03d", i)
					ledger.Reservations[reservation.ID] = reservation
				}
				data, _ := json.Marshal(ledger)
				return data
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Decode(tt.data(), valid.UID); !errors.Is(err, ErrLedgerIdentity) {
				t.Fatalf("Decode() error = %v, want fail-closed ledger/decode error", err)
			}
		})
	}
}

func persistedLedgerFixture() *Ledger {
	return &Ledger{
		Version: LedgerVersion,
		UID:     "ledger-uid",
		Reservations: map[string]Reservation{
			"reservation-a": {
				ReservationRequest: ReservationRequest{
					ID: "reservation-a", CampaignUID: "campaign-uid", PlanHash: "sha256:" + strings.Repeat("a", 64),
					PolicyUID: "policy-uid", PolicyVersion: "42", PolicyEpoch: 1, TopologyLockID: strings.Repeat("1", 32), PhysicalID: "serial-a",
					DeviceUID: "device-uid-a", NodeUID: "node-uid-a", ChildNamespace: "devices", ChildName: "upgrade-a",
					Domains: map[string]string{"topology.cisco.vk/site": "site-a"}, ControlRevision: 1,
				},
				ChildUID: "child-uid-a",
				State:    ReservationBound,
			},
		},
	}
}

func testLedger(t *testing.T) *Ledger {
	t.Helper()
	ledger, err := NewLedger("ledger-uid")
	if err != nil {
		t.Fatal(err)
	}
	return ledger
}

func testPolicy() Policy {
	return Policy{
		UID:                          "policy-uid",
		Version:                      "42",
		Epoch:                        1,
		GlobalMaxConcurrentTransfers: 10,
		GlobalMaxUnavailable:         10,
		DomainTransferBudgets: map[string]int{
			"topology.cisco.vk/site":             3,
			"topology.cisco.vk/redundancy-group": 2,
		},
		DomainBudgets: map[string]int{
			"topology.cisco.vk/site":             3,
			"topology.cisco.vk/redundancy-group": 1,
		},
		MaxActiveRecords:   DefaultMaxActiveRecords,
		MaxSerializedBytes: DefaultMaxSerializedBytes,
	}
}

func testMember(physical, device, node, site, pair string, healthy bool) Member {
	return Member{
		PhysicalID: physical,
		DeviceUID:  device,
		NodeUID:    node,
		Domains: map[string]string{
			"topology.cisco.vk/site":             site,
			"topology.cisco.vk/redundancy-group": pair,
		},
		HealthKnown:    true,
		Healthy:        healthy,
		HealthObserved: time.Now().UTC(),
	}
}

func testRequest(id string, target Member) ReservationRequest {
	return ReservationRequest{
		ID:             id,
		CampaignUID:    "campaign-uid",
		PlanHash:       "sha256:" + strings.Repeat("a", 64),
		PolicyUID:      "policy-uid",
		PolicyVersion:  "42",
		PolicyEpoch:    1,
		TopologyLockID: strings.Repeat("1", 32),
		PhysicalID:     target.PhysicalID,
		DeviceUID:      target.DeviceUID,
		NodeUID:        target.NodeUID,
		ChildNamespace: "default",
		ChildName:      "upgrade-" + id,
		Domains:        target.Domains,
	}
}
