// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	reservationworker "github.com/cisco/virtual-kubelet-cisco/internal/provider/configurationreservation"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

func lanDeleteReplanFixture(t *testing.T) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, *lanPreflightFixture, []*ops.ControllerConfigurationReservation) {
	t.Helper()
	ctx := context.Background()
	a, config, source, want, f := ownedLANDeleteFixture(t, true)
	if _, err := a.prepareLANLinkDelete(ctx, config, source, want.name); err != nil {
		t.Fatal(err)
	}
	installMixedLANGroup(t, a, config, false)
	var rows ops.ControllerConfigurationReservationList
	if err := a.statusReader.List(ctx, &rows); err != nil {
		t.Fatal(err)
	}
	members := make([]*ops.ControllerConfigurationReservation, len(rows.Items))
	for i := range rows.Items {
		members[i] = rows.Items[i].DeepCopy()
	}
	return a, config, source, f, members
}
func TestLANLinkDeleteStalePreparationCancellation(t *testing.T) {
	for _, mode := range []string{"topology", "down-link", "partial-claim", "ungranted", "unchanged", "source-generation", "denied", "missing-member", "changed-member", "full-history", "late-source", "late-native", "late-member", "late-submitting", "lost-response", "private-callback-copy"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, f, members := lanDeleteReplanFixture(t)
			original := journalState(t, a).Operation.DeepCopy()
			initialTopology := f.topologyID
			f.topologyID = "new-cache-id"
			switch mode {
			case "unchanged":
				f.topologyID = initialTopology
			case "topology":
				f.topologyID = initialTopology
				f.topologyID = "new-cache-id"
			case "down-link":
				f.topologyID = initialTopology
				f.ports[0].Status = "Down"
			case "source-generation":
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case "partial-claim":
				members[0].Status.Claim = &ops.ConfigurationReservationClaim{OperationID: original.ID, OperationDigest: original.PhysicalGroup.Digest, ClaimedAt: metav1.NewTime(time.Now().UTC().Truncate(time.Second))}
				if err := a.statusWriter.Status().Update(ctx, members[0]); err != nil {
					t.Fatal(err)
				}
			case "ungranted":
				for _, member := range members {
					member.Status.Grant = nil
					if err := a.statusWriter.Status().Update(ctx, member); err != nil {
						t.Fatal(err)
					}
				}
			case "missing-member":
				member := members[0]
				member.Finalizers = nil
				if err := a.statusWriter.Update(ctx, member); err != nil {
					t.Fatal(err)
				}
				if err := a.statusWriter.Delete(ctx, member); err != nil {
					t.Fatal(err)
				}
			case "changed-member":
				members[0].Spec.DeviceWorkerPod.UID = "foreign"
				if err := a.statusWriter.Update(ctx, members[0]); err != nil {
					t.Fatal(err)
				}
			case "full-history":
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.GroupCancellations = make([]cvk.NetworkControllerConfigurationGroupCancellation, 32)
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			observed, _, err := a.observeLANLinkDeleteModel(ctx, config, source)
			if err != nil {
				t.Fatal("current complete observation", err)
			}
			authorizations := 0
			a.configurationAdmission.authorize = func(ctx context.Context, member *ops.ControllerConfigurationReservation) error {
				authorizations++
				if mode == "denied" {
					return errors.New("revoked")
				}
				if mode == "private-callback-copy" {
					member.Spec.Device.UID = "forged"
					member.UID = "forged"
				}
				if authorizations != 1 {
					return nil
				}
				switch mode {
				case "late-source":
					var changed configapi.NetworkControllerConfig
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &changed); err != nil {
						return err
					}
					changed.Generation++
					return a.statusWriter.Update(ctx, &changed)
				case "late-native":
					f.topologyID = "changed-during-authorization"
				case "late-member":
					var changed ops.ControllerConfigurationReservation
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(members[0]), &changed); err != nil {
						return err
					}
					changed.UID = "replacement"
					return a.statusWriter.Update(ctx, &changed)
				case "late-submitting":
					nc, err := a.currentController(ctx)
					if err != nil {
						return err
					}
					nc.Status.Configuration.Operation.Phase = "Submitting"
					at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
					nc.Status.Configuration.Operation.SubmittedAt = &at
					return a.statusWriter.Status().Update(ctx, nc)
				}
				return nil
			}
			if mode == "lost-response" {
				a.statusWriter = lostJournalAckClient{a.statusWriter}
			}
			cancelled, err := a.cancelStaleLANLinkPreparation(ctx, config, source, original, observed)
			success := mode == "topology" || mode == "down-link" || mode == "partial-claim" || mode == "ungranted" || mode == "private-callback-copy"
			if cancelled != success || (err == nil) != (success || mode == "unchanged") {
				t.Fatal("unexpected cancellation outcome", cancelled, err)
			}
			state := journalState(t, a)
			expected := "Prepared"
			if success || mode == "lost-response" {
				expected = "Cancelling"
			}
			if mode == "late-submitting" {
				expected = "Submitting"
			}
			if state.Operation.Phase != expected || state.Operation.ID != original.ID || state.Operation.Resource != original.Resource || !reflect.DeepEqual(state.Operation.PhysicalGroup, original.PhysicalGroup) || len(state.ActionApplications) != 1 || f.posts != 1 {
				t.Fatal("cancellation refreshed scope, submitted or completed work", state.Operation.Phase)
			}
			if expected == "Cancelling" {
				nc, e := a.currentController(ctx)
				if e != nil || contract.ValidateGroupCancellationFence(nc, time.Now()) != nil {
					t.Fatal("missing durable cancellation fence", e)
				}
			}
			if mode == "partial-claim" {
				var kept ops.ControllerConfigurationReservation
				if e := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(members[0]), &kept); e != nil || !reflect.DeepEqual(kept.Status.Claim, members[0].Status.Claim) {
					t.Fatal("cancellation erased partial claim", e)
				}
			}
		})
	}
}

