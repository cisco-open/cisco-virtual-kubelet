// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type definitionRecoveryCase struct{ name, model, path, resource, from, to string }

var definitionRecoveryCases = []definitionRecoveryCase{
	{"anchor_groups", wirelessAnchorModel, wirelessAnchorPath, "anchor-group:CVK_LAB", "PRIMARY", "SECONDARY"},
	{"interfaces", wirelessInterfaceModel, wirelessInterfacesPath, "interface:CVK_LAB", "3998", "3997"},
	{"power_profiles", wirelessPowerModel, wirelessPowerPath, "power-profile:CVK_LAB", "100MBPS", "1000MBPS"},
	{"dot11be_profiles", wirelessDot11beModel, wirelessDot11bePath, "dot11be-profile:CVK_LAB", "false", "true"},
	{"security_settings", wirelessACLModel, wirelessACLsPath, "pre-auth-acl:CVK_LAB", "443", "8443"},
	{"rf_profiles", wirelessRFModel, wirelessRFPath, "rf-profile:CVK_LAB", "100", "200"},
}

func TestWirelessDefinitionRecoveryScope(t *testing.T) {
	for _, entry := range definitionRecoveryCases {
		for _, mixed := range []bool{false, true} {
			raw := entry.model
			resource := entry.resource
			if mixed {
				extra := `"interfaces":[{"name":"SECOND","vlan_id":3990}],`
				if entry.name == "interfaces" {
					extra = `"anchor_groups":[{"name":"SECOND","mobility_anchors":[{"device_name":"external","ip_address":"192.0.2.199","anchor_priority":"PRIMARY","managed_anchor_wlc":false}]}],`
				}
				raw = strings.Replace(raw, `"wireless":{`, `"wireless":{`+extra, 1)
				resource = entry.name + "/" + resource
			}
			plan, err := (&client{}).compileConfigurationApply([]byte(raw), []string{"wireless"}, nil)
			if err != nil || plan.recoverKnownTask == nil {
				t.Fatal(entry.name, "missing selector", err)
			}
			now := metav1.NewTime(time.Now().Add(-time.Second))
			original := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, Section: "wireless", Resource: resource, Action: "Update", Phase: "Unknown", TaskID: "task-id", TargetID: "native-id", SubmittedAt: &now}
			for _, mode := range []string{"known", "taskless", "targetless", "create", "delete", "section", "phase", "serial", "parent", "created", "group", "dependencies", "cancellation", "prepared", "credential", "history", "missing-time", "zero-time", "id", "owner", "generation", "unselected", "prefix"} {
				t.Run(entry.name+fmt.Sprint(mixed)+"/"+mode, func(t *testing.T) {
					op := original.DeepCopy()
					switch mode {
					case "taskless":
						op.TaskID = ""
					case "targetless":
						op.TargetID = ""
					case "create":
						op.Action = "Create"
						op.TargetID = ""
					case "delete":
						op.Action = "Delete"
					case "section":
						op.Section = "feature_templates"
					case "phase":
						op.Phase = "Accepted"
					case "serial":
						op.TargetSerial = "serial"
					case "parent":
						op.ParentID = "parent"
					case "created":
						op.CreatedResourceID = "created"
					case "group":
						op.PhysicalGroup = &cvk.NetworkControllerConfigurationGroupIdentity{}
					case "dependencies":
						op.DependencyVersions = &cvk.NetworkControllerConfigurationDependencyVersions{}
					case "cancellation":
						op.CancellationRequestedAt = &now
					case "prepared":
						op.PreparedAt = &now
					case "credential":
						op.CredentialVersions = []cvk.NetworkControllerConfigurationCredentialVersion{{Version: 1}}
					case "history":
						op.SSIDCredentialHistory = []cvk.NetworkControllerConfigurationSSIDCredentialHistory{{Version: 1}}
					case "missing-time":
						op.SubmittedAt = nil
					case "zero-time":
						op.SubmittedAt = &metav1.Time{}
					case "id":
						op.ID = "bad"
					case "owner":
						op.ConfigUID = ""
					case "generation":
						op.ConfigGeneration = 0
					case "unselected":
						op.Resource = strings.Replace(resource, "CVK_LAB", "UNSELECTED", 1)
					case "prefix":
						if mixed {
							op.Resource = entry.resource
						} else {
							op.Resource = entry.name + "/" + resource
						}
					}
					before := op.DeepCopy()
					if plan.canObserveUnknownTask(op) != (mode == "known" || mode == "create") {
						t.Fatal("incorrect recovery scope")
					}
					if !reflect.DeepEqual(op, before) {
						t.Fatal("operation mutated by selector")
					}
				})
			}
		}
	}
	if wirelessDefinitionKnownTaskRecovery(&tagExecutor{}) != nil {
		t.Fatal("unrelated executor qualified")
	}
	if wirelessDefinitionKnownTaskRecovery(&wirelessConfiguration{configurationExecutor: &wirelessAnchorExecutor{}}) != nil {
		t.Fatal("empty selection qualified")
	}
}

