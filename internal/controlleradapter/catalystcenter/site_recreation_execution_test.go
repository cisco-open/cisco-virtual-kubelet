// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
)

var siteRecreationExecutionModes = []string{"complete", "latest", "occupied", "old-moved", "parent-replaced", "source", "key", "ancestry", "history", "unknown", "pending", "failed", "lost-ack", "unauthorized", "server-error", "prepared"}

func TestSiteRecreationExecution(t *testing.T) {
	for _, cm := range []bool{false, true} {
		for _, mode := range siteRecreationExecutionModes {
			t.Run(fmt.Sprintf("cm-%t/%s", cm, mode), func(t *testing.T) { exerciseSiteRetirementBinding(t, "write-recreate-"+mode, cm, nil) })
		}
	}
}

// Continue the staged retirement fixture through source reintroduction, guarded
// native Create, original-task observation and the production creator publisher.
func exerciseSiteRecreationExecution(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, keyPath string, cm *core.ConfigMap, mode string) {
	t.Helper()
	ctx := context.Background()
	const model = `{"catalyst_center":{"sites":{"areas":[{"name":"Probe","parent_name":"Global"}]}}}`
	if config.Spec.Source.ConfigMapRef != nil {
		cm.Data["model.json"] = model
		if err := a.statusWriter.Update(ctx, cm); err != nil {
			t.Fatal(err)
		}
	} else {
		config.Spec.Source.Inline.Raw = []byte(model)
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	const successor = "22222222-2222-4222-8222-222222222222"
	created, changed := false, false
	posts := 0
	parent := "root"
	if mode == "parent-replaced" {
		parent = "replacement"
	}
	var submitted time.Time
	pending := mode == "pending"
	update := func(change func(*cvk.NetworkController)) {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		change(nc)
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method == http.MethodPost {
			posts++
			if r.URL.Path != siteDesignMutationEndpoint("area") {
				t.Error("recreation escaped fixed endpoint")
				w.WriteHeader(500)
				return
			}
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["name"] != "Probe" || body["parentId"] != "root" {
				t.Error("unexpected recreation body")
				w.WriteHeader(500)
				return
			}
			if mode == "unauthorized" {
				w.WriteHeader(401)
				return
			}
			created = true
			if mode == "server-error" {
				w.WriteHeader(500)
				return
			}
			if mode == "lost-ack" {
				fmt.Fprint(w, `{"response":{}}`)
				return
			}
			fmt.Fprint(w, `{"response":{"taskId":"recreation-task","url":"https://untrusted.example/task"}}`)
			return
		}
		if r.Method != http.MethodGet {
			t.Error("unexpected mutation")
			w.WriteHeader(500)
			return
		}
		if r.URL.Path == sitesPath {
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			if !changed && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Submitting" {
				changed = true
				switch mode {
				case "source":
					if config.Spec.Source.ConfigMapRef != nil {
						cm.Data["changed"] = "late source"
						if err := a.statusWriter.Update(ctx, cm); err != nil {
							t.Error(err)
						}
					} else {
						config.Generation++
						if err := a.statusWriter.Update(ctx, config); err != nil {
							t.Error(err)
						}
					}
				case "key":
					if err := os.WriteFile(keyPath, []byte(strings.Repeat("f", 64)), 0600); err != nil {
						t.Error(err)
					}
				case "ancestry":
					parent = "replacement"
				case "history":
					update(func(n *cvk.NetworkController) {
						n.Status.Configuration.ActionApplications[0].Operation.TaskID = "edited-original"
					})
				}
			}
			rows := []Site{{ID: parent, Name: "Global", NameHierarchy: "Global", Type: "global"}}
			if created || mode == "occupied" {
				rows = append(rows, Site{ID: successor, Name: "Probe", NameHierarchy: "Global/Probe", ParentID: parent, Type: "area"})
			}
			if mode == "old-moved" {
				rows = append(rows, Site{ID: siteOwnedTestUUID, Name: "Other", NameHierarchy: "Global/Other", ParentID: parent, Type: "area"})
			}
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
			return
		}
		if r.URL.Path == taskPath+"recreation-task/tree" {
			end := time.Now().UnixMilli()
			if pending {
				end = 0
			}
			row := map[string]any{"id": "recreation-task", "rootId": "recreation-task", "isError": mode == "failed", "startTime": submitted.UnixMilli(), "endTime": end}
			json.NewEncoder(w).Encode(map[string]any{"response": []any{row}})
			return
		}
		if r.URL.Path == taskPath+"recreation-task" {
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "recreation-task", "isError": false, "startTime": submitted.UnixMilli(), "endTime": time.Now().UnixMilli(), "data": successor}})
			return
		}
		t.Errorf("unexpected recreation read %s", r.URL.Path)
		w.WriteHeader(500)
	})
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := nc.Status.Configuration.DeepCopy()
	_, write, err := a.siteRecreationExecution(ctx, nc, config, source, "Global/Probe")
	if mode == "occupied" || mode == "old-moved" || mode == "parent-replaced" {
		if err == nil || write != nil || posts != 0 || !reflect.DeepEqual(before, journalState(t, a)) {
			t.Fatal("unsafe absence/ancestry acquired writer", err)
		}
		return
	}
	if err != nil || write == nil {
		t.Fatal("recreation did not plan", err)
	}
	if !noDispatchWriteSupported("sites", write) {
		t.Fatal("recreation bypassed shared no-dispatch capability")
	}
	if mode == "prepared" {
		op, err := a.prepareConfiguration(ctx, config, source, "sites", write.resource, "Create", write.identity)
		if err != nil {
			t.Fatal(err)
		}
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		restarted := *a
		_, write, err = restarted.siteRecreationExecution(ctx, current, config, source, "Global/Probe")
		if err != nil || write == nil {
			t.Fatal("prepared recreation did not reconstruct", err)
		}
		err = a.submitPreparedConfigurationReceipt(ctx, config, source, op, "sites", write.resource, "Create", configurationTaskWriteReceipt("sites", write), nil, write.identity)
	} else {
		err = a.submitConfigurationWrite(ctx, config, source, "sites", write)
	}
	state := journalState(t, a)
	if mode == "source" || mode == "key" || mode == "ancestry" || mode == "history" {
		if !errors.Is(err, errConfigurationNotDispatched) || posts != 0 || state.Operation != nil {
			t.Fatal("recreation preflight refusal lost no-dispatch proof", err)
		}
		return
	}
	if mode == "lost-ack" || mode == "unauthorized" || mode == "server-error" {
		if err == nil || posts != 1 || state.Operation == nil || state.Operation.Phase != "Unknown" || state.Operation.TaskID != "" {
			t.Fatal("uncertain recreation lost fence", err)
		}
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, retry, err := a.siteRecreationExecution(ctx, current, config, source, "Global/Probe"); err == nil || retry != nil || posts != 1 {
			t.Fatal("taskless recreation gained replay permission")
		}
		return
	}
	if err != nil || posts != 1 || state.Operation == nil || state.Operation.Phase != "Accepted" {
		t.Fatal("recreation submission failed", err)
	}
	submitted = state.Operation.SubmittedAt.Time
	if mode == "unknown" {
		update(func(n *cvk.NetworkController) { n.Status.Configuration.Operation.Phase = "Unknown" })
	}
	current, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restarted := *a
	e, retry, err := restarted.siteRecreationExecution(ctx, current, config, source, "Global/Probe")
	if err != nil || retry != nil || e == nil || posts != 1 {
		t.Fatal("original-task recreation recovery failed", err)
	}
	op := current.Status.Configuration.Operation
	done, err := a.client.siteTaskCompletion(e.desired)(ctx, op, time.Now())
	if mode == "failed" {
		if err == nil && done {
			t.Fatal("failed recreation task completed")
		}
		return
	}
	if pending {
		if err != nil || done {
			t.Fatal("pending recreation task completed", err)
		}
		pending = false
		done, err = a.client.siteTaskCompletion(e.desired)(ctx, op, time.Now())
	}
	if err != nil || !done {
		t.Fatal("original recreation task did not complete", err)
	}
	if err := a.advanceConfiguration(ctx, op.ID, "Verifying", ""); err != nil {
		t.Fatal(err)
	}
	current, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.recordSiteCreated(ctx, current, config, source, e, current.Status.Configuration.Operation); err != nil {
		t.Fatal("native verified recreation did not publish", err)
	}
	if err := a.finishConfiguration(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
	final := journalState(t, a)
	archives, err := siteHistoryCohorts(final)
	if err != nil || len(archives) != 1 || final.Operation != nil || posts != 1 || len(final.ActionApplications) != len(before.ActionApplications)+1 || !reflect.DeepEqual(final.Owners, before.Owners) {
		t.Fatal("complete recreation lost archived custody", err)
	}
}