func TestLANLinkDeletePublicReplanAfterCancellationSettlement(t *testing.T) {
	ctx := context.Background()
	a, config, _, f, members := lanDeleteReplanFixture(t)
	original := journalState(t, a).Operation.DeepCopy()
	f.topologyID = "changed-cache-id"
	r := &configReconciler{a: a}
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if nc.Status.Configuration.Operation.Phase != "Cancelling" {
		t.Fatal("missing cancellation")
	}
	// Explicit fake manager seeding of retained canonical leases, as required
	// by the production managed worker's RequireExisting gate.
	var canonical coordination.LeaseList
	if err := a.statusReader.List(ctx, &canonical); err != nil {
		t.Fatal(err)
	}
	for i := range canonical.Items {
		lease := &canonical.Items[i]
		if lease.Annotations == nil {
			lease.Annotations = map[string]string{}
		}
		lease.Annotations[devicecoordination.RetainLeaseAnnotation] = "true"
		if err := a.statusWriter.Update(ctx, lease); err != nil {
			t.Fatal(err)
		}
	}
	leaser := &engine.FamilyLeaser{Client: a.statusWriter, Namespace: "leases", TTL: time.Minute, RequireExisting: true}
	for i, member := range members {
		worker := &reservationworker.Reconciler{Reader: a.statusReader, Client: a.statusWriter, Leaser: leaser, Namespace: member.Namespace, DeviceName: member.Spec.Device.Name, DeviceUID: member.Spec.Device.UID, PodUID: member.Spec.DeviceWorkerPod.UID, ControllerType: cvk.NetworkControllerType(TypeName), Authorize: func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }}
		workerReq := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(member)}
		// First pass acknowledges the fence. Second pass performs only this
		// worker's release and records the existing Released grant status.
		for range 2 {
			if _, err := worker.Reconcile(ctx, workerReq); err != nil {
				t.Fatal("device cancellation worker", err)
			}
		}
		var fresh ops.ControllerConfigurationReservation
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(member), &fresh); err != nil {
			t.Fatal(err)
		}
		if fresh.Status.GroupCancellation == nil || fresh.Status.Grant.Phase != "Released" || fresh.Status.ManagerSettledAt != nil {
			t.Fatal("worker skipped release or fabricated manager settlement")
		}
		var lease coordination.Lease
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: "leases", Name: fresh.Status.Grant.Lease.Name}, &lease); err != nil {
			t.Fatal(err)
		}
		if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "" {
			t.Fatal("worker did not release canonical holder")
		}
		if _, err := r.Reconcile(ctx, req); err == nil {
			t.Fatal("release without manager settlement cleared journal")
		}
		// Explicit fake maintenance manager observation; the real contract
		// validates its identities/session and absent holder before publication.
		var device cvk.CiscoDevice
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: member.Namespace, Name: member.Spec.Device.Name}, &device); err != nil {
			t.Fatal(err)
		}
		device.Status.MaintenanceSession.Phase = cvk.DeviceMaintenanceSessionSettled
		if err := a.statusWriter.Update(ctx, &device); err != nil {
			t.Fatal(err)
		}
		var node core.Node
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: member.Spec.Node.Name}, &node); err != nil {
			t.Fatal(err)
		}
		kept := node.Spec.Taints[:0]
		for _, taint := range node.Spec.Taints {
			if taint.Key != managedprotocol.MaintenanceTaintKey {
				kept = append(kept, taint)
			}
		}
		node.Spec.Taints = kept
		if err := a.statusWriter.Update(ctx, &node); err != nil {
			t.Fatal(err)
		}
		if err := contract.PublishCancelledGroupManagerSettlement(ctx, a.statusReader, a.statusWriter, nc, &fresh, "leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }, time.Now()); err != nil {
			t.Fatal("manager cancellation settlement", err)
		}
		_, err := r.Reconcile(ctx, req)
		if (err == nil) != (i == len(members)-1) {
			t.Fatal("journal cleared before complete original settlement", i, err)
		}
	}
	state := journalState(t, a)
	if state.Operation != nil || len(state.GroupCancellations) != 1 || state.GroupCancellations[0].OperationID != original.ID || state.GroupCancellations[0].Operation.Resource != original.Resource {
		t.Fatal("original cancellation evidence lost")
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	state = journalState(t, a)
	if state.Operation == nil || state.Operation.ID == original.ID || state.Operation.Resource == original.Resource || state.Operation.Phase != "Prepared" || !reflect.DeepEqual(state.Operation.PhysicalGroup.Targets, original.PhysicalGroup.Targets) || len(state.GroupCancellations) != 1 || f.posts != 1 {
		t.Fatal("settled cancellation did not produce a fresh scoped preparation")
	}
}