func TestWirelessDefinitionRecoveryPublic(t *testing.T) {
	for _, entry := range definitionRecoveryCases {
		for _, mode := range []string{"known", "taskless", "foreign-task", "changed-source", "replaced-target", "missing-target", "pending-task", "failed-task", "child-failed", "child-pending", "child-foreign", "tree-unavailable", "unknown-create", "create-taskless", "create-foreign-task", "create-changed-source", "create-missing-target", "create-pending-task", "create-failed-task", "create-child-failed", "create-child-pending", "create-child-foreign", "create-tree-unavailable"} {
			t.Run(entry.name+"/"+mode, func(t *testing.T) { exerciseWirelessDefinitionRecoveryPublic(t, entry, mode, nil) })
		}
	}
}

func exerciseWirelessDefinitionRecoveryPublic(t *testing.T, entry definitionRecoveryCase, mode string, install func(*adapter, *configapi.NetworkControllerConfig), acceptedCreate ...bool) {
	t.Helper()
	ordinaryCreate := len(acceptedCreate) == 1 && acceptedCreate[0]
	createRecovery := ordinaryCreate || mode == "unknown-create" || strings.HasPrefix(mode, "create-")
	if mode == "unknown-create" {
		mode = "known"
	} else if createRecovery {
		mode = strings.TrimPrefix(mode, "create-")
	}
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"wireless"}
	config.Spec.Source.Inline.Raw = []byte(entry.model)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if install != nil {
		install(a, config)
	}
	rows := []map[string]any{}
	writes := 0
	stamp := int64(0)
	taskReads := 0
	recovering := false
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == devicesPath:
			fmt.Fprint(w, `{"response":[]}`)
		case r.URL.Path == entry.path && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.URL.Path == entry.path && r.Method == http.MethodPost || r.URL.Path == entry.path+"/native-id" && r.Method == http.MethodPut:
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid body")
				w.WriteHeader(400)
				return
			}
			writes++
			stamp = time.Now().UnixMilli()
			body["id"] = "native-id"
			rows = []map[string]any{body}
			fmt.Fprintf(w, `{"response":{"taskId":"task-%d"}}`, writes)
		case strings.HasPrefix(r.URL.Path, taskPath):
			taskReads++
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			end := stamp
			failed := false
			if recovering {
				switch mode {
				case "foreign-task":
					id = "different-task"
				case "pending-task":
					end = 0
				case "failed-task":
					failed = true
				}
			}
			root := map[string]any{"id": id, "startTime": stamp, "endTime": end, "isError": failed}
			if recovering && mode == "tree-unavailable" && strings.HasSuffix(r.URL.Path, "/tree") {
				w.WriteHeader(404)
				return
			}
			children := []map[string]any{}
			if recovering && (mode == "child-failed" || mode == "child-pending" || mode == "child-foreign") {
				child := map[string]any{"id": "child-task", "rootId": id, "parentId": id, "startTime": stamp, "endTime": stamp, "isError": false}
				switch mode {
				case "child-failed":
					child["isError"] = true
				case "child-pending":
					child["endTime"] = 0
				case "child-foreign":
					child["rootId"] = "unrelated-task"
				}
				children = append(children, child)
			}
			writeDefinitionTaskFixture(w, r, root, children...)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	key := ctrlclient.ObjectKeyFromObject(config)
	tick := func() {
		t.Helper()
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
	}
	injected := false
	stages := 2
	if createRecovery {
		stages = 1
	}
	for stage := range stages {
		if stage == 1 {
			config.Generation++
			config.Spec.Source.Inline.Raw = []byte(strings.Replace(entry.model, entry.from, entry.to, 1))
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
		}
		for range 20 {
			tick()
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if nc.Status.Configuration != nil {
				op := nc.Status.Configuration.Operation
				if op != nil && op.Phase == "Accepted" && !injected && (stage == 1 || createRecovery) {
					if !ordinaryCreate {
						if err := a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
							t.Fatal(err)
						}
					}
					nc, err = a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if mode == "taskless" {
						nc.Status.Configuration.Operation.TaskID = ""
					}
					raw, _ := json.Marshal(nc)
					var restored cvk.NetworkController
					if err := json.Unmarshal(raw, &restored); err != nil {
						t.Fatal(err)
					}
					if err := a.statusWriter.Status().Update(ctx, &restored); err != nil {
						t.Fatal(err)
					}
					switch mode {
					case "changed-source":
						config.Spec.Source.Inline.Raw = []byte(entry.model)
						if createRecovery {
							config.Spec.Source.Inline.Raw = []byte(strings.Replace(entry.model, entry.from, entry.to, 1))
						}
						config.Generation++
						if err := a.statusWriter.Update(ctx, config); err != nil {
							t.Fatal(err)
						}
					case "replaced-target":
						rows[0]["id"] = "different-native"
					case "missing-target":
						rows = []map[string]any{}
					}
					injected = true
					recovering = true
				}
			}
			if injected && mode != "known" {
				for range 4 {
					tick()
				}
				break
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				break
			}
		}
	}
	if !injected {
		t.Fatal("did not reach accepted operation", config.Status)
	}
	expected := 2
	if createRecovery {
		expected = 1
	}
	if writes != expected {
		t.Fatal("replayed native request", writes)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "known" {
		if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || nc.Status.Configuration.Operation != nil {
			t.Fatal("known task not recovered", config.Status)
		}
		tick()
		if writes != expected {
			t.Fatal("no-op replayed")
		}
		if createRecovery && (len(nc.Status.Configuration.ActionApplications) != 0 || len(nc.Status.Configuration.CredentialApplications) != 0) {
			t.Fatal("unowned Create convergence fabricated ownership receipts")
		}
	} else if nc.Status.Configuration.Operation == nil {
		t.Fatal("uncertain journal released", mode, taskReads)
	} else if ordinaryCreate && nc.Status.Configuration.Operation.Phase != "Accepted" {
		t.Fatal("ordinary accepted Create unexpectedly changed phase", mode)
	}
}

