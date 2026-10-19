// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func qualifyLiveFeatureRevision(t *testing.T, ctx context.Context, a *adapter, config *configapi.NetworkControllerConfig, raw []byte, action string, save func(string, any)) int {
	t.Helper()
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	config.Generation++
	config.Spec.Source.Inline.Raw = raw
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	key := ctrlclient.ObjectKeyFromObject(config)
	recovered := map[string]bool{}
	for range 1500 {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal("recreated lifecycle failed; retain fixture", err)
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		save("controller.json", nc)
		if nc.Status.Configuration != nil {
			op := nc.Status.Configuration.Operation
			if op != nil && op.Phase == "Accepted" && !recovered[op.ID] {
				if op.Action != action || !validAPIID(op.TaskID) || action == "Create" && !featureRecreateSelected(op.Resource) {
					t.Fatal("unexpected recreated operation; retain fixture", op.Action)
				}
				op.Phase = "Unknown"
				data, err := json.Marshal(nc)
				if err != nil {
					t.Fatal(err)
				}
				restored := &cvk.NetworkController{}
				if err = json.Unmarshal(data, restored); err != nil {
					t.Fatal(err)
				}
				if err = a.statusWriter.Status().Update(ctx, restored); err != nil {
					t.Fatal(err)
				}
				recovered[op.ID] = true
				save("controller-unknown.json", restored)
				t.Log("serialized recreated operation retained Unknown", op.Action, op.TargetID, op.TaskID)
			}
		}
		if err = a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
		save("config.json", config)
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseFailed {
			t.Fatal("recreated source failed; retain fixture", config.Status)
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
			if len(recovered) != 2 {
				t.Fatal("missing recreated task recoveries", len(recovered))
			}
			return len(recovered)
		}
		select {
		case <-ctx.Done():
			t.Fatal("recreated source timed out; retain fixture")
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("recreated source did not converge; retain fixture")
	return 0
}
func qualifyLiveFeatureRecreateCycles(t *testing.T, ctx context.Context, c *client, a *adapter, config *configapi.NetworkControllerConfig, desired []byte, name, fraName string, caBaseline, fraBaseline map[string]wirelessFeatureInstance, transport http.RoundTripper, save func(string, any)) {
	t.Helper()
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	priorTransport := rest.HTTPClient.Transport
	defer func() { rest.HTTPClient.Transport = priorTransport }()
	original := journalState(t, a).DeepCopy().ActionApplications
	totalRecoveries := 16
	comparisons := 14
	totalAttemptsPerKind := 8
	for cycle := 1; cycle <= 2; cycle++ {
		phaseSave := func(phase string) func(string, any) {
			return func(file string, value any) { save(fmt.Sprintf("cycle-%d-%s-%s", cycle, phase, file), value) }
		}
		caTrace := &cleanAirManifestTrace{base: transport, name: name, maxPuts: 1, allowDelete: true}
		fraTrace := &fraManifestTrace{base: transport, fallback: caTrace, name: fraName, maxPuts: 1, allowDelete: true}
		rest.HTTPClient.Transport = fraTrace
		totalRecoveries += qualifyLiveFeatureRevision(t, ctx, a, config, desired, "Create", phaseSave("create"))
		state := journalState(t, a)
		creators, err := featureCreatedRecords(state)
		if err != nil || len(creators) != 2 {
			t.Fatal("missing recreated creator records", err)
		}
		cohorts, err := featureRetiredCohorts(state)
		if err != nil || len(cohorts) != 2*cycle {
			t.Fatal("recreated history incomplete", err, len(cohorts))
		}
		check := func(kind, design, keyPrefix string, creation bool) string {
			t.Helper()
			instances, err := c.wirelessFeatureInstances(ctx, kind)
			if err != nil {
				t.Fatal(err)
			}
			instance, exists := instances[design]
			if !exists || *instance.System {
				t.Fatal("recreated identity absent", design)
			}
			var fingerprint string
			if kind == wirelessCleanAirKind {
				_, fingerprint, err = c.wirelessCleanAirReadState(ctx, instance)
			} else {
				_, fingerprint, err = c.wirelessFRAReadState(ctx, instance)
			}
			if err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			records, err := featureCreatedRecords(state)
			prefix := featureCreatedPrefix
			if !creation {
				records, err = featureAppliedRecords(state)
				prefix = featureAppliedPrefix
			}
			if err != nil {
				t.Fatal(err)
			}
			key := keyPrefix + ":" + featureIdentityDigest(design)
			row, exists := records[key]
			id, hash, err := featureReceiptIdentity(row.Resource, prefix, key)
			if !exists || err != nil || id != instance.ID || hash != fingerprint || row.Operation.ConfigGeneration != config.Generation {
				t.Fatal("recreated state not bound", design, err)
			}
			for _, cohort := range cohorts {
				if cohort.Key == key && cohort.ID == id {
					t.Fatal("retired UUID reused", design, id)
				}
			}
			if creation && !featureRecreateSelected(row.Operation.Resource) {
				t.Fatal("missing explicit recreation intent", design)
			}
			comparisons++
			return id
		}
		caTrace.id = check(wirelessCleanAirKind, name, "ca", true)
		fraTrace.id = check(wirelessFRAKind, fraName, "fra", true)
		if caTrace.posts != 1 || fraTrace.posts != 1 || caTrace.attempts != 1 || fraTrace.attempts != 1 {
			t.Fatal("recreation duplicated a native POST")
		}
		var root map[string]any
		if json.Unmarshal(desired, &root) != nil {
			t.Fatal("invalid qualification model")
		}
		wireless := root["catalyst_center"].(map[string]any)["feature_templates"].(map[string]any)["wireless"].(map[string]any)
		ca := wireless["cleanair"].([]any)[0].(map[string]any)
		reporting, _ := ca["device_reporting"].(bool)
		ca["device_reporting"] = !reporting
		fra := wireless["rrm_fra"].([]any)[0].(map[string]any)
		enabled, _ := fra["fra_status"].(bool)
		fra["fra_status"] = !enabled
		updated, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		totalRecoveries += qualifyLiveFeatureRevision(t, ctx, a, config, updated, "Update", phaseSave("update"))
		if check(wirelessCleanAirKind, name, "ca", false) != caTrace.id || check(wirelessFRAKind, fraName, "fra", false) != fraTrace.id {
			t.Fatal("recreated Update changed UUID")
		}
		deleted := qualifyLiveFeaturePrune(t, ctx, a, config, phaseSave("prune"))
		totalRecoveries += len(deleted)
		if caTrace.posts != 1 || caTrace.puts != 1 || caTrace.deletes != 1 || caTrace.attempts != 3 || fraTrace.posts != 1 || fraTrace.puts != 1 || fraTrace.deletes != 1 || fraTrace.attempts != 3 {
			t.Fatal("recreated cycle repeated native effects")
		}
		totalAttemptsPerKind += 3
		after, err := c.wirelessFeatureInstances(ctx, wirelessCleanAirKind)
		if err != nil || !reflect.DeepEqual(after, caBaseline) {
			t.Fatal("recreated CleanAir baseline not restored", err)
		}
		fraAfter, err := c.wirelessFeatureInstances(ctx, wirelessFRAKind)
		if err != nil || !reflect.DeepEqual(fraAfter, fraBaseline) {
			t.Fatal("recreated FRA baseline not restored", err)
		}
		save(fmt.Sprintf("cycle-%d-complete.json", cycle), map[string]any{"cycle": cycle, "cleaned": true, "caID": caTrace.id, "fraID": fraTrace.id, "caAttempts": caTrace.attempts, "fraAttempts": fraTrace.attempts, "recoveries": 6})
		t.Log("recreated source lifecycle and retained cleanup complete", cycle, caTrace.id, fraTrace.id)
	}
	state := journalState(t, a)
	cohorts, err := featureRetiredCohorts(state)
	if err != nil || len(cohorts) != 4 || len(state.ActionApplications) != 18 || state.Operation != nil {
		t.Fatal("final recreation lineage incomplete", err, len(cohorts), len(state.ActionApplications))
	}
	for _, originalRow := range original {
		found := false
		for _, r := range state.ActionApplications {
			if r.Resource == featureRetiredPrefix+originalRow.Resource && reflect.DeepEqual(r.Operation, originalRow.Operation) && r.VerifiedAt.Equal(originalRow.VerifiedAt) {
				found = true
			}
		}
		if !found {
			t.Fatal("original native lifecycle history lost")
		}
	}
	save("recreate-complete-state.json", state)
	save("complete.json", map[string]any{"name": name, "fraName": fraName, "cleaned": true, "publicPrune": true, "publicRecreate": true, "manifestRevisions": 14, "knownTaskRecoveries": totalRecoveries, "nativeStatesChecked": comparisons, "retiredCohorts": len(cohorts), "retainedRecords": len(state.ActionApplications), "tombstoneRecords": 2, "mutationAttemptsPerKind": totalAttemptsPerKind})
}
