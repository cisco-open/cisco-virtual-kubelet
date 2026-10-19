// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var siteRetirementRecoveryModes = []string{"accepted", "latest", "latest-pruned", "edited-state-proof", "edited-ancestry-proof", "unknown", "verifying", "present", "replacement", "reused-uuid", "parent-replaced", "parent-settings", "reappear", "pending-child", "failed-child", "missing-tree", "old-tree", "future-tree", "changed-completion", "final-failure", "taskless", "borrowed-task", "mismatched-task", "legacy-selector", "current-missing", "task-source", "task-owner", "task-key", "absence-history", "absence-phase"}

func TestSiteRetirementTaskRecovery(t *testing.T) {
	for _, cm := range []bool{false, true} {
		for _, mode := range siteRetirementRecoveryModes {
			t.Run(fmt.Sprintf("cm-%t/%s", cm, mode), func(t *testing.T) { exerciseSiteRetirementBinding(t, "recovery-"+mode, cm, nil) })
		}
	}
}

type siteRetirementRecoveryFixture struct {
	mode                 string
	active               bool
	now, submitted       time.Time
	treeReads, siteReads int
	change               func(string)
}

func (q *siteRetirementRecoveryFixture) handle(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.URL.Path == taskPath+"retirement-task/tree" {
		q.treeReads++
		if q.treeReads == 1 && strings.HasPrefix(q.mode, "task-") {
			q.change(q.mode)
		}
		if q.mode == "missing-tree" {
			w.WriteHeader(503)
			return true
		}
		start, end := q.submitted.UnixMilli(), q.submitted.Add(time.Millisecond).UnixMilli()
		if q.mode == "old-tree" {
			start = q.submitted.Add(-24 * time.Hour).UnixMilli()
		}
		if q.mode == "future-tree" {
			end = q.now.Add(time.Second).UnixMilli()
		}
		if q.mode == "changed-completion" && q.treeReads > 1 {
			end -= 1
		}
		row := func(id, parent string) map[string]any {
			return map[string]any{"id": id, "parentId": parent, "rootId": "retirement-task", "isError": false, "startTime": start, "endTime": end, "progress": "private ignored native output"}
		}
		rows := []map[string]any{row("retirement-task", ""), row("child", "retirement-task")}
		if q.mode == "pending-child" || q.mode == "receipt-pending" {
			rows[1]["endTime"] = 0
		}
		if q.mode == "failed-child" || q.mode == "receipt-failed" || q.mode == "final-failure" && q.treeReads > 1 {
			rows[1]["isError"] = true
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
		return true
	}
	if r.URL.Path == sitesPath {
		q.siteReads++
		if q.siteReads == 1 && strings.HasPrefix(q.mode, "absence-") {
			q.change(q.mode)
		}
		root := Site{ID: "root", Name: "Global", NameHierarchy: "Global", Type: "global"}
		if q.mode == "parent-replaced" {
			root.ID = "replaced-root"
		}
		if q.mode == "parent-settings" {
			value := "private unrelated parent settings"
			root.Address = &value
		}
		rows := []Site{root}
		target := Site{ID: siteOwnedTestUUID, Name: "Probe", NameHierarchy: "Global/Probe", ParentID: root.ID, Type: "area"}
		switch q.mode {
		case "present":
			rows = append(rows, target)
		case "reappear":
			if q.siteReads > 1 {
				rows = append(rows, target)
			}
		case "replacement":
			target.ID = "22222222-2222-4222-8222-222222222222"
			rows = append(rows, target)
		case "reused-uuid":
			target.Name = "Elsewhere"
			target.NameHierarchy = "Global/Elsewhere"
			rows = append(rows, target)
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
		return true
	}
	return false
}

func (q *siteRetirementRecoveryFixture) verify(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, keyPath string, cm *core.ConfigMap, updateState func(func(*cvk.NetworkController))) {
	t.Helper()
	ctx := context.Background()
	updateState(func(n *cvk.NetworkController) {
		op := n.Status.Configuration.Operation
		op.Phase = "Accepted"
		op.TaskID = "retirement-task"
		if q.mode == "unknown" || q.mode == "receipt-unknown" {
			op.Phase = "Unknown"
		}
		if q.mode == "verifying" {
			op.Phase = "Verifying"
		}
		if q.mode == "taskless" {
			op.TaskID = ""
		}
		if q.mode == "borrowed-task" {
			op.TaskID = n.Status.Configuration.ActionApplications[0].Operation.TaskID
		}
		if q.mode == "legacy-selector" {
			op.Resource = strings.Replace(op.Resource, ":v2:", ":v1:", 1)
		}
		if q.mode == "edited-state-proof" || q.mode == "edited-ancestry-proof" {
			parts := strings.Split(strings.TrimPrefix(op.Resource, siteRetirementPrefix), ":")
			index := 6
			if q.mode == "edited-ancestry-proof" {
				index = 7
			}
			parts[index] = strings.Repeat("0", 64)
			op.Resource = siteRetirementPrefix + strings.Join(parts, ":")
		}
	})
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expected := nc.Status.Configuration.Operation.DeepCopy()
	if q.mode == "mismatched-task" {
		expected.TaskID = "other-task"
	}
	expectedRows := len(nc.Status.Configuration.ActionApplications)
	if q.mode == "latest-pruned" {
		updateState(func(n *cvk.NetworkController) {
			n.Status.Configuration.ActionApplications = n.Status.Configuration.ActionApplications[:1]
		})
	}
	if q.mode == "current-missing" {
		updateState(func(n *cvk.NetworkController) { n.Status.Configuration.Operation = nil })
	}
	q.submitted = expected.SubmittedAt.Time
	q.now = time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	q.change = func(mode string) {
		switch mode {
		case "task-source":
			if config.Spec.Source.ConfigMapRef != nil {
				var current core.ConfigMap
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(cm), &current); err != nil {
					t.Fatal(err)
				}
				current.Data["changed"] = "source revision"
				if err := a.statusWriter.Update(ctx, &current); err != nil {
					t.Fatal(err)
				}
			} else {
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
		case "task-owner":
			updateState(func(n *cvk.NetworkController) { n.Status.Configuration.Owners[0].ConfigUID = "foreign" })
		case "task-key":
			if err := os.WriteFile(keyPath, []byte(strings.Repeat("f", 64)), 0600); err != nil {
				t.Fatal(err)
			}
		case "absence-history":
			updateState(func(n *cvk.NetworkController) {
				n.Status.Configuration.ActionApplications[0].Operation.TaskID = "tampered-original"
			})
		case "absence-phase":
			updateState(func(n *cvk.NetworkController) { n.Status.Configuration.Operation.Phase = "Unknown" })
		}
	}
	q.active = true
	// A reconstructed actor has no in-memory pre-Delete candidate or attributes.
	restarted := *a
	freshSource, err := loadControllerSource(ctx, restarted.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(q.mode, "receipt-") {
		q.verifyReceipt(t, &restarted, config, freshSource, expected, updateState)
		return
	}
	complete, err := restarted.observeSiteRetirementTask(ctx, config, freshSource, expected, q.now)
	done := q.mode == "accepted" || q.mode == "latest" || q.mode == "unknown" || q.mode == "verifying" || q.mode == "parent-settings"
	pending := q.mode == "present" || q.mode == "reappear" || q.mode == "pending-child" || q.mode == "future-tree"
	if complete != done || (err == nil) != (done || pending) {
		t.Fatal("retirement task/absence mismatch", q.mode, complete, err)
	}
	if done && (q.treeReads != 2 || q.siteReads != 2) {
		t.Fatal("completion did not fence two independent absence observations with original task tree reads")
	}
	current, readErr := a.currentController(ctx)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if q.mode != "current-missing" && (current.Status.Configuration.Operation == nil || current.Status.Configuration.Operation.ID != expected.ID || len(current.Status.Configuration.ActionApplications) != expectedRows && q.mode != "latest-pruned") {
		t.Fatal("read-only recovery retired custody or lost original operation")
	}
	if err != nil && strings.Contains(err.Error(), "private ignored") {
		t.Fatal("native task payload leaked")
	}
}

func TestSiteRetirementAncestryIdentity(t *testing.T) {
	actual := append(sampleSites(), Site{ID: siteOwnedTestUUID, Name: "Floor", NameHierarchy: "Global/Lab/Building/Floor", ParentID: "building", Type: "floor"})
	original, err := siteRetirementAncestry(actual, "building", "floor")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"same", "settings", "parent", "ancestor", "missing", "duplicate", "wrong-kind"} {
		t.Run(mode, func(t *testing.T) {
			rows := append([]Site(nil), actual...)
			parent, kind := "building", "floor"
			switch mode {
			case "settings":
				value := "new unrelated setting"
				rows[2].Address = &value
			case "parent":
				rows[2].ID = "other"
				parent = "other"
			case "ancestor":
				rows[1].ID = "other"
				rows[2].ParentID = "other"
			case "missing":
				rows = rows[:2]
			case "duplicate":
				rows = append(rows, rows[1])
			case "wrong-kind":
				kind = "area"
			}
			got, err := siteRetirementAncestry(rows, parent, kind)
			same := err == nil && string(got) == string(original)
			if same != (mode == "same" || mode == "settings") {
				t.Fatal("ancestry identity mismatch", mode, err)
			}
		})
	}
}
