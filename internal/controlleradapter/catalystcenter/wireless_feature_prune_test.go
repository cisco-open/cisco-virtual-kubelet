// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func featurePruneModel(enabled bool) []byte {
	return []byte(fmt.Sprintf(`{"catalyst_center":{"feature_templates":{"wireless":{"cleanair":[{"name":"CA","radio_band":"2.4GHz","enable":%t}],"rrm_fra":[{"name":"FRA","radio_band":"2.4GHz_5GHz","fra_status":%t}]}}}}`, enabled, enabled))
}

type featurePruneTask struct {
	ID, Name, Action string
	At               int64
}
type featurePruneWorld struct {
	rows     map[string]map[string]any
	tasks    map[string]featurePruneTask
	profiles []map[string]any
	writes   []string
	mode     string
	onTask   func(string)
	creates  map[string]int
}

func (w *featurePruneWorld) serve(t *testing.T, rw http.ResponseWriter, r *http.Request) {
	t.Helper()
	respond := func(value any) {
		if err := json.NewEncoder(rw).Encode(value); err != nil {
			t.Error(err)
		}
	}
	if r.URL.Path == authPath {
		fmt.Fprint(rw, `{"Token":"test"}`)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == wirelessProfilesPath {
		respond(map[string]any{"response": w.profiles})
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == wirelessFeatureSummaryPath {
		kind := r.URL.Query().Get("type")
		rows := []any{}
		for id, row := range w.rows {
			if row["kind"] == kind {
				rows = append(rows, map[string]any{"id": id, "designName": row["designName"], "systemTemplate": row["_system"] == true})
			}
		}
		respond(map[string]any{"response": []any{map[string]any{"type": kind, "count": len(rows), "instances": rows}}})
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, taskPath) {
		taskID := strings.TrimPrefix(r.URL.Path, taskPath)
		if w.onTask != nil {
			w.onTask(taskID)
		}
		task, ok := w.tasks[taskID]
		if !ok {
			t.Error("unretained task lookup", taskID)
			rw.WriteHeader(404)
			return
		}
		progress := "Successfully " + map[string]string{"Create": "created", "Update": "updated", "Delete": "deleted"}[task.Action] + " model config design with name " + task.Name
		if task.Action == "Delete" {
			progress = "Successfully deleted model config design with id " + task.ID
		}
		response := map[string]any{"id": taskID, "startTime": task.At, "endTime": task.At, "isError": task.Action == "Delete" && w.mode == "failed", "serviceType": "NCNC", "data": task.ID, "progress": progress}
		if task.Action == "Delete" && w.mode == "foreign-result" {
			response["data"] = featureTestCAUUID
			if task.ID == featureTestCAUUID {
				response["data"] = featureTestFRAUUID
			}
		}
		respond(map[string]any{"response": response})
		return
	}
	kind, path, id := wirelessFRAKind, wirelessFRAPath, featureTestFRAUUID
	if strings.HasPrefix(r.URL.Path, wirelessCleanAirPath) {
		kind, path, id = wirelessCleanAirKind, wirelessCleanAirPath, featureTestCAUUID
	}
	if r.URL.Path != path && !strings.HasPrefix(r.URL.Path, path+"/") {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		rw.WriteHeader(404)
		return
	}
	if r.Method == http.MethodGet {
		selected := strings.TrimPrefix(r.URL.Path, path+"/")
		row, exists := w.rows[selected]
		if !exists {
			rw.WriteHeader(404)
			return
		}
		detail := map[string]any{}
		for k, v := range row {
			if k != "kind" && k != "_system" {
				detail[k] = v
			}
		}
		respond(map[string]any{"response": detail})
		return
	}
	action := "Create"
	if r.Method == http.MethodPut {
		action = "Update"
	}
	if r.Method == http.MethodDelete {
		action = "Delete"
	}
	name := ""
	if action == "Update" {
		id = strings.TrimPrefix(r.URL.Path, path+"/")
	}
	if action == "Create" {
		if w.creates == nil {
			w.creates = map[string]int{}
		}
		w.creates[kind]++
		if w.creates[kind] > 1 && w.mode != "reused-uuid" {
			id = fmt.Sprintf("33333333-3333-4333-8333-%012d", w.creates[kind])
			if kind == wirelessFRAKind {
				id = fmt.Sprintf("44444444-4444-4444-8444-%012d", w.creates[kind])
			}
		}
	}
	if action == "Delete" {
		id = strings.TrimPrefix(r.URL.Path, path+"/")
		row, ok := w.rows[id]
		if !ok {
			t.Error("deletion replay", id)
			rw.WriteHeader(404)
			return
		}
		name, _ = row["designName"].(string)
		if w.mode != "failed" {
			delete(w.rows, id)
		}
	} else {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid body")
			rw.WriteHeader(400)
			return
		}
		name, _ = body["designName"].(string)
		body["id"] = id
		body["kind"] = kind
		w.rows[id] = body
	}
	w.writes = append(w.writes, action+":"+id)
	taskID := fmt.Sprint("feature-prune-task-", len(w.writes))
	w.tasks[taskID] = featurePruneTask{ID: id, Name: name, Action: action, At: time.Now().UnixMilli()}
	if action == "Delete" && w.mode == "lost-ack" || action == "Create" && w.mode == "lost-create-ack" && w.creates[kind] > 1 {
		fmt.Fprint(rw, `{}`)
		return
	}
	respond(map[string]any{"response": Task{ID: taskID}})
}
func featurePruneFixture(t *testing.T, install func(*adapter, *configapi.NetworkControllerConfig)) (*adapter, *configapi.NetworkControllerConfig, *featurePruneWorld) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"feature_templates"}
	config.Spec.Source.Inline.Raw = featurePruneModel(false)
	if install != nil {
		install(a, config)
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	world := &featurePruneWorld{rows: map[string]map[string]any{}, tasks: map[string]featurePruneTask{}, profiles: []map[string]any{}}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) { world.serve(t, w, r) })
	key := ctrlclient.ObjectKeyFromObject(config)
	for _, enabled := range []bool{false, true} {
		config.Generation++
		config.Spec.Source.Inline.Raw = featurePruneModel(enabled)
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		converged := false
		for range 40 {
			restart := *a
			if _, err := (&configReconciler{a: &restart}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				converged = true
				break
			}
		}
		if !converged {
			t.Fatal("prune fixture did not converge", config.Status)
		}
	}
	if len(world.writes) != 4 || len(journalState(t, a).ActionApplications) != 4 {
		t.Fatal("missing creator/update evidence", world.writes)
	}
	return a, config, world
}
func TestWirelessFeaturePublicPrune(t *testing.T) {
	for _, mode := range []string{"prune", "imports", "retain", "referenced", "incomplete-profiles", "native-drift", "system", "missing", "replacement", "legacy-no-state", "capacity", "cohort-capacity", "missing-task", "lost-ack", "failed", "foreign-result", "tombstone-missing", "pruned", "publish-owner", "publish-source"} {
		t.Run(mode, func(t *testing.T) { exerciseFeaturePublicPrune(t, mode, nil) })
	}
}
func exerciseFeaturePublicPrune(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config, world := featurePruneFixture(t, install)
	world.mode = mode
	switch mode {
	case "imports":
		for id, name := range map[string]string{"11111111-1111-4111-8111-111111111111": "IMPORT", "22222222-2222-4222-8222-222222222222": "FOREIGN"} {
			world.rows[id] = map[string]any{"id": id, "kind": wirelessCleanAirKind, "designName": name, "featureAttributes": map[string]any{"radioBand": "2_4GHZ", "cleanAir": false, "cleanAirDeviceReporting": false, "persistentDevicePropagation": false}}
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		foreign := nc.Status.Configuration.ActionApplications[0]
		foreign.Operation = *foreign.Operation.DeepCopy()
		foreign.Operation.ConfigUID = "foreign"
		foreign.Operation.ConfigName = "foreign"
		foreign.Operation.ID = strings.Repeat("f", 32)
		foreign.Operation.Resource = featureCreatePrefix + "ca:" + featureIdentityDigest("FOREIGN") + ":" + featureIdentityDigest("foreign-effect")
		key, _ := featureCreateKey(foreign.Operation.Resource)
		foreign.Resource = featureCreatedPrefix + key + ":22222222-2222-4222-8222-222222222222"
		nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, foreign)
		if err = a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	case "referenced":
		world.profiles = []map[string]any{{"id": "profile", "wirelessProfileName": "P", "featureTemplates": []any{map[string]any{"id": featureTestCAUUID, "ssids": []any{}}}}}
	case "incomplete-profiles":
		world.profiles = []map[string]any{{"id": "profile", "wirelessProfileName": "P", "futureField": true}}
	case "system":
		world.rows[featureTestCAUUID]["_system"] = true
	case "native-drift":
		world.rows[featureTestCAUUID]["futureNativeField"] = true
	case "missing":
		delete(world.rows, featureTestCAUUID)
	case "replacement":
		world.rows["11111111-1111-4111-8111-111111111111"] = world.rows[featureTestCAUUID]
		delete(world.rows, featureTestCAUUID)
		world.rows["11111111-1111-4111-8111-111111111111"]["id"] = "11111111-1111-4111-8111-111111111111"
	case "capacity", "cohort-capacity", "legacy-no-state":
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "capacity" || mode == "cohort-capacity" {
			limit := 256
			if mode == "cohort-capacity" {
				limit = 255
			}
			for len(nc.Status.Configuration.ActionApplications) < limit {
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: fmt.Sprint("other-", len(nc.Status.Configuration.ActionApplications)), Operation: cvk.NetworkControllerConfigurationOperation{ID: fmt.Sprintf("%032x", len(nc.Status.Configuration.ActionApplications)+1), ConfigUID: "other", ConfigName: "other", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Resource: "other", Action: "Create", Phase: "Verified"}})
			}
		} else {
			for i := range nc.Status.Configuration.ActionApplications {
				r := &nc.Status.Configuration.ActionApplications[i]
				r.Resource = r.Resource[:strings.LastIndex(r.Resource, ":")]
			}
		}
		if err = a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	config.Generation++
	config.Spec.Source.Inline.Raw = []byte(featurePruneEmpty)
	config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	if mode == "retain" {
		config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	key := ctrlclient.ObjectKeyFromObject(config)
	recovered := map[string]bool{}
	raced := false
	world.onTask = func(taskID string) {
		if raced || (mode != "publish-owner" && mode != "publish-source") {
			return
		}
		current, err := a.currentController(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		op := current.Status.Configuration.Operation
		if op == nil || op.Action != "Delete" || op.Phase != "Verifying" || op.TaskID != taskID {
			return
		}
		raced = true
		if mode == "publish-owner" {
			current.Status.Configuration.Owners[0].ConfigUID = "foreign"
			if err = a.statusWriter.Status().Update(ctx, current); err != nil {
				t.Error(err)
			}
		} else {
			latest := &configapi.NetworkControllerConfig{}
			if err = a.statusReader.Get(ctx, key, latest); err != nil {
				t.Error(err)
				return
			}
			latest.Generation++
			latest.Spec.Source.Inline.Raw = []byte(strings.Replace(featurePruneEmpty, `"cleanair":[],`, "", 1))
			if err = a.statusWriter.Update(ctx, latest); err != nil {
				t.Error(err)
			}
		}
	}
	corrupted := false
	for range 55 {
		restart := *a
		_, err := (&configReconciler{a: &restart}).Reconcile(ctx, ctrl.Request{NamespacedName: key})
		if err != nil && !(raced && mode == "publish-source" && apierrors.IsConflict(err)) {
			t.Fatal(err)
		}
		if err = a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state := nc.Status.Configuration
		if state != nil && state.Operation != nil && state.Operation.Action == "Delete" {
			op := state.Operation
			if op.Phase == "Accepted" && !recovered[op.ID] {
				op.Phase = "Unknown"
				if mode == "missing-task" {
					op.TaskID = ""
				}
				raw, _ := json.Marshal(nc)
				restored := &cvk.NetworkController{}
				if err = json.Unmarshal(raw, restored); err != nil {
					t.Fatal(err)
				}
				if err = a.statusWriter.Status().Update(ctx, restored); err != nil {
					t.Fatal(err)
				}
				recovered[op.ID] = true
			}
			if op.Phase == "Verifying" {
				if err := a.advanceConfiguration(ctx, op.ID, "Verified", ""); err != errConfigurationTransition {
					t.Fatal("generic metadata verification bypassed delete proof", err)
				}
			}
			if mode == "pruned" && op.Phase == "Verifying" {
				a.statusWriter = pruningActionReceiptClient{Client: a.statusWriter}
			}
			if mode == "tombstone-missing" && op.Phase == "Verified" && !corrupted {
				rows := state.ActionApplications[:0]
				for _, r := range state.ActionApplications {
					if !strings.HasPrefix(r.Resource, featureDeletedPrefix) {
						rows = append(rows, r)
					}
				}
				state.ActionApplications = rows
				if err = a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				corrupted = true
			}
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
			break
		}
	}
	deletes := len(world.writes) - 4
	state := journalState(t, a)
	if mode == "prune" || mode == "imports" {
		wantRows, wantNative := 6, 0
		if mode == "imports" {
			wantRows, wantNative = 7, 2
		}
		if mode == "imports" {
			if world.rows["11111111-1111-4111-8111-111111111111"]["designName"] != "IMPORT" || world.rows["22222222-2222-4222-8222-222222222222"]["designName"] != "FOREIGN" {
				t.Fatal("import or foreign owner was modified")
			}
		}
		tombstones, err := featureDeletedRecords(state)
		if err != nil || len(tombstones) != 2 || len(state.ActionApplications) != wantRows || len(world.rows) != wantNative || deletes != 2 || len(recovered) != 2 || state.Operation != nil || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("pruning/recovery did not converge", err, world.writes, state, config.Status)
		}
	} else if mode == "retain" {
		if deletes != 0 || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("retain changed native definitions", world.writes, config.Status)
		}
	} else {
		expected := 0
		switch mode {
		case "missing-task", "lost-ack", "failed", "foreign-result", "tombstone-missing", "pruned", "publish-owner", "publish-source":
			expected = 1
		}
		if deletes != expected || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("unsafe prune proceeded", mode, world.writes, config.Status)
		}
		if expected == 1 && (state.Operation == nil || featureDeletedReceiptPresent(state, state.Operation)) {
			t.Fatal("unproven delete released", mode, state)
		}
	}
}

