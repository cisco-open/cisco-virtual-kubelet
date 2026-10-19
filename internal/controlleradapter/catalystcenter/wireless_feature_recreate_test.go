// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type pruningFeatureArchiveClient struct{ ctrlclient.Client }

func (c pruningFeatureArchiveClient) Status() ctrlclient.SubResourceWriter {
	return pruningFeatureArchiveWriter{SubResourceWriter: c.Client.Status()}
}

type pruningFeatureArchiveWriter struct{ ctrlclient.SubResourceWriter }

func (w pruningFeatureArchiveWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if nc, ok := obj.(*cvk.NetworkController); ok && nc.Status.Configuration != nil {
		rows := nc.Status.Configuration.ActionApplications[:0]
		for _, r := range nc.Status.Configuration.ActionApplications {
			if !featureRetiredSelected(r.Resource) {
				rows = append(rows, r)
			}
		}
		nc.Status.Configuration.ActionApplications = rows
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func driveFeatureRecreate(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, raw []byte, mode string) (bool, int) {
	t.Helper()
	ctx := context.Background()
	config.Generation++
	config.Spec.Source.Inline.Raw = raw
	config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	if mode == "retain-recreate" {
		config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	key := ctrlclient.ObjectKeyFromObject(config)
	injected := map[string]bool{}
	for range 65 {
		restart := *a
		_, err := (&configReconciler{a: &restart}).Reconcile(ctx, ctrl.Request{NamespacedName: key})
		if err != nil && !(mode == "publish-source" && apierrors.IsConflict(err)) {
			t.Fatal(err)
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
			op := nc.Status.Configuration.Operation
			if op.Phase == "Accepted" && !injected[op.ID] {
				op.Phase = "Unknown"
				if featureRecreateSelected(op.Resource) && mode == "missing-task" {
					op.TaskID = ""
				}
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
				injected[op.ID] = true
			}
			if op.Phase == "Verifying" && featureRecreateSelected(op.Resource) {
				if err = a.advanceConfiguration(ctx, op.ID, "Verified", ""); err != errConfigurationTransition {
					t.Fatal("generic Verified bypassed recreation lineage", err)
				}
				if mode == "archive-pruned" {
					a.statusWriter = pruningFeatureArchiveClient{Client: a.statusWriter}
				}
			}
		}
		if err = a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
			return true, len(injected)
		}
	}
	return false, len(injected)
}
func TestWirelessFeaturePublicRecreation(t *testing.T) {
	for _, mode := range []string{"known", "retain-recreate", "occupied", "old-id-alias", "missing-tombstone", "foreign-owner", "capacity", "missing-task", "lost-create-ack", "reused-uuid", "archive-pruned", "publish-owner", "publish-source"} {
		t.Run(mode, func(t *testing.T) { exerciseFeaturePublicRecreation(t, mode, nil) })
	}
}
func exerciseFeaturePublicRecreation(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config, world := featurePruneFixture(t, install)
	if ok, recovered := driveFeatureRecreate(t, a, config, []byte(featurePruneEmpty), ""); !ok || recovered != 2 {
		t.Fatal("initial prune failed", config.Status)
	}
	initial := journalState(t, a).DeepCopy().ActionApplications
	if len(world.writes) != 6 || len(initial) != 6 {
		t.Fatal("missing initial lifecycle")
	}
	world.mode = mode
	switch mode {
	case "occupied":
		world.rows["11111111-1111-4111-8111-111111111111"] = map[string]any{"id": "11111111-1111-4111-8111-111111111111", "kind": wirelessCleanAirKind, "designName": "CA"}
	case "old-id-alias":
		world.rows[featureTestCAUUID] = map[string]any{"id": featureTestCAUUID, "kind": wirelessCleanAirKind, "designName": "RENAMED"}
	case "missing-tombstone", "foreign-owner", "capacity":
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state := nc.Status.Configuration
		switch mode {
		case "missing-tombstone":
			rows := state.ActionApplications[:0]
			for _, r := range state.ActionApplications {
				if r.Operation.TargetID != featureTestCAUUID || !featureDeleteSelected(r.Operation.Resource) {
					rows = append(rows, r)
				}
			}
			state.ActionApplications = rows
		case "foreign-owner":
			for i := range state.ActionApplications {
				state.ActionApplications[i].Operation.ConfigUID = "foreign"
				state.ActionApplications[i].Operation.ConfigName = "foreign"
			}
		case "capacity":
			for len(state.ActionApplications) < 255 {
				n := len(state.ActionApplications)
				state.ActionApplications = append(state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: fmt.Sprint("other-", n), Operation: cvk.NetworkControllerConfigurationOperation{ID: fmt.Sprintf("%032x", n+1), ConfigUID: "other", ConfigName: "other", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Resource: "other", Action: "Create", Phase: "Verified"}})
			}
		}
		if err = a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	raced := false
	world.onTask = func(task string) {
		if raced || mode != "publish-owner" && mode != "publish-source" {
			return
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		op := nc.Status.Configuration.Operation
		if op == nil || !featureRecreateSelected(op.Resource) || op.Phase != "Verifying" || op.TaskID != task {
			return
		}
		raced = true
		if mode == "publish-owner" {
			nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			if err = a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Error(err)
			}
		} else {
			latest := &configapi.NetworkControllerConfig{}
			if err = a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), latest); err != nil {
				t.Error(err)
				return
			}
			latest.Generation++
			latest.Spec.Source.Inline.Raw = featurePruneModel(true)
			if err = a.statusWriter.Update(ctx, latest); err != nil {
				t.Error(err)
			}
		}
	}
	ok, recovered := driveFeatureRecreate(t, a, config, featurePruneModel(false), mode)
	state := journalState(t, a)
	if mode != "known" && mode != "retain-recreate" {
		expected := 6
		switch mode {
		case "missing-task", "lost-create-ack", "reused-uuid", "archive-pruned", "publish-owner", "publish-source":
			expected = 7
		}
		if ok || len(world.writes) != expected || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("unsafe recreation advanced", mode, ok, world.writes, config.Status)
		}
		if expected == 7 && (state.Operation == nil || featureCreatedReceiptPresent(state, state.Operation)) {
			t.Fatal("unproven recreation released", mode, state)
		}
		return
	}
	if !ok || recovered != 2 || len(world.writes) != 8 {
		t.Fatal("recreation did not converge once", ok, recovered, world.writes, config.Status)
	}
	if mode == "retain-recreate" {
		cohorts, err := featureRetiredCohorts(state)
		if err != nil || len(cohorts) != 2 || len(state.ActionApplications) != 8 || len(world.rows) != 2 {
			t.Fatal("Retain recreation lost ownership/history", err)
		}
		return
	}
	for cycle := 1; cycle <= 2; cycle++ {
		if cycle > 1 {
			if ok, n := driveFeatureRecreate(t, a, config, featurePruneModel(false), ""); !ok || n != 2 {
				t.Fatal("second recreation failed", config.Status)
			}
		}
		creators, err := featureCreatedRecords(journalState(t, a))
		if err != nil || len(creators) != 2 {
			t.Fatal("missing new creators", err)
		}
		for key, creator := range creators {
			if !featureRecreateSelected(creator.Operation.Resource) || creator.Operation.ConfigGeneration != config.Generation {
				t.Fatal("old creator reused", creator)
			}
			id, _ := featureCreatedTarget(creator)
			if id == featureTestCAUUID || id == featureTestFRAUUID {
				t.Fatal("old UUID adopted", key, id)
			}
		}
		if ok, n := driveFeatureRecreate(t, a, config, featurePruneModel(true), ""); !ok || n != 2 {
			t.Fatal("recreated update failed", config.Status)
		}
		if ok, n := driveFeatureRecreate(t, a, config, []byte(featurePruneEmpty), ""); !ok || n != 2 {
			t.Fatal("recreated prune failed", config.Status)
		}
		cohorts, err := featureRetiredCohorts(journalState(t, a))
		if err != nil || len(cohorts) != 2*cycle {
			t.Fatal("retired lineage missing", cycle, len(cohorts), err)
		}
	}
	state = journalState(t, a)
	if len(world.writes) != 18 || len(world.rows) != 0 || len(state.ActionApplications) != 18 || state.Operation != nil {
		t.Fatal("unbounded/replayed lifecycle", world.writes, len(state.ActionApplications))
	}
	for _, original := range initial {
		found := false
		for _, r := range state.ActionApplications {
			if r.Resource == featureRetiredPrefix+original.Resource && reflect.DeepEqual(r.Operation, original.Operation) && r.VerifiedAt.Equal(original.VerifiedAt) {
				found = true
			}
		}
		if !found {
			t.Fatal("original lifecycle history lost", original.Resource)
		}
	}
	// Mutating any archive member, removing it, or corrupting the explicit parent
	// binding invalidates the chain even though the current tombstones remain.
	for _, corruption := range []string{"missing", "changed", "duplicate", "parent"} {
		t.Run(corruption, func(t *testing.T) {
			copy := state.DeepCopy()
			index := -1
			for i, r := range copy.ActionApplications {
				if featureRetiredSelected(r.Resource) {
					index = i
					break
				}
			}
			if index < 0 {
				t.Fatal("missing archive fixture")
			}
			switch corruption {
			case "missing":
				copy.ActionApplications = append(copy.ActionApplications[:index], copy.ActionApplications[index+1:]...)
			case "changed":
				copy.ActionApplications[index].Operation.SourceVersion += "-changed"
			case "duplicate":
				copy.ActionApplications = append(copy.ActionApplications, copy.ActionApplications[index])
			case "parent":
				for i := range copy.ActionApplications {
					r := &copy.ActionApplications[i]
					if !featureRetiredSelected(r.Resource) && featureRecreateSelected(r.Operation.Resource) {
						parts := strings.Split(r.Operation.Resource, ":")
						parts[len(parts)-2] = strings.Repeat("a", 32)
						r.Operation.Resource = strings.Join(parts, ":")
						break
					}
				}
			}
			if _, err := featureRetiredCohorts(copy); err == nil {
				t.Fatal("corrupt historical lineage accepted", corruption)
			}
		})
	}
	count := len(world.writes)
	for range 3 {
		restart := *a
		if _, err := (&configReconciler{a: &restart}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(world.writes) != count {
		t.Fatal("same-input tombstone replayed")
	}
}

func TestFeatureRecreationRequiresLaterInput(t *testing.T) {
	a, config, _ := featurePruneFixture(t, nil)
	if ok, _ := driveFeatureRecreate(t, a, config, []byte(featurePruneEmpty), ""); !ok {
		t.Fatal("prune failed")
	}
	state := journalState(t, a)
	deleted, err := featureDeletedRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	b := featureCreateBinding{resource: featureCreatePrefix + "fra:" + featureIdentityDigest("FRA") + ":" + featureIdentityDigest("new-effect")}
	resource := featureRecreateIntent(b, state)
	prior := deleted[b.key()]
	op := prior.Operation.DeepCopy()
	op.Resource = resource
	op.Action = "Create"
	op.TargetID = ""
	op.TaskID = ""
	op.SubmittedAt = nil
	if err := featureRecreateAuthority(state, op); err == nil {
		t.Fatal("same deleted input recreated")
	}
	later := op.DeepCopy()
	later.ConfigGeneration++
	if err := featureRecreateAuthority(state, later); err != nil {
		t.Fatal("later generation refused", err)
	}
	other := state.DeepCopy()
	for _, resource := range []string{"other-a", "other-b"} {
		other.ActionApplications = append(other.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: resource, Operation: cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("f", 32), Section: "inventory", Resource: resource, ConfigUID: "other"}})
	}
	if err := featureRecreateAuthority(other, later); err != nil {
		t.Fatal("feature lineage imposed rules on unrelated action protocols", err)
	}
	changed := op.DeepCopy()
	changed.SourceVersion += "-new"
	if err := featureRecreateAuthority(state, changed); err != nil {
		t.Fatal("new source version refused", err)
	}
	for _, mode := range []string{"foreign", "parent", "digest"} {
		t.Run(mode, func(t *testing.T) {
			copy := later.DeepCopy()
			switch mode {
			case "foreign":
				copy.ConfigUID = "foreign"
			case "parent":
				copy.Resource = strings.Replace(copy.Resource, prior.Operation.ID, strings.Repeat("a", 32), 1)
			case "digest":
				parts := strings.Split(copy.Resource, ":")
				parts[len(parts)-1] = strings.Repeat("b", 64)
				copy.Resource = strings.Join(parts, ":")
			}
			if err := featureRecreateAuthority(state, copy); err == nil {
				t.Fatal("unbound recreation permitted", mode)
			}
		})
	}
}
func TestWirelessFeatureRecreateFinalDispatchFence(t *testing.T) {
	for _, mode := range []string{"stable", "old-id-alias", "late-tombstone", "late-source", "late-owner"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, world := featurePruneFixture(t, nil)
			if ok, _ := driveFeatureRecreate(t, a, config, []byte(featurePruneEmpty), ""); !ok {
				t.Fatal("prune failed")
			}
			config.Generation++
			config.Spec.Source.Inline.Raw = featurePruneModel(false)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := a.client.compileConfigurationApply(source.raw, config.Spec.ManagedSections, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.bindFeatureCreateOwnership(nc, config, source, &plan); err != nil {
				t.Fatal(err)
			}
			if err = a.bindFeaturePrune(nc, config, source, &plan); err != nil {
				t.Fatal(err)
			}
			write, err := plan.executor.next(ctx)
			if err != nil || write == nil || !featureRecreateSelected(write.resource) {
				t.Fatal("missing recreation plan", err)
			}
			op, err := a.prepareConfiguration(ctx, config, source, "feature_templates", write.resource, write.action, write.identity)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.startConfiguration(ctx, config, source, op.ID); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "old-id-alias":
				world.rows[featureTestFRAUUID] = map[string]any{"id": featureTestFRAUUID, "kind": wirelessFRAKind, "designName": "RENAMED"}
			case "late-source":
				config.Generation++
				config.Spec.Source.Inline.Raw = featurePruneModel(true)
				if err = a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case "late-tombstone", "late-owner":
				latest, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "late-owner" {
					latest.Status.Configuration.Owners[0].ConfigUID = "foreign"
				} else {
					for i := range latest.Status.Configuration.ActionApplications {
						r := &latest.Status.Configuration.ActionApplications[i]
						if featureDeleteSelected(r.Operation.Resource) {
							r.Operation.SourceVersion += "-changed"
						}
					}
				}
				if err = a.statusWriter.Status().Update(ctx, latest); err != nil {
					t.Fatal(err)
				}
			}
			_, err = write.submit(ctx)
			if mode == "stable" {
				if err != nil || len(world.writes) != 7 {
					t.Fatal("valid recreation refused", err, world.writes)
				}
			} else if err == nil || len(world.writes) != 6 {
				t.Fatal("stale recreation dispatched", mode, err, world.writes)
			}
			count := len(world.writes)
			if _, err = write.submit(ctx); err == nil || len(world.writes) != count {
				t.Fatal("recreation permit replayed", mode, err)
			}
		})
	}
}
