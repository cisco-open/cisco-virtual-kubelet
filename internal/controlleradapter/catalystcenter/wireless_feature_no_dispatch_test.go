// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestWirelessFeatureNoDispatch(t *testing.T) {
	for _, stage := range []string{"create", "import-update", "update", "prune", "recreate", "finalize"} {
		for _, mode := range []string{"selected", "companion", "lost-ack"} {
			t.Run(stage+"/"+mode, func(t *testing.T) { exerciseWirelessFeatureNoDispatch(t, stage, mode, nil) })
		}
	}
}
func exerciseWirelessFeatureNoDispatch(t *testing.T, stage, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	var world *featurePruneWorld
	if stage == "create" || stage == "import-update" {
		config.Spec.ManagedSections = []string{"feature_templates"}
		config.Spec.Source.Inline.Raw = featurePruneModel(false)
		if install != nil {
			install(a, config)
		}
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		world = &featurePruneWorld{rows: map[string]map[string]any{}, tasks: map[string]featurePruneTask{}, profiles: []map[string]any{}}
	} else {
		a, config, world = featurePruneFixture(t, install)
	}
	key := ctrlclient.ObjectKeyFromObject(config)
	admin := a.statusWriter
	tick := func() ctrl.Result {
		t.Helper()
		restarted := *a
		result, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key})
		if err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if stage == "recreate" {
		config.Generation++
		config.Spec.Source.Inline.Raw = []byte(featurePruneEmpty)
		config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
		if err := admin.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		done := false
		for range 50 {
			tick()
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				done = true
				break
			}
		}
		if !done || len(world.writes) != 6 {
			t.Fatal("recreation prerequisite incomplete")
		}
	}
	if err := a.statusReader.Get(ctx, key, config); err != nil {
		t.Fatal(err)
	}
	config.Generation++
	switch stage {
	case "create":
		config.Spec.Source.Inline.Raw = featurePruneModel(false)
	case "import-update":
		config.Spec.Source.Inline.Raw = featurePruneModel(true)
	case "update", "recreate":
		config.Spec.Source.Inline.Raw = featurePruneModel(false)
	case "prune":
		config.Spec.Source.Inline.Raw = []byte(featurePruneEmpty)
		config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	case "finalize":
		config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
		config.Finalizers = append(config.Finalizers, featureConfigFinalizer)
	}
	if err := admin.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if stage == "finalize" {
		if err := admin.Delete(ctx, config); err != nil {
			t.Fatal(err)
		}
		a.statusWriter = statusOnlyFeatureClient{Client: admin}
	}
	beforeWrites := len(world.writes)
	var beforeRecords []cvk.NetworkControllerConfigurationActionApplication
	if state := journalState(t, a); state != nil {
		beforeRecords = state.DeepCopy().ActionApplications
	}
	injected := map[string]bool{}
	witnessed := map[string]bool{}
	pending := ""
	active := stage != "import-update"
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if active && mode == "lost-ack" && r.Method != http.MethodGet && r.URL.Path != authPath {
			reply := httptest.NewRecorder()
			world.serve(t, reply, r)
			fmt.Fprint(w, `{}`)
			return
		}
		if active && mode != "lost-ack" && r.Method == http.MethodGet && r.URL.Path == wirelessFeatureSummaryPath {
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			if nc.Status.Configuration != nil {
				op := nc.Status.Configuration.Operation
				if op != nil && op.Phase == "Submitting" && noDispatchOperationSupported(op) {
					selected := wirelessCleanAirKind
					if strings.Contains(op.Resource, ":fra:") || strings.HasPrefix(op.Resource, "rrm_fra/") || strings.HasPrefix(op.Resource, "rrm-fra:") {
						selected = wirelessFRAKind
					}
					kind := selected
					if mode == "companion" {
						if kind == wirelessFRAKind {
							kind = wirelessCleanAirKind
						} else {
							kind = wirelessFRAKind
						}
					}
					identity := fmt.Sprint(op.ConfigGeneration, "/", op.Action, "/", op.Resource)
					if r.URL.Query().Get("type") == kind && !injected[identity] {
						injected[identity] = true
						pending = identity
						w.WriteHeader(503)
						return
					}
				}
			}
		}
		world.serve(t, w, r)
	})
	if stage == "import-update" {
		private, err := a.client.compileConfigurationExecutor(featurePruneModel(false), "feature_templates")
		if err != nil {
			t.Fatal(err)
		}
		for range 3 {
			write, err := private.next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if write == nil {
				break
			}
			if _, err = write.submit(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if len(world.rows) != 2 || len(world.writes) != 2 {
			t.Fatal("unowned native prerequisites missing")
		}
		beforeWrites = len(world.writes)
		active = true
	}
	complete := false
	for range 100 {
		result := tick()
		nc := mustFeatureController(t, a)
		for _, c := range config.Status.Conditions {
			if c.Type == "Ready" && c.Reason == "NativePreflightRefused" && pending != "" {
				if result.RequeueAfter <= 0 || nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil || len(nc.Status.Configuration.Owners) != 1 {
					t.Fatal("proof did not retire exact work")
				}
				if len(world.writes) != beforeWrites+len(witnessed) {
					t.Fatal("refused action mutated native service", world.writes)
				}
				// Every successful earlier action changes receipts; the refused operation
				// itself may not fabricate or truncate any lifecycle evidence.
				if len(witnessed) == 0 && !reflect.DeepEqual(beforeRecords, nc.Status.Configuration.ActionApplications) {
					t.Fatal("first refusal changed original history")
				}
				witnessed[pending] = true
				pending = ""
			}
		}
		if mode == "lost-ack" && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Unknown" {
			for range 3 {
				tick()
			}
			break
		}
		if stage == "finalize" {
			complete = featureDeletionCompleted(nc, config)
		} else {
			complete = config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation
		}
		if complete {
			break
		}
	}
	state := journalState(t, a)
	if stage == "import-update" && len(state.ActionApplications) != 0 {
		t.Fatal("unowned import gained creator/applied ownership")
	}
	if mode == "lost-ack" {
		if complete || len(world.writes) != beforeWrites+1 || state.Operation == nil || state.Operation.Phase != "Unknown" || state.Operation.TaskID != "" {
			t.Fatal("uncertain native request was replayed/released")
		}
		return
	}
	if !complete || len(injected) != 2 || len(witnessed) != 2 || len(world.writes) != beforeWrites+2 || state.Operation != nil {
		t.Fatal("feature automatic no-dispatch incomplete", stage, mode, len(injected), len(witnessed), world.writes, config.Status)
	}
	if stage == "prune" || stage == "finalize" {
		if len(world.rows) != 0 || len(state.ActionApplications) != 6 {
			t.Fatal("cleanup history lost")
		}
	} else if len(world.rows) != 2 {
		t.Fatal("native objects missing")
	}
}
