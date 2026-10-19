// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

func TestTagTaskRecoverySelection(t *testing.T) {
	for _, section := range []string{"inventory", "templates"} {
		e := &configurationGroup{parts: []configurationPart{{name: "templates", executor: &configurationGroup{parts: []configurationPart{{name: "tags", executor: &tagExecutor{desired: []desiredTag{{name: "selected"}}}}}}}}}
		selectOp := tagTaskSelection(e, section)
		for _, mode := range []string{"create", "update", "taskless", "unselected", "wrong-path", "delete", "parent", "physical", "prepared", "wrong-section", "wrong-phase", "no-source-owner", "future"} {
			t.Run(section+"/"+mode, func(t *testing.T) {
				at := metav1.Now()
				op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("1", 32), Section: section, Resource: "templates/tags/selected", Action: "Create", Phase: "Unknown", TaskID: "task", SubmittedAt: &at, ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1}
				switch mode {
				case "update":
					op.Action = "Update"
					op.TargetID = "target"
				case "taskless":
					op.TaskID = ""
				case "unselected":
					op.Resource = "templates/tags/other"
				case "wrong-path":
					op.Resource = "tags/selected"
				case "delete":
					op.Action = "Delete"
				case "parent":
					op.ParentID = "parent"
				case "physical":
					op.TargetSerial = "serial"
				case "prepared":
					op.PreparedAt = &at
				case "wrong-section":
					op.Section = "wireless"
				case "wrong-phase":
					op.Phase = "Prepared"
				case "no-source-owner":
					op.ConfigUID = ""
				case "future":
					v := metav1.NewTime(time.Now().Add(time.Hour))
					op.SubmittedAt = &v
				}
				want := mode == "create" || mode == "update"
				if selectOp(op) != want {
					t.Fatal("recovery selection mismatch", mode)
				}
			})
		}
	}
}

func TestTagKnownTaskPublicRestart(t *testing.T) {
	for _, section := range []string{"inventory", "templates", "combined", "inventory-configmap", "templates-configmap", "combined-configmap"} {
		for _, update := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/update-%t", section, update), func(t *testing.T) { exerciseTagKnownTaskRestart(t, section, update, nil) })
		}
	}
}

func exerciseTagKnownTaskRestart(t *testing.T, section string, update bool, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	projected := strings.HasSuffix(section, "-configmap")
	section = strings.TrimSuffix(section, "-configmap")
	ctx := context.Background()
	a, config := journalFixture(t)
	const id = "22f33fa0-e275-41c9-a50d-d7e5135adb8c"
	config.Spec.ManagedSections = []string{section}
	model := map[string]any{section: map[string]any{"tags": []any{map[string]any{"name": "selected", "description": "desired", "system_tag": false}}}}
	if section == "combined" {
		config.Spec.ManagedSections = []string{"inventory", "templates"}
		model = map[string]any{"inventory": map[string]any{"tags": []any{map[string]any{"name": "selected", "description": "desired", "system_tag": false}}}, "templates": map[string]any{}}
	}
	config.Spec.Source.Inline.Raw, _ = json.Marshal(map[string]any{"catalyst_center": model})
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if install != nil {
		install(a, config)
	}
	if projected {
		model := string(config.Spec.Source.Inline.Raw)
		cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "tag-model", Namespace: config.Namespace}, Data: map[string]string{"model": model}}
		if install == nil {
			cm.UID = "tag-model-uid"
		}
		if err := a.statusWriter.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
		config.Spec.Source = configapi.NetworkControllerConfigurationSource{ConfigMapRef: &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}}
		if install == nil {
			config.Generation++
		}
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	actual := []observedTag{}
	old := "old"
	no := false
	if update {
		actual = append(actual, observedTag{ID: id, Name: "selected", Description: &old, SystemTag: &no})
	}
	writes := 0
	stamp := int64(0)
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.Method == "GET" && r.URL.Path == taskPath+"original-task":
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "original-task", "startTime": stamp, "endTime": stamp, "isError": false, "serviceType": "Grouping Service", "data": id}})
		case r.Method == "GET" && r.URL.Path == tagsPath:
			json.NewEncoder(w).Encode(map[string]any{"response": actual})
		case r.URL.Path == tagsPath && (r.Method == "POST" || r.Method == "PUT"):
			if writes != 0 {
				t.Error("original task recovery replayed mutation")
			}
			var tag observedTag
			if json.NewDecoder(r.Body).Decode(&tag) != nil {
				t.Error("invalid tag request")
			}
			tag.ID = id
			actual = []observedTag{tag}
			writes++
			stamp = time.Now().UnixMilli()
			fmt.Fprint(w, `{"response":{"taskId":"original-task"}}`)
		default:
			t.Error("unexpected API", r.Method, r.URL.Path)
			w.WriteHeader(400)
		}
	})
	interrupted := false
	for i := 0; i < 16; i++ {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !interrupted && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Accepted" {
			op := nc.Status.Configuration.Operation
			if op.TaskID != "original-task" {
				t.Fatal("task receipt missing")
			}
			op.Phase = "Unknown"
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			interrupted = true
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			break
		}
	}
	if !update {
		records, err := tagOwnedRecords(journalState(t, a))
		if err != nil {
			t.Fatal(err)
		}
		creator, exists := records.created[featureIdentityDigest("selected")]
		if !exists || len(records.created) != 1 || len(records.applied) != 0 {
			t.Fatal("new tag did not retain exactly one immutable creator")
		}
		if projected && (creator.Operation.SourceUID == "" || creator.Operation.SourceVersion == "" || !strings.HasSuffix(creator.Operation.Resource, ":c")) {
			t.Fatal("creator lost ConfigMap source identity")
		}
	} else {
		records, err := tagOwnedRecords(journalState(t, a))
		if err != nil || len(records.created) != 0 || len(records.applied) != 0 {
			t.Fatal("imported tag Update acquired creator authority", err)
		}
	}
	if !interrupted || writes != 1 || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("acknowledged tag did not recover once", interrupted, writes, config.Status.Phase)
	}
}
