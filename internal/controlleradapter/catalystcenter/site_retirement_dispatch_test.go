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

var siteRetirementDispatchModes = []string{"complete", "latest", "long-task", "unknown", "pending", "failed", "lost-ack", "unauthorized", "server-error", "source", "owner", "key", "native", "consumer", "plan-capacity", "plan-bytes", "late-capacity", "prepared", "prepared-drift", "retired-child", "retired-grandchild", "live-child", "child-reappeared", "grandchild-reappeared", "local-policy", "local-credentials", "local-shape", "prepared-local-drift", "lan-session", "lan-device", "lan-legacy", "lan-unavailable"}

func TestSiteRetirementDispatch(t *testing.T) {
	for _, cm := range []bool{false, true} {
		for _, mode := range siteRetirementDispatchModes {
			t.Run(fmt.Sprintf("cm-%t/%s", cm, mode), func(t *testing.T) { exerciseSiteRetirementBinding(t, "write-"+mode, cm, nil) })
		}
	}
}

type siteRetirementDispatchFixture struct {
	mode, extra                       string
	active, changed, deleted, pending bool
	calls                             int
	started, ended                    time.Time
	change                            func()
}

func (q *siteRetirementDispatchFixture) taskID() string {
	if q.mode == "long-task" {
		return strings.Repeat("t", 128)
	}
	return "retirement-task"
}