func TestWirelessFeaturePruneFinalDispatchFence(t *testing.T) {
	for _, mode := range []string{"stable", "peer-change", "late-reference", "late-receipt", "late-owner", "late-source", "paused", "unrelated-profile"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, world := featurePruneFixture(t, nil)
			config.Generation++
			config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
			config.Spec.Source.Inline.Raw = []byte(featurePruneEmpty)
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
			if err != nil || write == nil || write.action != "Delete" {
				t.Fatal("missing prune plan", err)
			}
			op, err := a.prepareConfiguration(ctx, config, source, "feature_templates", write.resource, write.action, write.identity)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.startConfiguration(ctx, config, source, op.ID); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "peer-change":
				world.rows[featureTestFRAUUID]["futureNativeField"] = true
			case "late-reference":
				world.profiles = []map[string]any{{"id": "profile", "wirelessProfileName": "P", "featureTemplates": []any{map[string]any{"id": featureTestCAUUID, "ssids": []any{}}}}}
			case "unrelated-profile":
				world.profiles = []map[string]any{{"id": "profile", "wirelessProfileName": "P", "featureTemplates": []any{}}}
			case "late-source":
				config.Generation++
				if err = a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case "late-receipt", "late-owner", "paused":
				latest, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "late-owner" {
					latest.Status.Configuration.Owners[0].ConfigUID = "foreign"
				}
				if mode == "late-receipt" {
					latest.Status.Configuration.ActionApplications = latest.Status.Configuration.ActionApplications[1:]
				}
				if mode == "paused" {
					latest.Spec.Paused = true
					err = a.statusWriter.Update(ctx, latest)
				} else {
					err = a.statusWriter.Status().Update(ctx, latest)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err = write.submit(ctx)
			allowed := mode == "stable" || mode == "unrelated-profile"
			if allowed {
				if err != nil || len(world.writes) != 5 {
					t.Fatal("stable prune refused", err, world.writes)
				}
			} else if err == nil || len(world.writes) != 4 {
				t.Fatal("stale prepared prune escaped", mode, err, world.writes)
			}
			count := len(world.writes)
			if _, err = write.submit(ctx); err == nil || len(world.writes) != count {
				t.Fatal("consumed delete permit replayed", mode, err)
			}
		})
	}
}
