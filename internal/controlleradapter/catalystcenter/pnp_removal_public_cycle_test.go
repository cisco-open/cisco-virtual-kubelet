// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestPnPRemovalPublicMetadataCycle(t *testing.T) {
	for _, mode := range []string{"verified", "native-lost", "late-sibling", "planning-denied", "planning-revoked"} {
		t.Run(mode, func(t *testing.T) { exercisePnPRemovalPublicMetadataCycle(t, mode, nil, nil) })
	}
}

// The public reconciler owns preparation, original-operation dispatch, absence
// verification and finishing. Manager custody uses the shared production helpers;
// no test directly invokes the native Delete closure or reconstructs a permit.
func exercisePnPRemovalPublicMetadataCycle(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig), harness *metadataSubmissionHarness) {
	nativeMode := "cycle"
	if mode == "native-lost" {
		nativeMode = "metadata-lost"
	}
	if mode == "late-sibling" {
		nativeMode = "late-blocked-second"
	}
	exercisePnPRemovalCohort(t, nativeMode, install, func(a *adapter, c *configapi.NetworkControllerConfig, s *configSourceSnapshot, _ *configurationWrite) {
		ctx := context.Background()
		admin := a.statusWriter
		var deletes atomic.Int32
		a.client.mu.Lock()
		a.client.builtAt = time.Time{}
		a.client.wrapTransport = func(base http.RoundTripper) http.RoundTripper {
			return metadataSubmitRoundTrip(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodDelete {
					deletes.Add(1)
				}
				return base.RoundTrip(req)
			})
		}
		a.client.mu.Unlock()
		reconcile := func(expected string) {
			t.Helper()
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
				t.Fatal(err)
			}
			if err := admin.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
				t.Fatal(err)
			}
			ready := meta.FindStatusCondition(c.Status.Conditions, configapi.NetworkControllerConfigConditionReady)
			if ready == nil || ready.Reason != expected || ready.Status == metav1.ConditionTrue || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("unexpected public metadata outcome", expected, c.Status.Conditions, c.Status.Phase)
			}
		}
		reconcile("PnPRemovalDispatchUnavailable")
		if journalState(t, a).Operation != nil || deletes.Load() != 0 {
			t.Fatal("disabled runtime prepared or dispatched cleanup")
		}
		installation := contract.BootstrapFenceInstallation{PolicyName: "cvk-cisco-virtual-kubelet-bootstrap-fence", ManagerUsername: "system:serviceaccount:system:cisco-virtual-kubelet-controller", LeaseNamespace: "leases"}
		policy := "cvk-cisco-virtual-kubelet-metadata-handoff"
		// Installation/caller authorization is staged in this fixture. Production
		// composition remains disabled until installed runtime qualification exists.
		a.workerPodName, a.workerPodUID = "metadata-worker", "metadata-worker"
		planningChecks := 0
		if err := a.ConfigureMetadataHandoff(installation, policy, func(context.Context, *cvk.NetworkController) error {
			planningChecks++
			if mode == "planning-denied" || mode == "planning-revoked" && planningChecks > 1 {
				return errors.New("metadata execution disabled")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if mode == "planning-denied" || mode == "planning-revoked" {
			reconcile("PnPRemovalRuntimeUnavailable")
			state := journalState(t, a)
			if state.Operation != nil || state.MetadataHandoff != nil || len(state.ActionApplications) != 2 || deletes.Load() != 0 {
				t.Fatal("unavailable planning authority created work or lost creator history")
			}
			return
		}
		reconcile("PnPRemovalHandoffPending")
		state := journalState(t, a)
		if state.Operation == nil || state.Operation.TargetID != "aaa-id" || state.Operation.Phase != "Prepared" || state.MetadataHandoff != nil || deletes.Load() != 0 {
			t.Fatal("public preparation bypassed manager custody")
		}
		original := state.Operation.DeepCopy()
		reconcile("PnPRemovalHandoffPending")
		if !reflect.DeepEqual(original, journalState(t, a).Operation) || deletes.Load() != 0 {
			t.Fatal("unsealed public preparation changed or dispatched")
		}
		installation, policy = installPnPMetadataSubmissionFixture(t, a, c, s, harness)
		var manager ctrlclient.Client = &metadataSubmissionManager{Client: admin}
		if harness != nil {
			manager = harness.manager
		}
		for index := 0; index < 2; index++ {
			if index > 0 {
				reconcile("PnPRemovalHandoffPending")
				state = journalState(t, a)
				if state.Operation == nil || state.Operation.TargetID != "zzz-id" || state.Operation.Phase != "Prepared" {
					t.Fatal("public cohort did not advance deterministically")
				}
				sealPnPMetadataSubmissionFixture(t, a, c, s, manager, installation, policy)
			}
			before := journalState(t, a).DeepCopy()
			if mode != "late-sibling" && mode != "native-lost" {
				reconcile("PnPRemovalSubmitted")
			} else if mode == "late-sibling" {
				reconcile("PnPRemovalSettlementPending")
			} else {
				reconcile("PnPRemovalRecoveryRequired")
			}
			state = journalState(t, a)
			if mode == "native-lost" {
				if deletes.Load() != 1 || state.Operation == nil || state.Operation.Phase != "Unknown" || state.MetadataHandoff.RefusedAt != nil {
					t.Fatal("uncertain native response lost custody")
				}
				for range 3 {
					reconcile("PnPRemovalRecoveryRequired")
				}
				if deletes.Load() != 1 || len(journalState(t, a).ActionApplications) != 2 {
					t.Fatal("unknown public submission replayed or gained absence receipt")
				}
				return
			}
			if mode == "late-sibling" {
				if deletes.Load() != 0 || state.Operation != nil || state.MetadataHandoff.RefusedAt == nil || !reflect.DeepEqual(before.ActionApplications, state.ActionApplications) {
					t.Fatal("public pre-HTTP refusal was not certified")
				}
				reconcile("PnPRemovalSettlementPending")
			} else {
				if deletes.Load() != int32(index+1) || state.Operation.Phase != "Verifying" {
					t.Fatal("public dispatch did not retain acknowledgement")
				}
				reconcile("PnPRemovalVerified")
				state = journalState(t, a)
				if !pnpRemovedReceiptPresent(state, state.Operation) {
					t.Fatal("public verifier did not publish creator-bound tombstone")
				}
				// The early terminal route waits for manager custody retirement.
				if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err == nil {
					t.Fatal("terminal PnP recovery ignored retained manager custody")
				}
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			compatible := func(context.Context, *cvk.NetworkController) error { return nil }
			released, err := contract.SettleMetadataHandoff(ctx, a.statusReader, manager, nc, installation, policy, pnpMetadataSettlementState, compatible, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = contract.RetireMetadataHandoff(ctx, a.statusReader, manager, released, installation, policy, pnpMetadataSettlementState, compatible, time.Now()); err != nil {
				t.Fatal(err)
			}
			if mode == "late-sibling" {
				return
			}
			if strings.HasPrefix(mode, "terminal-") {
				exercisePnPTerminalRecovery(t, ctx, a, c, mode, &deletes)
				return
			}
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
				t.Fatal("terminal PnP recovery after manager retirement", err)
			}
			if journalState(t, a).Operation != nil {
				t.Fatal("terminal PnP recovery retained verified journal")
			}
		}
		reconcile("PnPRemovalComplete")
		reconcile("PnPRemovalComplete")
		state = journalState(t, a)
		if state.Operation != nil || state.MetadataHandoff != nil || len(state.ActionApplications) != 4 || deletes.Load() != 2 {
			t.Fatal("public cleanup lost history, replayed or failed to finish")
		}
	})
}