func TestWirelessDefinitionRecoveryCombined(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"wireless"}
	rows := map[string][]map[string]any{}
	stamps := map[string]int64{}
	writes := map[string]int{}
	total := 0
	for _, e := range definitionRecoveryCases {
		rows[e.path] = []map[string]any{}
	}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == devicesPath:
			fmt.Fprint(w, `{"response":[]}`)
		case strings.HasPrefix(r.URL.Path, taskPath):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			stamp, ok := stamps[id]
			if !ok {
				t.Error("unbound task read")
				w.WriteHeader(404)
				return
			}
			writeDefinitionTaskFixture(w, r, map[string]any{"id": id, "isError": false, "startTime": stamp, "endTime": stamp})
		default:
			for _, entry := range definitionRecoveryCases {
				if r.URL.Path == entry.path && r.Method == http.MethodGet {
					json.NewEncoder(w).Encode(map[string]any{"response": rows[entry.path]})
					return
				}
				if r.URL.Path == entry.path && r.Method == http.MethodPost || r.URL.Path == entry.path+"/native-"+entry.name && r.Method == http.MethodPut {
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Error("invalid payload")
						w.WriteHeader(400)
						return
					}
					body["id"] = "native-" + entry.name
					rows[entry.path] = []map[string]any{body}
					total++
					writes[entry.name]++
					task := fmt.Sprintf("combined-task-%d", total)
					stamps[task] = time.Now().UnixMilli()
					fmt.Fprintf(w, `{"response":{"taskId":%q}}`, task)
					return
				}
			}
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	recovered := map[string]bool{}
	key := ctrlclient.ObjectKeyFromObject(config)
	for stage := range 2 {
		if err := a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
		config.Spec.ManagedSections = []string{"wireless"}
		config.Generation++
		sections := map[string]any{}
		for _, entry := range definitionRecoveryCases {
			model := entry.model
			if stage == 1 {
				model = strings.Replace(model, entry.from, entry.to, 1)
			}
			var doc map[string]map[string]map[string]any
			if err := json.Unmarshal([]byte(model), &doc); err != nil {
				t.Fatal(err)
			}
			sections[entry.name] = doc["catalyst_center"]["wireless"][entry.name]
		}
		raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"wireless": sections}})
		config.Spec.Source.Inline.Raw = raw
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		converged := false
		for range 100 {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if nc.Status.Configuration != nil {
				op := nc.Status.Configuration.Operation
				if op != nil && op.Phase == "Accepted" && (op.Action == "Update" || op.Action == "Create") {
					if recovered[op.ID] {
						t.Fatal("repeated accepted update")
					}
					op.Phase = "Unknown"
					raw, _ := json.Marshal(nc)
					var restored cvk.NetworkController
					if err := json.Unmarshal(raw, &restored); err != nil {
						t.Fatal(err)
					}
					if err := a.statusWriter.Status().Update(ctx, &restored); err != nil {
						t.Fatal(err)
					}
					recovered[op.ID] = true
				}
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
			t.Fatal("combined recovery not converged", stage, config.Status)
		}
	}
	if len(recovered) != 12 || total != 12 {
		t.Fatal("incorrect combined mutation/recovery counts", total, len(recovered))
	}
	for _, entry := range definitionRecoveryCases {
		if writes[entry.name] != 2 {
			t.Fatal("replayed collection", entry.name, writes[entry.name])
		}
	}
	for range 3 {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
	}
	if total != 12 {
		t.Fatal("combined no-op replayed")
	}
	if len(journalState(t, a).ActionApplications) != 0 {
		t.Fatal("combined unowned recovery fabricated creator receipts")
	}
}

