// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const featureTestFRAUUID = "b81aaf99-cdcd-4932-8148-49244eff1c46"
const featureTestCAUUID = "c01658ad-8573-4368-b91a-71aa2d666239"

func TestWirelessFeatureRetainedUpdateRecovery(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		for _, mode := range []string{"known", "known-create", "missing-task", "create", "missing-create-task", "source", "foreign-owner", "publish-owner", "publish-source", "publish-update-owner", "publish-update-source", "replacement", "failed", "pending", "foreign-task", "stale-task"} {
			t.Run(fmt.Sprint(mixed)+"/"+mode, func(t *testing.T) { exerciseWirelessFeatureRecovery(t, mixed, mode, nil) })
		}
	}
}

func exerciseWirelessFeatureRecovery(t *testing.T, mixed bool, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"feature_templates"}
	model := func(revision int) []byte {
		fra := map[string]any{"name": "FRA", "radio_band": "2.4GHz_5GHz", "fra_interval": 3, "fra_status": false}
		ca := map[string]any{"name": "CA", "radio_band": "2.4GHz", "enable": false}
		if revision == 1 {
			fra["fra_interval"] = 6
			fra["fra_status"] = true
			ca["radio_band"] = "5GHz"
			ca["enable"] = true
		}
		if revision == 2 {
			fra["radio_band"] = "5GHz_6GHz"
			fra["fra_interval"] = 24
			ca["radio_band"] = "6GHz"
			ca["enable"] = true
		}
		wireless := map[string]any{"rrm_fra": []any{fra}}
		if mixed {
			wireless["cleanair"] = []any{ca}
		}
		raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"feature_templates": map[string]any{"wireless": wireless}}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	config.Spec.Source.Inline.Raw = model(0)
	if install != nil {
		install(a, config)
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	var fra *wirelessFRA
	var ca *wirelessCleanAir
	writes := 0
	tasks := map[string]int64{}
	results := map[string][3]string{}
	poisoned := false
	publicationRaced := false
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == wirelessFeatureSummaryPath && r.Method == http.MethodGet:
			kind := r.URL.Query().Get("type")
			instances := []wirelessFeatureInstance{}
			flag := false
			if kind == wirelessFRAKind && fra != nil {
				instances = append(instances, wirelessFeatureInstance{ID: fra.ID, Name: fra.Name, System: &flag})
			}
			if kind == wirelessCleanAirKind && ca != nil {
				instances = append(instances, wirelessFeatureInstance{ID: ca.ID, Name: ca.Name, System: &flag})
			}
			if kind != wirelessFRAKind && kind != wirelessCleanAirKind {
				t.Error("unexpected kind", kind)
			}
			json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"type": kind, "count": len(instances), "instances": instances}}})
		case r.Method == http.MethodGet && fra != nil && r.URL.Path == wirelessFRAPath+"/"+fra.ID:
			json.NewEncoder(w).Encode(map[string]any{"response": fra})
		case r.Method == http.MethodGet && ca != nil && r.URL.Path == wirelessCleanAirPath+"/"+ca.ID:
			json.NewEncoder(w).Encode(map[string]any{"response": ca})
		case r.Method == http.MethodPost && r.URL.Path == wirelessFRAPath || r.Method == http.MethodPut && r.URL.Path == wirelessFRAPath+"/"+featureTestFRAUUID:
			var body wirelessFRA
			if json.NewDecoder(r.Body).Decode(&body) != nil || validateWirelessFRA(body) != nil || body.Name != "FRA" || body.ID != "" {
				t.Error("invalid FRA payload")
				w.WriteHeader(400)
				return
			}
			body.ID = featureTestFRAUUID
			fra = &body
			writes++
			id := fmt.Sprint("feature-task-", writes)
			tasks[id] = time.Now().UnixMilli()
			verb := "created"
			if r.Method == http.MethodPut {
				verb = "updated"
			}
			results[id] = [3]string{featureTestFRAUUID, "FRA", verb}
			json.NewEncoder(w).Encode(map[string]any{"response": Task{ID: id}})
		case r.Method == http.MethodPost && r.URL.Path == wirelessCleanAirPath || r.Method == http.MethodPut && r.URL.Path == wirelessCleanAirPath+"/"+featureTestCAUUID:
			var body wirelessCleanAir
			if json.NewDecoder(r.Body).Decode(&body) != nil || validateWirelessCleanAir(body) != nil || body.Name != "CA" || body.ID != "" {
				t.Error("invalid CleanAir payload")
				w.WriteHeader(400)
				return
			}
			body.ID = featureTestCAUUID
			ca = &body
			writes++
			id := fmt.Sprint("feature-task-", writes)
			tasks[id] = time.Now().UnixMilli()
			verb := "created"
			if r.Method == http.MethodPut {
				verb = "updated"
			}
			results[id] = [3]string{featureTestCAUUID, "CA", verb}
			json.NewEncoder(w).Encode(map[string]any{"response": Task{ID: id}})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, taskPath):
			id := strings.TrimPrefix(r.URL.Path, taskPath)
			if !publicationRaced && (mode == "publish-owner" || mode == "publish-source" || mode == "publish-update-owner" || mode == "publish-update-source") {
				current, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if current.Status.Configuration != nil && current.Status.Configuration.Operation != nil && current.Status.Configuration.Operation.Phase == "Verifying" && current.Status.Configuration.Operation.TaskID == id && (!strings.HasPrefix(mode, "publish-update-") || current.Status.Configuration.Operation.Action == "Update") {
					publicationRaced = true
					if strings.HasSuffix(mode, "-owner") {
						current.Status.Configuration.Owners[0].ConfigUID = "foreign"
						if err = a.statusWriter.Status().Update(ctx, current); err != nil {
							t.Fatal(err)
						}
					} else {
						latest := &configapi.NetworkControllerConfig{}
						if err = a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), latest); err != nil {
							t.Fatal(err)
						}
						latest.Generation++
						latest.Spec.Source.Inline.Raw = model(1)
						if err = a.statusWriter.Update(ctx, latest); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			stamp, ok := tasks[id]
			if !ok {
				t.Error("unretained task lookup", id)
				w.WriteHeader(404)
				return
			}
			start, end := stamp, stamp
			if poisoned {
				switch mode {
				case "foreign-task":
					id = "foreign-task"
				case "stale-task":
					start -= int64(time.Hour / time.Millisecond)
				case "pending":
					end = 0
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": id, "startTime": start, "endTime": end, "isError": poisoned && mode == "failed", "errorCode": "qualification", "serviceType": "NCNC", "data": results[strings.TrimPrefix(r.URL.Path, taskPath)][0], "progress": "Successfully " + results[strings.TrimPrefix(r.URL.Path, taskPath)][2] + " model config design with name " + results[strings.TrimPrefix(r.URL.Path, taskPath)][1]}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	key := ctrlclient.ObjectKeyFromObject(config)
	injected := map[string]bool{}
	tick := func(inject bool) {
		t.Helper()
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil && !(publicationRaced && strings.HasSuffix(mode, "-source") && apierrors.IsConflict(err)) {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
		if !inject {
			return
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if nc.Status.Configuration == nil {
			return
		}
		op := nc.Status.Configuration.Operation
		if op == nil || op.Phase != "Accepted" || injected[op.ID] || op.Action != "Update" && mode != "create" && mode != "known-create" && mode != "missing-create-task" {
			return
		}
		op.Phase = "Unknown"
		if mode == "create" {
			op.Resource = "rrm-fra:FRA"
			if mixed {
				op.Resource = "rrm_fra/rrm-fra:FRA"
			}
		}
		if mode == "missing-task" || mode == "missing-create-task" {
			op.TaskID = ""
		}
		if mode == "foreign-owner" {
			op.ConfigUID = "foreign-owner"
		}
		if mode == "replacement" {
			fra.ID = "replacement-id"
		}
		raw, err := json.Marshal(nc)
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
		injected[op.ID] = true
		poisoned = mode != "known" && mode != "known-create"
		if mode == "source" {
			config.Generation++
			config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(config.Spec.Source.Inline.Raw), `"fra_interval":6`, `"fra_interval":12`, 1))
			if err = a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
		}
	}
	resources := 1
	if mixed {
		resources = 2
	}
	var initialCreators map[string]cvk.NetworkControllerConfigurationActionApplication
	for revision := range 3 {
		config.Generation++
		config.Spec.Source.Inline.Raw = model(revision)
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		converged := false
		for range 50 {
			tick(true)
			if publicationRaced {
				for range 5 {
					tick(false)
				}
				state := journalState(t, a)
				expectedWrites, expectedRecords := 1, 0
				if strings.HasPrefix(mode, "publish-update-") {
					expectedWrites, expectedRecords = resources+1, resources
				}
				if writes != expectedWrites || len(state.ActionApplications) != expectedRecords || state.Operation == nil || state.Operation.Phase != "Verifying" || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("publication race created authority", mode, writes, state, config.Status)
				}
				if err := a.advanceConfiguration(ctx, state.Operation.ID, "Verified", ""); err != errConfigurationTransition {
					t.Fatal("publication race bypassed mandatory receipt", err)
				}
				return
			}
			if mode != "known" && mode != "known-create" && !strings.HasPrefix(mode, "publish-update-") && len(injected) > 0 {
				expected := writes
				for range 5 {
					tick(false)
				}
				op := journalState(t, a).Operation
				if op == nil || op.Phase != "Unknown" && op.Phase != "Verifying" || writes != expected || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("uncertainty escaped fence", mode, writes, config.Status, op)
				}
				wanted := resources + 1
				if mode == "create" || mode == "missing-create-task" {
					wanted = 1
				}
				if writes != wanted {
					t.Fatal("unexpected fenced write count", mode, writes, wanted)
				}
				return
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				converged = true
				break
			}
		}
		if !converged || writes != (revision+1)*resources {
			t.Fatal("revision did not converge once", revision, writes, config.Status)
		}
		creators, err := featureCreatedRecords(journalState(t, a))
		if err != nil {
			t.Fatal(err)
		}
		if revision == 0 {
			initialCreators = creators
		} else if !reflect.DeepEqual(initialCreators, creators) {
			t.Fatal("update rewrote immutable creation lineage")
		}
		tick(false)
		if journalState(t, a).Operation != nil || writes != (revision+1)*resources {
			t.Fatal("no-op replayed a mutation")
		}
	}
	expectedRecoveries := resources * 2
	if mode == "known-create" {
		expectedRecoveries = resources * 3
	}
	records, recordErr := featureCreatedRecords(journalState(t, a))
	if recordErr != nil || len(records) != resources {
		t.Fatal("creator receipts missing", len(records), recordErr)
	}
	applied, appliedErr := featureAppliedRecords(journalState(t, a))
	if appliedErr != nil || len(applied) != resources || len(journalState(t, a).ActionApplications) != 2*resources {
		t.Fatal("missing bounded applied state", len(applied), appliedErr)
	}
	for _, record := range applied {
		key, _ := featureUpdateKey(&record.Operation)
		_, hash, err := featureReceiptIdentity(record.Resource, featureAppliedPrefix, key)
		if err != nil || !validWirelessFeatureFingerprint(hash) {
			t.Fatal("missing applied native state", err)
		}
		if record.Operation.ConfigGeneration != config.Generation || record.Operation.Action != "Update" {
			t.Fatal("stale applied state", record)
		}
	}
	for _, record := range records {
		key, _ := featureCreateKey(record.Operation.Resource)
		_, hash, err := featureReceiptIdentity(record.Resource, featureCreatedPrefix, key)
		if err != nil || !validWirelessFeatureFingerprint(hash) {
			t.Fatal("missing creator native state", err)
		}
		if record.Operation.ConfigUID != string(config.UID) || record.Operation.ConfigName != config.Name {
			t.Fatal("foreign creator publication")
		}
	}
	if len(injected) != expectedRecoveries || fra == nil || fra.ID != featureTestFRAUUID || mixed && (ca == nil || ca.ID != featureTestCAUUID) {
		t.Fatal("missing original-UUID task recovery", len(injected))
	}
}
