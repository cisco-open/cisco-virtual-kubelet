// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

type wirelessProfileRecoveryQualification struct {
	mode, target                        string
	receipts                            *wirelessTagReceiptQualification
	install                             func(*adapter, *configapi.NetworkControllerConfig)
	tasks                               map[string]int64
	recovered, trees, injected, retired map[string]bool
	pending                             string
	before                              *cvk.NetworkControllerConfigurationStatus
	writes, beforeWrites                int
	interrupted                         *cvk.NetworkControllerConfigurationOperation
}

func (q *wirelessProfileRecoveryQualification) installFixture(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig) {
	q.tasks = map[string]int64{}
	q.recovered = map[string]bool{}
	q.trees = map[string]bool{}
	q.injected = map[string]bool{}
	q.retired = map[string]bool{}
	if q.receipts != nil {
		q.receipts.keyPath, q.receipts.key = configureWirelessTagComparisonFixture(t, a, c)
	}
	if q.install != nil {
		q.install(a, c)
	}
	if q.receipts != nil {
		q.receipts.seed(t, a)
	}
}
func (q *wirelessProfileRecoveryQualification) matches(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil {
		return false
	}
	kind, _, ok := networkProfileMetadataResource(op.Resource)
	return ok && (q.target == "all" || kind == q.target)
}
func (q *wirelessProfileRecoveryQualification) refuseRead(t *testing.T, a *adapter, r *http.Request) bool {
	if q.mode != "no-dispatch" || r.Method != "GET" || r.URL.Path != wirelessProfilesPath {
		return false
	}
	nc, err := a.currentController(r.Context())
	if err != nil {
		t.Error(err)
		return true
	}
	if nc.Status.Configuration == nil {
		return false
	}
	op := nc.Status.Configuration.Operation
	if !q.matches(op) || op.Phase != "Submitting" {
		return false
	}
	key := fmt.Sprintf("%d/%s/%s", op.ConfigGeneration, op.Action, op.Resource)
	if q.injected[key] {
		return false
	}
	if !noDispatchOperationSupported(op) || q.pending != "" {
		t.Error("unqualified or overlapping refusal")
		return true
	}
	q.injected[key] = true
	q.pending = key
	q.before = nc.Status.Configuration.DeepCopy()
	q.beforeWrites = q.writes
	return true
}
func (q *wirelessProfileRecoveryQualification) respondTask(t *testing.T, a *adapter, w http.ResponseWriter, stamp int64) {
	id := fmt.Sprintf("task-%d", len(q.tasks)+1)
	q.tasks[id] = stamp
	nc, err := a.currentController(context.Background())
	if err != nil {
		t.Error(err)
		w.WriteHeader(500)
		return
	}
	if q.mode == "lost-ack" && q.matches(nc.Status.Configuration.Operation) {
		w.WriteHeader(503)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": id}})
}
func (q *wirelessProfileRecoveryQualification) respondObservation(t *testing.T, a *adapter, w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
	stamp, ok := q.tasks[id]
	if !ok {
		t.Error("observation invented a task")
		w.WriteHeader(404)
		return
	}
	root := map[string]any{"id": id, "isError": false, "startTime": stamp, "endTime": stamp}
	nc, err := a.currentController(r.Context())
	if err != nil {
		t.Error(err)
		w.WriteHeader(500)
		return
	}
	op := nc.Status.Configuration.Operation
	selected := q.matches(op) && op.Phase == "Unknown"
	if strings.HasSuffix(r.URL.Path, "/tree") && selected {
		q.trees[id] = true
	}
	if selected && q.mode == "tree-missing" && strings.HasSuffix(r.URL.Path, "/tree") {
		w.WriteHeader(404)
		return
	}
	if selected && q.mode == "pending" {
		root["endTime"] = 0
	}
	children := []map[string]any{}
	if selected && q.mode == "child-failed" {
		children = append(children, map[string]any{"id": id + "-child", "rootId": id, "parentId": id, "isError": true, "startTime": stamp, "endTime": stamp})
	}
	writeDefinitionTaskFixture(w, r, root, children...)
}
func (q *wirelessProfileRecoveryQualification) failureExpected() bool {
	return q.mode != "complete" && q.mode != "no-dispatch"
}
func (q *wirelessProfileRecoveryQualification) afterTick(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, profiles []map[string]any, tags map[string][]map[string]any, writes int) {
	t.Helper()
	q.writes = writes
	state := journalState(t, a)
	if q.pending != "" {
		for _, condition := range config.Status.Conditions {
			if condition.Reason == "NativePreflightRefused" {
				if state.Operation != nil || writes != q.beforeWrites || !reflect.DeepEqual(state.Owners, q.before.Owners) || !reflect.DeepEqual(state.ActionApplications, q.before.ActionApplications) {
					t.Fatal("refusal changed effects or custody")
				}
				q.retired[q.pending] = true
				q.pending = ""
				break
			}
		}
	}
	op := state.Operation
	if q.mode == "lost-ack" && q.matches(op) && op.Phase == "Unknown" && q.interrupted == nil {
		q.interrupted = op.DeepCopy()
		q.beforeWrites = writes
		return
	}
	if !q.matches(op) || op.Phase != "Accepted" || q.recovered[op.ID] {
		return
	}
	q.recovered[op.ID] = true
	if err := a.advanceConfiguration(context.Background(), op.ID, "Unknown", ""); err != nil {
		t.Fatal(err)
	}
	if !q.failureExpected() {
		return
	}
	q.interrupted = journalState(t, a).Operation.DeepCopy()
	q.beforeWrites = writes
	switch q.mode {
	case "source":
		current := string(config.Spec.Source.Inline.Raw)
		changed := strings.Replace(current, "WLAN1", "DifferentWLAN", 1)
		if changed == current {
			changed = strings.Replace(current, "WLAN2", "DifferentWLAN", 1)
		}
		if changed == current {
			t.Fatal("source-change fixture did not change source")
		}
		config.Spec.Source.Inline.Raw = []byte(changed)
		config.Generation++
		if err := a.statusWriter.Update(context.Background(), config); err != nil {
			t.Fatal(err)
		}
	case "owner":
		nc, err := a.currentController(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		nc.Status.Configuration.Owners[0].ConfigUID = "different-owner"
		if err := a.statusWriter.Status().Update(context.Background(), nc); err != nil {
			t.Fatal(err)
		}
	case "managed-drift":
		if q.target == "profile" {
			profiles[0]["ssidDetails"].([]any)[0].(map[string]any)["wlanProfileName"] = "external"
		} else {
			kind := "siteTags"
			if q.target != "site-tag" {
				kind = "policyTags"
			}
			tags[kind][0]["siteIds"] = []any{}
		}
	}
}
func (q *wirelessProfileRecoveryQualification) assertRetained(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, writes int) {
	t.Helper()
	op := journalState(t, a).Operation
	if q.interrupted == nil || !reflect.DeepEqual(op, q.interrupted) || writes != q.beforeWrites || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("uncertain profile/tag outcome settled or replayed")
	}
	if (op.TaskID == "") != (q.mode == "lost-ack") {
		t.Fatal("acknowledgement identity changed")
	}
}
func (q *wirelessProfileRecoveryQualification) assertComplete(t *testing.T, a *adapter) {
	t.Helper()
	if journalState(t, a).Operation != nil || len(q.recovered) < 3 || len(q.trees) < len(q.recovered) {
		t.Fatal("original profile/tag task recovery not proven")
	}
	if q.mode == "no-dispatch" && (len(q.injected) < 3 || len(q.injected) != len(q.retired) || q.pending != "") {
		t.Fatal("not all live refusal proofs retired", len(q.injected), len(q.retired), q.pending)
	}
}

func TestWirelessProfilePublicRecovery(t *testing.T) {
	for _, cohort := range []bool{false, true} {
		for _, mode := range []string{"complete", "no-dispatch"} {
			t.Run(fmt.Sprintf("cohort-%t/%s", cohort, mode), func(t *testing.T) {
				qualifyWirelessProfileManifestRestart(t, cohort, "", &wirelessProfileRecoveryQualification{mode: mode, target: "all"})
			})
		}
	}
	for _, target := range []string{"profile", "site-tag", "policy-tag", "policy-cohort"} {
		for _, mode := range []string{"lost-ack", "child-failed", "tree-missing", "pending", "managed-drift", "source", "owner"} {
			t.Run(target+"/"+mode, func(t *testing.T) {
				qualifyWirelessProfileManifestRestart(t, target == "policy-cohort", "", &wirelessProfileRecoveryQualification{mode: mode, target: target})
			})
		}
	}
}

func TestWirelessProfileRecoveryEnvelope(t *testing.T) {
	e, err := (&client{}).compileNetworkProfileConfiguration(wirelessProfileTestModel(map[string]any{"name": "P", "ssid_details": []any{map[string]any{"name": "S"}}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "taskless", "no-parent", "wrong-parent-format", "wrong-section", "undeclared", "malformed-binding", "future", "physical", "prepared", "credential", "created-id", "missing-owner", "wrong-action", "update-targetless", "unknown-family"} {
		t.Run(mode, func(t *testing.T) {
			now := metav1.Now()
			op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), Section: "network_profiles", Resource: "wireless/wireless-profile:P:" + strings.Repeat("b", 64), ConfigName: "C", ConfigUID: "config", ConfigGeneration: 1, ControllerGeneration: 1, ParentID: "global", Action: "Create", Phase: "Unknown", TaskID: "task", SubmittedAt: &now}
			switch mode {
			case "taskless":
				op.TaskID = ""
			case "no-parent":
				op.ParentID = ""
			case "wrong-parent-format":
				op.ParentID = "bad/parent"
			case "wrong-section":
				op.Section = "wireless"
			case "undeclared":
				op.Resource = "wireless/wireless-profile:Other:" + strings.Repeat("b", 64)
			case "malformed-binding":
				op.Resource = "wireless/wireless-profile:P:" + strings.Repeat("B", 64)
			case "future":
				op.Resource = "wireless/wireless-profile:v9:P:" + strings.Repeat("b", 64)
			case "physical":
				op.TargetSerial = "SERIAL"
			case "prepared":
				op.PreparedAt = &now
			case "credential":
				op.CredentialVersions = []cvk.NetworkControllerConfigurationCredentialVersion{{}}
			case "created-id":
				op.CreatedResourceID = "native"
			case "missing-owner":
				op.ConfigUID = ""
			case "wrong-action":
				op.Action = "Delete"
			case "update-targetless":
				op.Action = "Update"
			case "unknown-family":
				op.Resource = "wireless-sites/profile-site:P"
			}
			if e.selectsProfileMetadata(op) != (mode == "valid") {
				t.Fatal("unsupported journal gained native observation")
			}
		})
	}
}