func TestWirelessDefinitionRecoveryPreservesSSIDFallback(t *testing.T) {
	plan, err := (&client{}).compileConfigurationApply([]byte(wirelessAnchorModel), []string{"wireless"}, nil)
	if err != nil || plan.recoverKnownTask == nil {
		t.Fatal(err)
	}
	_, state, _ := ssidQualificationProof(t, 2)
	op := state.Operation
	op.Phase = "Unknown"
	before := op.DeepCopy()
	if !plan.canObserveUnknownTask(op) {
		t.Fatal("existing credential recovery lost in mixed plans")
	}
	if !reflect.DeepEqual(before, op) {
		t.Fatal("credential evidence changed")
	}
	op.SSIDCredentialHistory = nil
	if plan.canObserveUnknownTask(op) {
		t.Fatal("pruned credential history gained recovery")
	}
}

// Explicit test-service responses for endpoints that expose both task APIs.
// Child outcomes remain independently supplied by each test, never inferred
// from a successful root. Production uses the existing shared tree validator.
func writeDefinitionTaskFixture(w http.ResponseWriter, r *http.Request, task map[string]any, children ...map[string]any) {
	if strings.HasSuffix(r.URL.Path, "/tree") {
		task["rootId"] = task["id"]
		task["parentId"] = ""
		rows := append([]map[string]any{task}, children...)
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
	} else {
		json.NewEncoder(w).Encode(map[string]any{"response": task})
	}
}