func TestLANLinkDeletePublicReplanRefusesUnknownScope(t *testing.T) {
	for _, mode := range []string{"topology", "unchanged", "native-drift", "software", "pool", "source", "active", "missing-member", "submitting"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, f, members := lanDeleteReplanFixture(t)
			old := journalState(t, a).Operation.DeepCopy()
			if mode != "unchanged" {
				f.topologyID = "changed-cache-id"
			}
			switch mode {
			case "native-drift":
				f.ports[0].IPv4 = "192.0.2.132"
			case "software":
				f.inventory[0].SoftwareVersion = "17.18.4"
			case "pool":
				f.poolChanged = true
			case "source":
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case "active":
				f.mode = "active"
			case "missing-member":
				members[0].Finalizers = nil
				if err := a.statusWriter.Update(ctx, members[0]); err != nil {
					t.Fatal(err)
				}
				if err := a.statusWriter.Delete(ctx, members[0]); err != nil {
					t.Fatal(err)
				}
			case "submitting":
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.Operation.Phase = "Submitting"
				at := metav1.Now()
				nc.Status.Configuration.Operation.SubmittedAt = &at
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			expected := "Prepared"
			posts := 1
			if mode == "unchanged" {
				expected = "Accepted"
				posts = 2
			}
			if mode == "topology" {
				expected = "Cancelling"
			}
			if mode == "submitting" {
				expected = "Submitting"
			}
			if state.Operation == nil || state.Operation.Phase != expected || state.Operation.ID != old.ID || state.Operation.Resource != old.Resource || f.posts != posts || len(state.ActionApplications) != 1 {
				t.Fatal("unknown scope cancelled, replayed or discarded original work", mode)
			}
		})
	}
}
