// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func groupGrantFixture(t *testing.T) (*cvk.NetworkController, []*ops.ControllerConfigurationReservation, []*coordination.Lease, time.Time) {
	t.Helper()
	nc, members, connector, now := groupCandidateFixture(t)
	reservations, err := GroupReservationCandidates(nc, members, connector, now)
	if err != nil {
		t.Fatal(err)
	}
	var leases []*coordination.Lease
	for _, r := range reservations {
		r.UID = types.UID(r.Name + "-uid")
		holder, err := Holder(r.Namespace, r)
		if err != nil {
			t.Fatal(err)
		}
		name := engine.LeaseName(devicecoordination.DeviceKey(r.Namespace, r.Spec.Device.Name), devicecoordination.MutationLeaseFamily)
		duration, transitions := int32(60), int32(1)
		acquired, renewed := metav1.NewMicroTime(now.Add(-30*time.Second)), metav1.NewMicroTime(now.Add(-time.Second))
		lease := &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Namespace: "leases", Name: name, UID: types.UID(name + "-uid")}, Spec: coordination.LeaseSpec{HolderIdentity: &holder, LeaseDurationSeconds: &duration, LeaseTransitions: &transitions, AcquireTime: &acquired, RenewTime: &renewed}}
		r.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held", LeaseNamespace: "leases", Lease: ops.ConfigurationReservationReference{Name: name, UID: string(lease.UID)}, Holder: holder, LeaseTransitions: transitions, GrantedAt: metav1.NewTime(now.Add(-20 * time.Second))}
		leases = append(leases, lease)
	}
	return nc, reservations, leases, now
}

func TestGroupLiveGrants(t *testing.T) {
	for _, mode := range []string{"valid", "reordered", "missing-lease", "extra-lease", "duplicate-lease", "missing-reservation", "revoked", "expired", "replaced-lease", "foreign-holder", "transition", "future-renewal", "foreign-namespace", "settled", "completed", "cancelled", "replaced-reservation"} {
		t.Run(mode, func(t *testing.T) {
			nc, reservations, leases, now := groupGrantFixture(t)
			r, l := reservations[1], leases[1]
			at := metav1.NewTime(now)
			switch mode {
			case "reordered":
				leases[0], leases[1] = leases[1], leases[0]
			case "missing-lease":
				leases = leases[:1]
			case "extra-lease":
				leases = append(leases, leases[0].DeepCopy())
			case "duplicate-lease":
				leases[1] = leases[0]
			case "missing-reservation":
				reservations = reservations[:1]
			case "revoked":
				r.Status.Grant.Phase = "Revoked"
			case "expired":
				v := metav1.NewMicroTime(now.Add(-time.Minute))
				l.Spec.RenewTime = &v
			case "replaced-lease":
				l.UID = "replacement"
			case "foreign-holder":
				*l.Spec.HolderIdentity = "software-upgrade/other"
			case "transition":
				*l.Spec.LeaseTransitions++
			case "future-renewal":
				v := metav1.NewMicroTime(now.Add(time.Second))
				l.Spec.RenewTime = &v
			case "foreign-namespace":
				l.Namespace = "other"
				r.Status.Grant.LeaseNamespace = "other"
			case "settled":
				r.Status.ManagerSettledAt = &at
			case "completed":
				r.Status.Completion = &ops.ConfigurationReservationCompletion{}
			case "cancelled":
				r.Status.PreparationCancelledAt = &at
			case "replaced-reservation":
				r.UID = "replacement"
			}
			err := ValidateGroupLiveGrants(nc, reservations, leases, "leases", now)
			if (err == nil) != (mode == "valid" || mode == "reordered") {
				t.Fatal(mode, err)
			}
		})
	}
}

func TestGroupPreparationRequiresFreshUnclaimedGrants(t *testing.T) {
	for _, mode := range []string{"valid", "old-preparation", "claimed", "submitted", "receipt", "paused", "generation", "unknown", "deleting"} {
		t.Run(mode, func(t *testing.T) {
			nc, reservations, leases, now := groupGrantFixture(t)
			switch mode {
			case "old-preparation":
				reservations[1].Status.Grant.GrantedAt = metav1.NewTime(now.Add(-ConfigurationGrantMaxAge))
				at := metav1.NewMicroTime(now.Add(-ConfigurationGrantMaxAge - time.Second))
				leases[1].Spec.AcquireTime = &at
				// The renewed lease remains live; it must not refresh native preparation.
				if err := ValidateGroupLiveGrants(nc, reservations, leases, "leases", now); err != nil {
					t.Fatal(err)
				}
			case "claimed":
				reservations[1].Status.Claim = &ops.ConfigurationReservationClaim{}
			case "submitted":
				nc.Status.Configuration.Operation.Phase = "Submitting"
			case "receipt":
				nc.Status.Configuration.Operation.TaskID = "task"
			case "paused":
				nc.Spec.Paused = true
			case "generation":
				nc.Generation++
			case "unknown":
				nc.Status.Configuration.Operation.Phase = "Unknown"
			case "deleting":
				at := metav1.NewTime(now)
				nc.DeletionTimestamp = &at
			}
			err := ValidateGroupPreparation(nc, reservations, leases, "leases", now)
			if (err == nil) != (mode == "valid") {
				t.Fatal(mode, err)
			}
			if ValidateLiveGrant(reservations[0], leases[0], "leases", now) == nil {
				t.Fatal("v1 grant gate admitted group")
			}
		})
	}
}
