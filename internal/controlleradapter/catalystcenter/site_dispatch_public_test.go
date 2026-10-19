// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestSiteSingularPublicNoDispatch(t *testing.T) {
	for _, stage := range []string{"area-create", "building-create", "floor-create", "building-update", "floor-update"} {
		for _, mode := range []string{"stable", "native-edit", "source-edit", "partial-read", "owner-edit", "lost-ack"} {
			t.Run(stage+"/"+mode, func(t *testing.T) { exerciseSiteSingularPublic(t, stage, mode, nil) })
		}
	}
}

func TestSiteSingularPublicTaskRecovery(t *testing.T) {
	for _, stage := range []string{"area-create", "building-create", "floor-create", "building-update", "floor-update"} {
		for _, mode := range []string{"recover", "recover-pending", "recover-failed", "recover-foreign", "recover-unavailable", "recover-source", "recover-owner", "recover-readback", "recover-missing-result", "recover-foreign-result"} {
			if strings.HasSuffix(stage, "update") && strings.HasSuffix(mode, "result") {
				continue
			}
			t.Run(stage+"/"+mode, func(t *testing.T) { exerciseSiteSingularPublic(t, stage, mode, nil) })
		}
	}
}

func exerciseSiteSingularPublic(t *testing.T, stage, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	desired, actual := siteDispatchFixture(stage)
	item := map[string]any{"name": lastSiteName(desired[0].path), "parent_name": parentSitePath(desired[0].path)}
	for key, value := range desired[0].fields {
		item[key] = value
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"sites": map[string]any{desired[0].kind + "s": []any{item}}}})
	config.Spec.ManagedSections = []string{"sites"}
	config.Spec.Source.Inline = nil
	config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: "site-dispatch-source", Key: "model.yaml"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	privateMode := strings.Contains(mode, "private")
	privatePath := ""
	if privateMode {
		privatePath, _ = configureSiteNativeComparisonFixture(t, a, config)
	}
	if install != nil {
		install(a, config)
	}
	model := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "site-dispatch-source", Namespace: config.Namespace}, Data: map[string]string{"model.yaml": string(raw)}}
	if install == nil {
		model.UID = "site-source"
	}
	if err := a.statusWriter.Create(ctx, model); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	injected, sawRefusal, privateEdited := false, false, false
	recovery := strings.HasPrefix(mode, "recover")
	dispatchFault := mode == "native-edit" || mode == "source-edit" || mode == "partial-read" || mode == "owner-edit"
	recovered, taskReads := false, 0
	injectedOperation := ""
	posts := 0
	stamp := time.Now().UnixMilli()
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method == "GET" && r.URL.Path == sitesPath {
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Error("controller observation failed")
				w.WriteHeader(500)
				return
			}
			if !injected && dispatchFault && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Submitting" {
				injected = true
				injectedOperation = nc.Status.Configuration.Operation.ID
				switch mode {
				case "native-edit":
					country := "private ancestor edit"
					actual[0].Country = &country
				case "partial-read":
					w.WriteHeader(206)
				case "source-edit":
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(model), model); err != nil {
						t.Error("source observation failed")
					} else {
						model.Data["qualification-revision"] = "changed"
						if err := a.statusWriter.Update(ctx, model); err != nil {
							t.Error("source edit failed")
						}
					}
				case "owner-edit":
					nc.Status.Configuration.Owners[0].ConfigUID = "foreign-owner"
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Error("owner edit failed")
					}
				}
			}
			if privateMode {
				body, _ := json.Marshal(actual)
				var rows []map[string]any
				json.Unmarshal(body, &rows)
				for _, row := range rows {
					if row["nameHierarchy"] == desired[0].path {
						value := "private-native-value"
						if privateEdited {
							value = "changed-private-native-value"
						}
						row["opaqueNative"] = value
						row["exactCounter"] = json.Number("9007199254740993")
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"response": actual})
			}
			return
		}
		if r.Method == "GET" && (r.URL.Path == taskPath+"site-task" || r.URL.Path == taskPath+"site-task/tree") {
			taskReads++
			if recovery && r.URL.Path != taskPath+"site-task/tree" {
				nc, err := a.currentController(ctx)
				if err != nil || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.Phase != "Verifying" || nc.Status.Configuration.Operation.Action != "Create" {
					t.Error("recovery fell back to root before full-tree verification")
				}
			}
			if mode == "recover-unavailable" {
				w.WriteHeader(404)
				return
			}
			child := map[string]any{"id": "child", "parentId": "site-task", "rootId": "site-task", "startTime": stamp, "endTime": stamp, "isError": false}
			switch mode {
			case "recover-pending":
				child["endTime"] = 0
			case "recover-failed":
				child["isError"] = true
			case "recover-foreign":
				child["rootId"] = "foreign"
			}
			result := siteOwnedTestUUID
			if mode == "recover-missing-result" {
				result = ""
			}
			if mode == "recover-foreign-result" {
				result = "22222222-2222-4222-8222-222222222222"
			}
			writeDefinitionTaskFixture(w, r, map[string]any{"id": "site-task", "startTime": stamp, "endTime": stamp, "isError": false, "data": result}, child)
			return
		}
		if r.Method == "POST" || r.Method == "PUT" {
			posts++
			stamp = time.Now().UnixMilli()
			var site Site
			if json.NewDecoder(r.Body).Decode(&site) != nil {
				t.Error("invalid site write")
			}
			site.ID, site.NameHierarchy, site.Type = siteOwnedTestUUID, desired[0].path, desired[0].kind
			found := false
			for i := range actual {
				if actual[i].NameHierarchy == site.NameHierarchy {
					site.ID = actual[i].ID
					actual[i], found = site, true
					break
				}
			}
			if !found {
				actual = append(actual, site)
			}
			if mode == "lost-ack" {
				fmt.Fprint(w, `{}`)
			} else {
				fmt.Fprint(w, `{"response":{"taskId":"site-task"}}`)
			}
			return
		}
		t.Error("unexpected native operation")
		w.WriteHeader(500)
	})
	key := ctrlclient.ObjectKeyFromObject(config)
	for i := 0; i < 35; i++ {
		if recovery && !recovered {
			state := journalState(t, a)
			if state != nil && state.Operation != nil && state.Operation.Phase == "Accepted" {
				if state.Operation.TaskID != "site-task" {
					t.Fatal("cannot inject uncertainty without original acknowledged task")
				}
				if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
				recovered = true
				if mode == "recover-readback" {
					mu.Lock()
					for index, site := range actual {
						if site.NameHierarchy != desired[0].path {
							continue
						}
						if strings.HasSuffix(stage, "update") {
							actual[index].ID = "replacement-site"
						} else {
							actual = append(actual[:index], actual[index+1:]...)
						}
						break
					}
					mu.Unlock()
				}
				if mode == "recover-source" {
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(model), model); err != nil {
						t.Fatal(err)
					}
					model.Data["qualification-revision"] = "after-acknowledgement"
					if err := a.statusWriter.Update(ctx, model); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "recover-owner" {
					nc, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					nc.Status.Configuration.Owners[0].ConfigUID = "foreign-owner"
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		restarted := *a
		result, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key})
		if err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
		state := journalState(t, a)
		mu.Lock()
		sourceRetired := mode == "source-edit" && injected && injectedOperation != "" && posts == 0
		mu.Unlock()
		if sourceRetired && state.Operation == nil {
			// The outer reconciler discards status derived from a stale source.
			// The durable original operation still retires with zero requests.
			if len(state.Owners) != 1 || state.Owners[0].ConfigUID != string(config.UID) {
				t.Fatal("source-change retirement lost section ownership")
			}
			sawRefusal = true
		}
		for _, c := range config.Status.Conditions {
			if c.Reason == "NativePreflightRefused" {
				mu.Lock()
				clean := posts == 0
				mu.Unlock()
				if !clean || state.Operation != nil || len(state.Owners) != 1 || state.Owners[0].ConfigUID != string(config.UID) || result.RequeueAfter <= 0 || config.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
					t.Fatal("site refusal retired more than the proven operation")
				}
				sawRefusal = true
			}
		}
	}
	state := journalState(t, a)
	mu.Lock()
	count := posts
	reads := taskReads
	mu.Unlock()
	if recovery && mode != "recover" && mode != "recover-private" {
		phase := "Unknown"
		if mode == "recover-readback" || strings.HasSuffix(mode, "result") {
			phase = "Verifying"
		}
		if !recovered || count != 1 || state.Operation == nil || state.Operation.Phase != phase || state.Operation.TaskID != "site-task" || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || len(state.ActionApplications) != 0 {
			t.Fatal("unverified or foreign-bound retained task escaped its fence")
		}
		if (mode == "recover-source" || mode == "recover-owner") && reads != 0 {
			t.Fatal("changed authority allowed task observation")
		}
		return
	}
	if mode == "lost-ack" || mode == "owner-edit" {
		want := 1
		if mode == "owner-edit" {
			want = 0
		}
		fenced := state.Operation != nil && (state.Operation.Phase == "Unknown" || mode == "owner-edit" && state.Operation.Phase == "Submitting")
		if count != want || sawRefusal || !fenced || state.Operation.TaskID != "" {
			t.Fatal("uncertain or foreign-owned site operation was released/replayed")
		}
		return
	}
	wantReceipts := 0
	if strings.HasSuffix(stage, "create") {
		wantReceipts = 1
	}
	if count != 1 || state.Operation != nil || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || sawRefusal != dispatchFault || len(state.ActionApplications) != wantReceipts || recovery && (!recovered || reads == 0) {
		t.Fatal("site replan did not converge with exact creator evidence", count, config.Status.Phase)
	}
	if wantReceipts == 1 {
		records, err := siteCreatedRecords(state)
		record, found := siteCreatorForPath(records, desired[0].path)
		if err != nil || !found || len(records) != 1 || record.id != siteOwnedTestUUID || !siteCreatedReceiptPresent(state, &state.ActionApplications[0].Operation) {
			t.Fatal("original creator receipt missing or malformed", err)
		}
		if privateMode {
			if record.keyID == "" || record.mac == "" || !strings.HasPrefix(record.receipt.Resource, sitePrivateCreatedPrefix) {
				t.Fatal("private creator baseline was lost")
			}
			body, _ := json.Marshal(state)
			if strings.Contains(string(body), "private-native-value") || strings.Contains(string(body), "9007199254740993") {
				t.Fatal("native private fields entered public status")
			}
			if mode != "private-known" && mode != "recover-private" {
				before := state.DeepCopy()
				switch mode {
				case "private-native-edit":
					mu.Lock()
					privateEdited = true
					mu.Unlock()
				case "private-key-change":
					if err := os.WriteFile(privatePath, []byte(strings.Repeat("fa", 32)), 0600); err != nil {
						t.Fatal(err)
					}
				case "private-option-loss":
					config.Spec.SecretRefs = nil
					config.Generation++
					if err := a.statusWriter.Update(ctx, config); err != nil {
						t.Fatal(err)
					}
				}
				for pass := 0; pass < 4; pass++ {
					restarted := *a
					if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
						t.Fatal(err)
					}
				}
				if err := a.statusReader.Get(ctx, key, config); err != nil {
					t.Fatal(err)
				}
				after := journalState(t, a)
				mu.Lock()
				writes := posts
				mu.Unlock()
				if writes != 1 || after.Operation != nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || !reflect.DeepEqual(before.ActionApplications, after.ActionApplications) {
					t.Fatal("private baseline drift or key loss was rebased/adopted")
				}
			}
		}
		if mode == "creator-replaced" || mode == "creator-missing" {
			before := state.DeepCopy()
			mu.Lock()
			for index, site := range actual {
				if site.NameHierarchy != desired[0].path {
					continue
				}
				if mode == "creator-replaced" {
					actual[index].ID = "replaced-site"
				} else {
					actual = append(actual[:index], actual[index+1:]...)
				}
				break
			}
			mu.Unlock()
			for pass := 0; pass < 4; pass++ {
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			after := journalState(t, a)
			mu.Lock()
			writes := posts
			mu.Unlock()
			if writes != 1 || after.Operation != nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || !reflect.DeepEqual(before.ActionApplications, after.ActionApplications) {
				t.Fatal("missing/replaced owned site was adopted, recreated or declared converged")
			}
		}
	}
}
