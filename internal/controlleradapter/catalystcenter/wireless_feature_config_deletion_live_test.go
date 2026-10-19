// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controller"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

func qualifyLiveFeatureConfigDeletion(t *testing.T, ctx context.Context, a *adapter, c *configapi.NetworkControllerConfig, manager *controller.NetworkControllerReconciler, admin ctrlclient.Client, caTrace *cleanAirManifestTrace, fraTrace *fraManifestTrace, caBaseline, fraBaseline map[string]wirelessFeatureInstance, save func(string, any), priorRecoveries int) {
	t.Helper()
	key := ctrlclient.ObjectKeyFromObject(c)
	originalSpec := featureIdentityDigest(c.Spec)
	caTrace.allowDelete = true
	fraTrace.allowDelete = true
	if err := admin.Delete(ctx, c); err != nil {
		t.Fatal(err)
	}
	recovered := map[string]bool{}
	complete := false
	for range 1600 {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal("config cleanup failed; retain fixture", err)
		}
		if err := admin.Get(ctx, key, c); err != nil {
			t.Fatal(err)
		}
		nc := mustFeatureController(t, a)
		save("deleting-config.json", c)
		save("deleting-controller.json", nc)
		if nc.Status.Configuration != nil {
			op := nc.Status.Configuration.Operation
			if op != nil && op.Phase == "Accepted" && op.Action == "Delete" && !recovered[op.ID] {
				if !featureFinalizeSelected(op.Resource) {
					t.Fatal("unbound config cleanup operation")
				}
				if err := a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
				recovered[op.ID] = true
				t.Log("acknowledged config cleanup retained Unknown", op.TargetID, op.TaskID)
			}
		}
		if featureDeletionCompleted(nc, c) {
			complete = true
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("config cleanup timed out; retain fixture")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !complete || len(recovered) != 2 || featureIdentityDigest(c.Spec) != originalSpec || !hasFeatureConfigFinalizer(c) {
		t.Fatal("deletion-bound config cleanup incomplete", complete, len(recovered))
	}
	save("deleting-config-complete.json", c)
	save("deleting-controller-complete.json", mustFeatureController(t, a))
	if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
		t.Fatal("manager did not finalize cleaned config", err)
	}
	if err := admin.Get(ctx, key, &configapi.NetworkControllerConfig{}); !apierrors.IsNotFound(err) {
		t.Fatal("cleaned config retained or lookup failed", err)
	}
	caAfter, err := a.client.wirelessFeatureInstances(ctx, wirelessCleanAirKind)
	if err != nil || !reflect.DeepEqual(caAfter, caBaseline) {
		t.Fatal("config cleanup did not restore CleanAir baseline", err)
	}
	fraAfter, err := a.client.wirelessFeatureInstances(ctx, wirelessFRAKind)
	if err != nil || !reflect.DeepEqual(fraAfter, fraBaseline) {
		t.Fatal("config cleanup did not restore FRA baseline", err)
	}
	if caTrace.posts != 1 || caTrace.puts != 6 || caTrace.deletes != 1 || caTrace.attempts != 8 || fraTrace.posts != 1 || fraTrace.puts != 6 || fraTrace.deletes != 1 || fraTrace.attempts != 8 {
		t.Fatal("config cleanup duplicated a native mutation")
	}
	save("complete.json", map[string]any{"configDeleted": true, "managerFinalized": true, "workerStatusOnly": true, "cleaned": true, "knownTaskRecoveries": priorRecoveries + len(recovered), "deletionTaskRecoveries": len(recovered), "sourceRevisions": 7, "retainedRecords": len(journalState(t, a).ActionApplications), "caMutationAttempts": caTrace.attempts, "fraMutationAttempts": fraTrace.attempts})
	t.Log("manager enrollment, status-only worker native cleanup and manager finalizer removal verified; both baselines restored")
}
