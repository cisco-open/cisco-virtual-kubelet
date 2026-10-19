// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

func qualifyLiveFeaturePrune(t *testing.T, ctx context.Context, a *adapter, config *configapi.NetworkControllerConfig, save func(string, any)) map[string]cvk.NetworkControllerConfigurationActionApplication {
	t.Helper()
	config.Generation++
	config.Spec.Source.Inline.Raw = []byte(featurePruneEmpty)
	config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	key := ctrlclient.ObjectKeyFromObject(config)
	recovered := map[string]bool{}
	converged := false
	for range 1600 {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal("public prune reconcile failed; retain fixture", err)
		}
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		save("prune-controller.json", current)
		if current.Status.Configuration != nil {
			op := current.Status.Configuration.Operation
			if op != nil && op.Action == "Delete" && op.Phase == "Accepted" && !recovered[op.ID] {
				if !featureDeleteSelected(op.Resource) || !validAPIID(op.TaskID) {
					t.Fatal("unbound Delete acknowledgement")
				}
				op.Phase = "Unknown"
				raw, err := json.Marshal(current)
				if err != nil {
					t.Fatal(err)
				}
				restored := &cvk.NetworkController{}
				if err = json.Unmarshal(raw, restored); err != nil {
					t.Fatal(err)
				}
				if err = a.statusWriter.Status().Update(ctx, restored); err != nil {
					t.Fatal(err)
				}
				recovered[op.ID] = true
				save("prune-controller-unknown.json", restored)
				t.Log("serialized acknowledged Delete retained Unknown", op.TargetID, op.TaskID)
			}
		}
		if err = a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
		save("prune-config.json", config)
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseFailed {
			t.Fatal("public prune failed; retain fixture", config.Status)
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
			converged = true
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("public prune timed out; retain fixture")
		case <-time.After(100 * time.Millisecond):
		}
	}
	state := journalState(t, a)
	deleted, err := featureDeletedRecords(state)
	cohorts, lineageErr := featureRetiredCohorts(state)
	retiredRows := 0
	for _, cohort := range cohorts {
		retiredRows += len(cohort.Rows)
	}
	if !converged || err != nil || lineageErr != nil || len(deleted) != 2 || len(recovered) != 2 || len(state.ActionApplications) != 6+retiredRows || state.Operation != nil {
		t.Fatal("public prune lifecycle incomplete; retain fixture", converged, err, len(deleted), len(recovered))
	}
	for range 3 {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.statusReader.Get(ctx, key, config); err != nil {
		t.Fatal(err)
	}
	save("prune-tombstones.json", deleted)
	save("prune-complete-state.json", journalState(t, a))
	return deleted
}