func TestWirelessDefinitionCompletionRequiresWholeTree(t *testing.T) {
	for _, entry := range append(append([]definitionRecoveryCase{}, definitionRecoveryCases...), wirelessAPDispatchCase) {
		for _, phase := range []string{"Accepted", "Unknown", "AcceptedCreate", "UnknownCreate"} {
			for _, mode := range []string{"complete", "failed-child", "pending-child", "foreign-child", "unavailable"} {
				t.Run(entry.name+"/"+phase+"/"+mode, func(t *testing.T) {
					now := time.Now().UTC()
					submitted := metav1.NewTime(now.Add(-time.Second))
					op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, Section: "wireless", Resource: entry.resource, Action: "Update", Phase: phase, TaskID: "bound-task", TargetID: "native-id", SubmittedAt: &submitted}
					if phase == "UnknownCreate" || phase == "AcceptedCreate" {
						op.Phase = strings.TrimSuffix(phase, "Create")
						op.Action = "Create"
						op.TargetID = ""
					}
					trees := 0
					c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == authPath {
							fmt.Fprint(w, `{"Token":"test"}`)
							return
						}
						if r.Method != http.MethodGet || r.URL.Path != taskPath+"bound-task/tree" {
							t.Error("unexpected root fallback or mutation")
							w.WriteHeader(500)
							return
						}
						trees++
						if mode == "unavailable" {
							w.WriteHeader(404)
							return
						}
						root := map[string]any{"id": "bound-task", "startTime": submitted.UnixMilli(), "endTime": now.UnixMilli(), "isError": false}
						child := map[string]any{"id": "child", "parentId": "bound-task", "rootId": "bound-task", "startTime": submitted.UnixMilli(), "endTime": now.UnixMilli(), "isError": false}
						switch mode {
						case "failed-child":
							child["isError"] = true
						case "pending-child":
							child["endTime"] = 0
						case "foreign-child":
							child["rootId"] = "another-root"
						}
						writeDefinitionTaskFixture(w, r, root, child)
					})
					plan, err := c.compileConfigurationApply([]byte(entry.model), []string{"wireless"}, nil)
					if err != nil {
						t.Fatal(err)
					}
					before := op.DeepCopy()
					done, err := plan.taskComplete(context.Background(), c, op, now)
					if mode == "complete" {
						if !done || err != nil {
							t.Fatal("valid tree refused", err)
						}
					} else if mode == "pending-child" {
						if done || err != nil {
							t.Fatal("pending child completed", err)
						}
					} else if done || err == nil {
						t.Fatal("incomplete/untrusted tree accepted")
					}
					if trees != 1 || !reflect.DeepEqual(before, op) {
						t.Fatal("observer replayed or changed retained phase")
					}
				})
			}
		}
	}
}

func TestWirelessDefinitionAcceptedCreatePublic(t *testing.T) {
	entries := append(append([]definitionRecoveryCase{}, definitionRecoveryCases...), wirelessAPDispatchCase)
	for _, entry := range entries {
		for _, mode := range []string{"known", "child-failed", "child-pending", "child-foreign", "tree-unavailable", "changed-source"} {
			t.Run(entry.name+"/"+mode, func(t *testing.T) { exerciseWirelessDefinitionRecoveryPublic(t, entry, mode, nil, true) })
		}
	}
}