func (q *siteRetirementDispatchFixture) handle(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.Method == http.MethodDelete {
		if r.URL.Path != siteDesignMutationEndpoint("area")+"/"+siteOwnedTestUUID || r.ContentLength > 0 {
			t.Error("retirement escaped exact empty-body endpoint")
			w.WriteHeader(500)
			return true
		}
		q.calls++
		if q.mode == "unauthorized" {
			w.WriteHeader(401)
			return true
		}
		q.deleted = true
		q.ended = time.Now().UTC()
		if q.mode == "server-error" {
			w.WriteHeader(500)
			fmt.Fprint(w, "private native error")
			return true
		}
		if q.mode == "lost-ack" {
			fmt.Fprint(w, `{"response":{}}`)
			return true
		}
		fmt.Fprintf(w, `{"response":{"taskId":%q,"url":"https://untrusted.example/task"}}`, q.taskID())
		return true
	}
	if r.Method != http.MethodGet {
		return false
	}
	if r.URL.Path == lanSessionStatusPath && strings.HasPrefix(q.mode, "lan-") {
		if q.mode == "lan-unavailable" {
			w.WriteHeader(503)
			return true
		}
		session := lanSessionFixture()
		session.Site = "Global/Probe"
		devices := []lanSessionDiscoveryDevice{}
		session.DiscoveryDevices = &devices
		if q.mode == "lan-device" {
			session.Site = "Global/Other"
			devices = append(devices, lanSessionDiscoveryDevice{IP: "192.0.2.20", SiteID: siteOwnedTestUUID})
		}
		if q.mode == "lan-legacy" {
			session.DiscoveryDevices = nil
		}
		json.NewEncoder(w).Encode(map[string]any{"response": []lanSession{session}})
		return true
	}
	if r.URL.Path == sitesPath+"/"+siteOwnedTestUUID+"/ntpSettings" && (q.mode == "local-policy" || q.mode == "local-shape" || q.mode == "prepared-local-drift") {
		value := map[string]any{}
		if q.mode == "local-policy" {
			value["servers"] = []string{"192.0.2.1"}
		}
		json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"ntp": value}})
		return true
	}
	if q.mode == "local-credentials" && r.URL.Path == siteCredentialsPath(siteOwnedTestUUID) {
		response, _ := siteLocalConfigurationFixture(r.URL.Path, siteOwnedTestUUID)
		response[credentialSlot("cli_credentials")] = map[string]any{"credentialsId": "private-fixture-credential"}
		json.NewEncoder(w).Encode(map[string]any{"response": response})
		return true
	}
	if r.URL.Path == sitesPath {
		if !q.changed {
			q.changed = true
			if q.change != nil {
				q.change()
			}
		}
		rows := []any{Site{ID: "root", Name: "Global", NameHierarchy: "Global", Type: "global"}}
		if q.mode == "lan-device" {
			rows = append(rows, Site{ID: "other", Name: "Other", NameHierarchy: "Global/Other", ParentID: "root", Type: "area"})
		}
		if q.mode == "child-reappeared" || q.mode == "grandchild-reappeared" {
			id := "22222222-2222-4222-8222-222222222222"
			if q.mode == "grandchild-reappeared" {
				id = "33333333-3333-4333-8333-333333333333"
			}
			rows = append(rows, Site{ID: id, Name: "Moved", NameHierarchy: "Global/Moved", ParentID: "root", Type: "area"})
		}
		if !q.deleted {
			raw, _ := json.Marshal(Site{ID: siteOwnedTestUUID, Name: "Probe", NameHierarchy: "Global/Probe", ParentID: "root", Type: "area"})
			var row map[string]any
			json.Unmarshal(raw, &row)
			if q.extra != "" {
				row["nativePrivateField"] = q.extra
			}
			rows = append(rows, row)
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
		return true
	}
	if r.URL.Path == assignedDevicesPath && q.mode == "consumer" {
		fmt.Fprintf(w, `{"response":[{"deviceId":"consumer","siteId":%q}]}`, siteOwnedTestUUID)
		return true
	}
	if r.URL.Path == taskPath+q.taskID()+"/tree" {
		end := q.ended.UnixMilli()
		if q.pending {
			end = 0
		}
		row := func(id, parent string) map[string]any {
			return map[string]any{"id": id, "parentId": parent, "rootId": q.taskID(), "isError": false, "startTime": q.started.UnixMilli(), "endTime": end}
		}
		rows := []map[string]any{row(q.taskID(), ""), row("child", q.taskID())}
		if q.mode == "failed" {
			rows[1]["isError"] = true
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
		return true
	}
	return false
}

func (q *siteRetirementDispatchFixture) execute(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, keyPath string, cm *core.ConfigMap, updateState func(func(*cvk.NetworkController))) {
	t.Helper()
	ctx := context.Background()
	if q.mode == "plan-capacity" || q.mode == "plan-bytes" {
		updateState(func(n *cvk.NetworkController) {
			fillSiteRetirementHistory(n.Status.Configuration, q.mode == "plan-bytes")
		})
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := nc.Status.Configuration.DeepCopy()
	write, err := a.siteRetirementWrite(ctx, nc, config, source, siteOwnedTestUUID)
	if q.mode == "plan-capacity" || q.mode == "plan-bytes" || q.mode == "live-child" {
		if err == nil || write != nil || !reflect.DeepEqual(before, journalState(t, a)) || q.calls != 0 {
			t.Fatal("exhausted capacity exposed retirement writer or changed journal")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if !noDispatchWriteSupported("sites", write) {
		t.Fatal("retirement bypassed common no-dispatch capability")
	}
	q.change = func() {
		switch q.mode {
		case "source":
			if config.Spec.Source.ConfigMapRef != nil {
				cm.Data["changed"] = "source changed"
				if err := a.statusWriter.Update(ctx, cm); err != nil {
					t.Fatal(err)
				}
			} else {
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
		case "owner":
			updateState(func(n *cvk.NetworkController) { n.Status.Configuration.Owners[0].ConfigUID = "foreign" })
		case "key":
			if err := os.WriteFile(keyPath, []byte(strings.Repeat("f", 64)), 0600); err != nil {
				t.Fatal(err)
			}
		case "native":
			q.extra = "private late drift"
		}
	}
	if q.mode == "late-capacity" {
		updateState(func(n *cvk.NetworkController) { fillSiteRetirementHistory(n.Status.Configuration, false) })
	}
	if strings.HasPrefix(q.mode, "prepared") {
		op, err := a.prepareConfiguration(ctx, config, source, "sites", write.resource, "Delete", write.identity)
		if err != nil {
			t.Fatal(err)
		}
		q.active = true
		if q.mode == "prepared-drift" {
			q.extra = "private prepared drift"
		}
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		restarted := *a
		resumed, err := restarted.siteRetirementWrite(ctx, current, config, source, siteOwnedTestUUID)
		if q.mode == "prepared-drift" || q.mode == "prepared-local-drift" {
			if err == nil || resumed != nil || q.calls != 0 {
				t.Fatal("changed prepared native state acquired submission")
			}
			if err := a.finishConfiguration(ctx, op.ID); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err != nil || resumed.resource != write.resource {
			t.Fatal("exact preparation did not reconstruct", err)
		}
		if err := a.submitPreparedConfigurationReceipt(ctx, config, source, op, "sites", resumed.resource, "Delete", configurationTaskWriteReceipt("sites", resumed), nil, resumed.identity); err != nil {
			t.Fatal(err)
		}
		write = resumed
	} else {
		q.active = true
		err = a.submitConfigurationWrite(ctx, config, source, "sites", write)
	}
	state := journalState(t, a)
	refused := q.mode == "source" || q.mode == "key" || q.mode == "native" || q.mode == "consumer" || q.mode == "late-capacity" || q.mode == "child-reappeared" || q.mode == "grandchild-reappeared" || strings.HasPrefix(q.mode, "local-") || strings.HasPrefix(q.mode, "lan-")
	if refused {
		if !errors.Is(err, errConfigurationNotDispatched) || q.calls != 0 || state.Operation != nil || len(state.Owners) != 1 {
			t.Fatal("preflight refusal did not use proven no-dispatch retirement", err)
		}
		return
	}
	if q.mode == "owner" {
		if err == nil || q.calls != 0 || state.Operation == nil {
			t.Fatal("owner change lost its fence", err)
		}
		return
	}
	uncertain := q.mode == "lost-ack" || q.mode == "unauthorized" || q.mode == "server-error"
	if uncertain {
		if err == nil || state.Operation == nil || state.Operation.Phase != "Unknown" || state.Operation.TaskID != "" || q.calls != 1 {
			t.Fatal("uncertain submission was replayed or released", err)
		}
		if err := a.submitConfigurationWrite(ctx, config, source, "sites", write); err == nil || q.calls != 1 {
			t.Fatal("uncertain writer resubmitted")
		}
		if err := a.recordSiteRetired(ctx, config, source, state.Operation.ID); err == nil {
			t.Fatal("taskless uncertainty became a retirement receipt")
		}
		return
	}
	if err != nil || q.calls != 1 || state.Operation == nil || state.Operation.Phase != "Accepted" || state.Operation.TaskID != q.taskID() {
		t.Fatal("single acknowledged retirement submission failed", err)
	}
	q.started = state.Operation.SubmittedAt.Time
	if _, err := write.submit(ctx); err == nil || q.calls != 1 {
		t.Fatal("one-use writer submitted twice")
	}
	if err := a.submitConfigurationWrite(ctx, config, source, "sites", write); err == nil || q.calls != 1 {
		t.Fatal("accepted writer resubmitted")
	}
	if q.mode == "unknown" {
		updateState(func(n *cvk.NetworkController) { n.Status.Configuration.Operation.Phase = "Unknown" })
	}
	restarted := *a
	if q.mode == "pending" {
		q.pending = true
		held := journalState(t, a).DeepCopy()
		if err := restarted.recordSiteRetired(ctx, config, source, state.Operation.ID); err == nil || !reflect.DeepEqual(held, journalState(t, a)) {
			t.Fatal("pending task retired custody")
		}
		q.pending = false
	}
	if q.mode == "failed" {
		if err := restarted.recordSiteRetired(ctx, config, source, state.Operation.ID); err == nil || journalState(t, a).Operation == nil || q.calls != 1 {
			t.Fatal("failed task retired custody")
		}
		return
	}
	if err := restarted.recordSiteRetired(ctx, config, source, state.Operation.ID); err != nil {
		t.Fatal("original-task retirement did not publish", err)
	}
	if err := restarted.finishConfiguration(ctx, state.Operation.ID); err != nil {
		t.Fatal(err)
	}
	settled := journalState(t, a)
	if settled.Operation != nil || q.calls != 1 || len(settled.ActionApplications) != len(before.ActionApplications)+1 || !reflect.DeepEqual(settled.Owners, before.Owners) {
		t.Fatal("retirement settlement lost bounded lineage or replayed")
	}
	for i, row := range before.ActionApplications {
		if !reflect.DeepEqual(row, settled.ActionApplications[i]) {
			t.Fatal("retirement changed creator/latest lineage")
		}
	}
	if strings.HasPrefix(q.mode, "recreate-") {
		exerciseSiteRecreationExecution(t, a, config, keyPath, cm, strings.TrimPrefix(q.mode, "recreate-"))
	}
}
