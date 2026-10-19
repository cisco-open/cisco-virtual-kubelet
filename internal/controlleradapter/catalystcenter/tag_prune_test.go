// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"net/http"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

var tagPruneModes = []string{"prune", "omitted-tags", "upgrade-baseline", "old-task", "retain", "legacy", "member-device", "member-interface", "member-template", "unknown-type", "native-drift", "unknown-field-drift", "foreign-id", "protected", "external-absence", "missing-count", "list-error", "count-list-disagree", "lost-ack", "late-consumer", "second-referenced"}

func TestTagPrunePublicLifecycle(t *testing.T) {
	for _, section := range []string{"inventory", "templates", "combined"} {
		for _, mode := range tagPruneModes {
			t.Run(section+"/"+mode, func(t *testing.T) { exerciseTagPrune(t, section, mode, nil) })
		}
	}
}
func exerciseTagPrune(t *testing.T, section, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	projected := strings.HasSuffix(mode, "-configmap")
	mode = strings.TrimSuffix(mode, "-configmap")
	finalizing := strings.HasPrefix(mode, "finalize-")
	mode = strings.TrimPrefix(mode, "finalize-")
	ctx := context.Background()
	a, c := journalFixture(t)
	c.Spec.ManagedSections = []string{section}
	if section == "combined" {
		c.Spec.ManagedSections = []string{"inventory", "templates"}
	}
	if finalizing {
		c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
		c.Finalizers = append(c.Finalizers, tagCleanupFinalizer(c), "example.com/other")
	}
	c.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	if mode == "retain" || mode == "legacy" || mode == "upgrade-baseline" || mode == "deletion-only" {
		c.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	}
	model := func(description string, empty bool) []byte {
		tags := []any{}
		if !empty {
			tags = append(tags, map[string]any{"name": "fixture", "description": description, "system_tag": false})
			if mode == "second-referenced" {
				tags = append(tags, map[string]any{"name": "other", "description": description, "system_tag": false})
			}
		}
		root := map[string]any{section: map[string]any{"tags": tags}}
		if section == "combined" {
			root = map[string]any{"inventory": map[string]any{"tags": tags}, "templates": map[string]any{}}
		}
		if empty && mode == "omitted-tags" {
			for name := range root {
				root[name] = map[string]any{}
			}
		}
		raw, _ := json.Marshal(map[string]any{"catalyst_center": root})
		return raw
	}
	c.Spec.Source.Inline.Raw = model("first", false)
	if err := a.statusWriter.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if install != nil {
		install(a, c)
	}
	if finalizing && install != nil {
		c.Finalizers = append(c.Finalizers, tagCleanupFinalizer(c), "example.com/other")
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if projected {
		cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "tag-source", Namespace: c.Namespace}, Data: map[string]string{"model": string(c.Spec.Source.Inline.Raw)}}
		if install == nil {
			cm.UID = "tag-source-uid"
		}
		if err := a.statusWriter.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
		c.Spec.Source = configapi.NetworkControllerConfigurationSource{ConfigMapRef: &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}}
		if install == nil {
			c.Generation++
		}
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	intentWriter := a.statusWriter
	setIntent := func(raw []byte) {
		if projected {
			var cm core.ConfigMap
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: c.Namespace, Name: "tag-source"}, &cm); err != nil {
				t.Fatal(err)
			}
			cm.Data["model"] = string(raw)
			if err := intentWriter.Update(ctx, &cm); err != nil {
				t.Fatal(err)
			}
		} else {
			c.Spec.Source.Inline.Raw = raw
			if install == nil {
				c.Generation++
			}
		}
		if err := intentWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	rows := map[string]map[string]any{}
	tasks := map[string]map[string]any{}
	posts, puts, deletes := 0, 0, 0
	blocking := false
	deleteRecoveries := 0
	lateInjected := false
	recreateRecoveries := 0
	firstNativeID := ""
	retirementLists := 0
	retirementResurrected := false
	originalCreateTask := ""
	list := func() []map[string]any {
		keys := []string{}
		for key := range rows {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := []map[string]any{}
		for _, key := range keys {
			out = append(out, rows[key])
		}
		return out
	}
	byID := func(id string) (string, map[string]any) {
		for name, row := range rows {
			if row["id"] == id {
				return name, row
			}
		}
		return "", nil
	}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		respond := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, taskPath) {
			task := tasks[strings.TrimPrefix(r.URL.Path, taskPath)]
			if task == nil {
				t.Error("foreign task")
				w.WriteHeader(404)
				return
			}
			respond(task)
			return
		}
		if r.URL.Path == tagsPath && r.Method == "GET" {
			if mode == "retirement-resurrection" && deletes == 1 {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Error(err)
				}
				if nc != nil && nc.Status.Configuration != nil && nc.Status.Configuration.Operation == nil {
					retirementLists++
					if retirementLists == 3 {
						rows["fixture"] = map[string]any{"id": firstNativeID, "name": "fixture", "description": "second", "systemTag": false, "instanceTenantId": "tenant"}
						retirementResurrected = true
					}
				}
			}
			respond(list())
			return
		}
		if r.URL.Path == tagsPath+"/member/type" {
			types := []string{"networkdevice", "interface"}
			if blocking && mode == "unknown-type" {
				types = append(types, "future")
			}
			respond(types)
			return
		}
		if strings.HasPrefix(r.URL.Path, tagsPath+"/") && r.Method == "GET" {
			local := strings.TrimPrefix(r.URL.Path, tagsPath+"/")
			id, _, _ := strings.Cut(local, "/")
			_, row := byID(id)
			if row == nil {
				w.WriteHeader(404)
				return
			}
			if strings.HasSuffix(local, "/member/count") {
				if blocking && mode == "missing-count" {
					respond(nil)
					return
				}
				active := blocking && (mode == "member-device" && r.URL.Query().Get("memberType") == "networkdevice" || mode == "member-interface" && r.URL.Query().Get("memberType") == "interface" || mode == "member-template" && r.URL.Query().Get("memberType") == "template" || mode == "second-referenced" && row["name"] == "other")
				if blocking && mode == "late-consumer" {
					nc, err := a.currentController(ctx)
					if err != nil {
						t.Error(err)
					}
					if nc != nil && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Submitting" {
						lateInjected = true
						active = true
					}
				}
				if active {
					respond(1)
				} else {
					respond(0)
				}
				return
			}
			if strings.HasSuffix(local, "/member") {
				if blocking && mode == "list-error" {
					w.WriteHeader(503)
					return
				}
				members := []map[string]any{}
				if blocking && mode == "count-list-disagree" {
					members = append(members, map[string]any{"instanceUuid": "consumer"})
				}
				respond(members)
				return
			}
			respond(row)
			return
		}
		if r.URL.Path == tagsPath && (r.Method == "POST" || r.Method == "PUT") {
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid write")
			}
			name, _ := body["name"].(string)
			id := ""
			if r.Method == "POST" {
				posts++
				id = fmt.Sprintf("22f33fa0-e275-41c9-a50d-%012d", posts)
			} else {
				puts++
				id, _ = body["id"].(string)
				_, old := byID(id)
				if old == nil {
					t.Error("Update lost identity")
				}
			}
			if r.Method == "POST" && posts > 1 && (mode == "recreate-reused-id" || mode == "handoff-reused-id") {
				id = firstNativeID
			}
			if firstNativeID == "" {
				firstNativeID = id
			}
			body["id"] = id
			body["instanceTenantId"] = "tenant"
			rows[name] = body
			task := fmt.Sprintf("task-%d-%d-%d", posts, puts, deletes)
			stamp := time.Now().UnixMilli()
			if posts == 1 && puts == 0 {
				originalCreateTask = task
			}
			tasks[task] = map[string]any{"id": task, "startTime": stamp, "endTime": stamp, "isError": false, "serviceType": "Grouping Service", "data": id}
			respond(map[string]any{"taskId": task})
			return
		}
		if r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, tagsPath+"/") {
			id := strings.TrimPrefix(r.URL.Path, tagsPath+"/")
			name, row := byID(id)
			if row == nil {
				t.Error("Delete lacked original UUID")
				w.WriteHeader(400)
				return
			}
			deletes++
			delete(rows, name)
			task := fmt.Sprintf("delete-%d", deletes)
			stamp := time.Now().UnixMilli()
			tasks[task] = map[string]any{"id": task, "startTime": stamp, "endTime": stamp, "isError": false, "serviceType": "Grouping Service", "data": id}
			if mode == "lost-ack" {
				respond(map[string]any{})
			} else {
				respond(map[string]any{"taskId": task})
			}
			return
		}
		t.Error("unexpected API", r.Method, r.URL.Path)
		w.WriteHeader(400)
	})
	tick := func() {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && (tagRecreateSelected(nc.Status.Configuration.Operation.Resource) || tagSuccessorSelected(nc.Status.Configuration.Operation.Resource)) && nc.Status.Configuration.Operation.Phase == "Accepted" {
			nc.Status.Configuration.Operation.Phase = "Unknown"
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			recreateRecoveries++
		}
		if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && tagDeleteSelected(nc.Status.Configuration.Operation.Resource) && nc.Status.Configuration.Operation.Phase == "Accepted" {
			nc.Status.Configuration.Operation.Phase = "Unknown"
			if mode == "old-task" {
				nc.Status.Configuration.Operation.TaskID = originalCreateTask
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			deleteRecoveries++
		}
	}
	converge := func() {
		for range 24 {
			tick()
			if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && c.Status.ObservedGeneration == c.Generation {
				return
			}
		}
		t.Fatal("tag revision did not converge", c.Status.Conditions)
	}
	converge()
	expectedPosts := 1
	if mode == "second-referenced" {
		expectedPosts = 2
	}
	if posts != expectedPosts {
		t.Fatal("Create count", posts)
	}
	// A real Update supplies independent latest-state ownership before cleanup.
	if mode == "upgrade-baseline" {
		c.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	}
	setIntent(model("second", false))
	converge()
	records, err := tagOwnedRecords(journalState(t, a))
	if err != nil {
		t.Fatal(err)
	}
	creatorBefore := records.created
	if mode != "legacy" && mode != "retain" {
		for _, latest := range records.applied {
			if _, err := tagFullReceiptState(latest); err != nil {
				t.Fatal("new full-state baseline missing", err)
			}
		}
	}
	blocking = true
	switch mode {
	case "native-drift":
		rows["fixture"]["description"] = "external"
	case "unknown-field-drift":
		rows["fixture"]["externalField"] = "unmanaged edit"
	case "foreign-id":
		rows["fixture"]["id"] = "11f33fa0-e275-41c9-a50d-d7e5135adb8c"
	case "protected":
		rows["fixture"]["systemTag"] = true
	case "external-absence":
		delete(rows, "fixture")
	}
	if mode == "legacy" {
		c.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	}
	if finalizing {
		admin := a.statusWriter
		if mode == "retain" {
			c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
			if install == nil {
				c.Generation++
			}
			if err := admin.Update(ctx, c); err != nil {
				t.Fatal(err)
			}
		}
		if mode == "source-gone" {
			if err := admin.Delete(ctx, &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: "tag-source"}}); err != nil {
				t.Fatal(err)
			}
		}
		if strings.HasPrefix(mode, "inflight-") {
			setIntent(model("third", false))
			tick()
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.Phase != "Accepted" {
				t.Fatal("inflight task not accepted", c.Status.Conditions)
			}
			nc.Status.Configuration.Operation.Phase = "Unknown"
			if mode == "inflight-taskless" {
				nc.Status.Configuration.Operation.TaskID = ""
			}
			if err := admin.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
		}
		if err := admin.Delete(ctx, c); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
		if c.DeletionTimestamp.IsZero() {
			t.Fatal("config deletion was not retained")
		}
		a.statusWriter = statusOnlyFeatureClient{Client: admin}
		var retirementWriter *tagRetirementStatusClient
		if mode == "retirement-refused" || mode == "retirement-lost-ack" {
			retirementWriter = &tagRetirementStatusClient{Client: a.statusWriter, reject: mode == "retirement-refused"}
			a.statusWriter = retirementWriter
		}
		blockedProofObserved := false
		for range 24 {
			tick()
			if retirementWriter != nil && retirementWriter.lost && !blockedProofObserved {
				current, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if tagDeletionCompleted(current, c) {
					t.Fatal("lost retirement acknowledgement prematurely released config proof")
				}
				blockedProofObserved = true
			}
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		complete := tagDeletionCompleted(nc, c)
		expected := mode == "prune" || mode == "deletion-only" || mode == "source-gone" || mode == "inflight-update" || mode == "retain" || mode == "retirement-lost-ack" || mode == "retirement-policy-retain" || strings.HasPrefix(mode, "handoff")
		if complete != expected {
			t.Fatal("tag CR cleanup proof mismatch", mode, c.Status.Conditions)
		}
		if expected && mode != "retain" {
			if deletes != 1 || deleteRecoveries != 1 || len(rows) != 0 || nc.Status.Configuration.Operation != nil {
				t.Fatal("tag CR deletion did not recover once", deletes, deleteRecoveries, nc.Status.Configuration.Operation)
			}
			if !tagConfigKnownWorkClosed(nc, c) {
				t.Fatal("missing cleanup receipts")
			}
			record, _, err := tagRetirementRecord(nc)
			if err != nil || record.Completion.ConfigUID != string(c.UID) || !reflect.DeepEqual(record.Completion, featureConfigCompletionProof(nc, c)) {
				t.Fatal("missing durable retirement before config release", err)
			}
			without := nc.DeepCopy()
			without.Status.Conditions = slices.DeleteFunc(without.Status.Conditions, func(v metav1.Condition) bool { return v.Type == tagRetirementCondition })
			if tagDeletionCompleted(without, c) {
				t.Fatal("manager accepted cleanup without persisted retirement authority")
			}
			if mode == "retirement-lost-ack" && (retirementWriter.attempted != 1 || !blockedProofObserved) {
				t.Fatal("retirement acknowledgement recovery repeated writes or missed proof fence", retirementWriter.attempted)
			}

			for _, row := range nc.Status.Configuration.ActionApplications {
				if tagDeleteSelected(row.Operation.Resource) && !tagFinalizeSelected(row.Operation.Resource) {
					t.Fatal("ordinary prune fabricated finalization")
				}
			}
		} else if mode == "retain" {
			if deletes != 0 || len(rows) != 1 {
				t.Fatal("Retain policy deleted native tag")
			}
		} else if mode == "retirement-refused" || mode == "retirement-resurrection" {
			if deletes != 1 || nc.Status.Configuration.Operation != nil {
				t.Fatal("retirement failure replayed native deletion")
			}
			if _, _, err := tagRetirementRecord(nc); err == nil {
				t.Fatal("blocked retirement minted authority")
			}
			if mode == "retirement-refused" && retirementWriter.attempted == 0 {
				t.Fatal("retirement writer refusal not exercised")
			}
			if mode == "retirement-resurrection" && !retirementResurrected {
				t.Fatal("late native resurrection not exercised")
			}
		} else if mode == "lost-ack" || mode == "old-task" {
			if deletes != 1 || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.Phase != "Unknown" {
				t.Fatal("uncertain CR cleanup replayed or released")
			}
		} else if deletes != 0 {
			t.Fatal("blocked CR cleanup dispatched", mode, deletes)
		}
		if mode == "late-consumer" && nc.Status.Configuration.Operation != nil {
			t.Fatal("late preflight refusal retained an unsubmitted journal")
		}
		after, err := tagOwnedRecords(nc.Status.Configuration)
		if err != nil || !reflect.DeepEqual(creatorBefore, after.created) {
			t.Fatal("CR deletion changed creator history", err)
		}
		if !slices.Contains(c.Finalizers, tagCleanupFinalizer(c)) || !slices.Contains(c.Finalizers, "example.com/other") {
			t.Fatal("worker changed metadata")
		}
		if complete {
			var lifecycle *controlleradapter.ConfigurationLifecycle
			for _, candidate := range controlleradapter.ConfigurationLifecyclesFor(TypeName) {
				if candidate.Finalizer == tagCleanupFinalizer(c) {
					copy := candidate
					lifecycle = &copy
				}
			}
			if lifecycle == nil || !lifecycle.Completed(nc, c) {
				t.Fatal("manager callback did not accept worker proof")
			}
			for _, change := range []string{"uid", "controller-generation", "paused", "spec", "proof", "history"} {
				n, conf := nc.DeepCopy(), c.DeepCopy()
				switch change {
				case "uid":
					conf.UID = "different"
				case "controller-generation":
					n.Generation++
				case "paused":
					n.Spec.Paused = true
				case "spec":
					conf.Spec.TaskTimeout = &metav1.Duration{Duration: time.Minute}
				case "proof":
					for i := range conf.Status.Conditions {
						if conf.Status.Conditions[i].Type == featureConfigCleanupCondition {
							conf.Status.Conditions[i].Message = "{}"
						}
					}
				case "history":
					n.Status.Configuration.ActionApplications = nil
				}
				if lifecycle.Completed(n, conf) {
					t.Fatal("unbound manager cleanup proof accepted", change)
				}
			}
			altered := c.DeepCopy()
			altered.Generation++
			if lifecycle.Completed(nc, altered) {
				t.Fatal("stale generation authorized manager release")
			}
		}
		if mode == "retirement-policy-retain" {
			c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
			if install == nil {
				c.Generation++
			}
			if err := admin.Update(ctx, c); err != nil {
				t.Fatal(err)
			}
			tick()
			current, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !tagDeletionCompleted(current, c) {
				t.Fatal("Retain policy could not invalidate previous retirement", c.Status.Conditions)
			}
			if _, _, err := tagRetirementRecord(current); err == nil {
				t.Fatal("Retain policy kept Delete retirement authority")
			}
			if deletes != 1 {
				t.Fatal("Retain policy repeated native mutation")
			}
		}

		if strings.HasPrefix(mode, "handoff") {
			cycles := 1
			if mode == "handoff-repeated" {
				cycles = 3
			}
			for cycle := 0; cycle < cycles; cycle++ {
				previous := c.DeepCopy()
				previousNC, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				oldRows := append([]cvk.NetworkControllerConfigurationActionApplication(nil), previousNC.Status.Configuration.ActionApplications...)
				if mode != "handoff-old-present" {
					before := c.DeepCopy()
					c.Finalizers = nil
					if err := admin.Patch(ctx, c, ctrlclient.MergeFromWithOptions(before, ctrlclient.MergeFromWithOptimisticLock{})); err != nil {
						t.Fatal(err)
					}
					var absent configapi.NetworkControllerConfig
					if err := admin.Get(ctx, ctrlclient.ObjectKeyFromObject(previous), &absent); !apierrors.IsNotFound(err) {
						t.Fatal("old config still exists", err)
					}
				}
				if mode == "handoff-missing-retirement" {
					current, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					current.Status.Conditions = slices.DeleteFunc(current.Status.Conditions, func(v metav1.Condition) bool { return v.Type == tagRetirementCondition })
					if err := admin.Status().Update(ctx, current); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "handoff-resurrection" {
					rows["resurrected"] = map[string]any{"id": firstNativeID, "name": "resurrected", "systemTag": false}
				}
				name := previous.Name
				if mode != "handoff-same-name" {
					name = fmt.Sprintf("tag-owner-%d", cycle)
				}
				next := previous.DeepCopy()
				if strings.HasPrefix(mode, "handoff-to-") {
					section = strings.TrimPrefix(mode, "handoff-to-")
					next.Spec.ManagedSections = []string{section}
					if section == "combined" {
						next.Spec.ManagedSections = []string{"inventory", "templates"}
					}
				}
				next.ObjectMeta = metav1.ObjectMeta{Namespace: previous.Namespace, Name: name, Finalizers: []string{tagCleanupFinalizer(next), "example.com/other"}}
				next.Status = configapi.NetworkControllerConfigStatus{}
				if install == nil {
					next.UID = types.UID(fmt.Sprintf("owner-next-%d", cycle))
					next.Generation = 1
				}
				if projected {
					var cm core.ConfigMap
					if err := admin.Get(ctx, ctrlclient.ObjectKey{Namespace: previous.Namespace, Name: "tag-source"}, &cm); err != nil {
						t.Fatal(err)
					}
					cm.Data["model"] = string(model(fmt.Sprintf("owner-%d", cycle), false))
					if err := admin.Update(ctx, &cm); err != nil {
						t.Fatal(err)
					}
				} else {
					next.Spec.Source = configapi.NetworkControllerConfigurationSource{Inline: &runtime.RawExtension{Raw: model(fmt.Sprintf("owner-%d", cycle), false)}}
				}
				if err := admin.Create(ctx, next); err != nil {
					t.Fatal(err)
				}
				c = next
				transferred := false
				corrupted := false
				for range 30 {
					tick()
					current, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					own := true
					for _, o := range current.Status.Configuration.Owners {
						if configurationSectionsOverlap(o.Section, c.Spec.ManagedSections[0]) && o.ConfigUID != string(c.UID) {
							own = false
						}
					}
					if own && !transferred {
						transferred = true
						if len(current.Status.Configuration.Owners) != len(c.Spec.ManagedSections) || configurationOwnersMatch(current.Status.Configuration, c) != nil {
							t.Fatal("handoff retained stale or omitted new section claims")
						}
						if posts != cycle+1 || puts != cycle+1 || deletes != cycle+1 {
							t.Fatal("handoff sent a native request instead of returning to the shared journal")
						}
						graph, err := tagOwnerArchiveGraph(current.Status.Configuration)
						if err != nil || graph.root == nil {
							t.Fatal("handoff did not preserve sealed history", err)
						}
						for _, old := range oldRows {
							found := false
							for _, now := range current.Status.Configuration.ActionApplications {
								if old.Operation.ID == now.Operation.ID && reflect.DeepEqual(old.Operation, now.Operation) && reflect.DeepEqual(old.VerifiedAt, now.VerifiedAt) {
									found = true
								}
							}
							if !found {
								t.Fatal("handoff dropped or rewrote an original operation", old.Operation.ID)
							}
						}
						if mode == "handoff-missing-carrier" || mode == "handoff-missing-history" || mode == "handoff-corrupt-history" {
							altered := current.DeepCopy()
							switch mode {
							case "handoff-missing-carrier":
								altered.Status.Configuration.ActionApplications = slices.DeleteFunc(altered.Status.Configuration.ActionApplications, func(r cvk.NetworkControllerConfigurationActionApplication) bool {
									return strings.HasPrefix(r.Resource, tagOwnerRootPrefix)
								})
							case "handoff-missing-history":
								altered.Status.Configuration.ActionApplications = nil
							case "handoff-corrupt-history":
								altered.Status.Configuration.ActionApplications[0].Resource += "x"
							}
							if err := admin.Status().Update(ctx, altered); err != nil {
								t.Fatal(err)
							}
							corrupted = true
						}
					}
					if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
						break
					}
				}
				blocked := mode == "handoff-old-present" || mode == "handoff-missing-retirement" || mode == "handoff-resurrection" || corrupted || mode == "handoff-reused-id"
				if blocked {
					if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
						t.Fatal("blocked handoff or successor claimed convergence")
					}
					expected := 1
					if mode == "handoff-reused-id" {
						expected = 2
					}
					if posts != expected || deletes != 1 {
						t.Fatal("blocked handoff/successor dispatched or replayed", posts, deletes)
					}
					if mode == "handoff-reused-id" && journalState(t, a).Operation == nil {
						t.Fatal("reused UUID released its journal")
					}
					return
				}
				if !transferred || c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || posts != cycle+2 {
					t.Fatal("successor did not converge", c.Status.Conditions)
				}
				if rows["fixture"]["id"] == firstNativeID {
					t.Fatal("successor reused old UUID")
				}
				current, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tagRetiredCohorts(current.Status.Configuration); err != nil {
					t.Fatal("successor detached archive graph", err)
				}
				setIntent(model(fmt.Sprintf("owner-updated-%d", cycle), false))
				converge()
				if err := admin.Delete(ctx, c); err != nil {
					t.Fatal(err)
				}
				if err := admin.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
					t.Fatal(err)
				}
				for range 24 {
					tick()
				}
				current, err = a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if !tagDeletionCompleted(current, c) || deletes != cycle+2 || len(rows) != 0 {
					t.Fatal("successor could not close its own epoch", c.Status.Conditions)
				}
				if _, _, err := tagRetirementRecord(current); err != nil {
					t.Fatal("successor retirement lost prior history", err)
				}
			}
			return
		}

		return
	}
	setIntent(model("", true))
	for range 15 {
		tick()
	}
	state := journalState(t, a)
	deleted, err := tagDeletedRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	checkMode := mode
	if strings.HasPrefix(mode, "recreate") {
		checkMode = "prune"
	}
	switch checkMode {
	case "prune", "omitted-tags", "upgrade-baseline":
		if deletes != 1 || deleteRecoveries != 1 || len(rows) != 0 || len(deleted) != 1 || c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("owned manifest cleanup did not settle once", deletes, deleteRecoveries, c.Status.Conditions)
		}
		after, err := tagOwnedRecords(state)
		if err != nil || !reflect.DeepEqual(creatorBefore, after.created) {
			t.Fatal("cleanup rewrote creator", err)
		}
	case "lost-ack", "old-task":
		if deletes != 1 || state.Operation == nil || state.Operation.Phase != "Unknown" || len(deleted) != 0 {
			t.Fatal("ambiguous Delete replayed or fabricated a tombstone", deletes, state.Operation)
		}
	case "retain":
		if deletes != 0 || len(rows) != 1 || len(deleted) != 0 || c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("Retain performed cleanup")
		}
	default:
		if deletes != 0 || len(deleted) != 0 || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("blocked cleanup mutated or claimed convergence", mode, deletes, c.Status.Conditions)
		}
	}
	if mode == "late-consumer" && state.Operation != nil {
		t.Fatal("proven pre-submit refusal did not retire its unsubmitted journal", state.Operation.Phase)
	}
	if mode == "late-consumer" && !lateInjected {
		t.Fatal("pre-submit consumer race not exercised")
	}
	if strings.HasPrefix(mode, "recreate") {
		if mode == "recreate-foreign" {
			rows["fixture"] = map[string]any{"id": "99f33fa0-e275-41c9-a50d-d7e5135adb8c", "name": "fixture", "description": "cycle", "systemTag": false}
		}
		if mode == "recreate-resurrected" {
			rows["resurrected"] = map[string]any{"id": firstNativeID, "name": "resurrected", "systemTag": false}
		}
		if mode == "recreate-missing-cohort" {
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration.ActionApplications = nc.Status.Configuration.ActionApplications[1:]
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
		}
		cycles := 1
		if mode == "recreate-twice" {
			cycles = 2
		}
		for cycle := 0; cycle < cycles; cycle++ {
			setIntent(model(fmt.Sprintf("cycle-%d", cycle), false))
			for range 24 {
				tick()
				if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && c.Status.ObservedGeneration == c.Generation {
					break
				}
			}
			if mode == "recreate-foreign" || mode == "recreate-resurrected" || mode == "recreate-missing-cohort" {
				if posts != 1 || deletes != 1 || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("recreation adopted foreign/incomplete state", posts, c.Status.Conditions)
				}
				return
			}
			if mode == "recreate-reused-id" {
				if posts != 2 || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || journalState(t, a).Operation == nil {
					t.Fatal("recreation accepted a prior native UUID")
				}
				return
			}
			if c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("recreation did not converge", c.Status.Conditions)
			}
			current := journalState(t, a)
			cohorts, err := tagRetiredCohorts(current)
			if err != nil || len(cohorts) != cycle+1 {
				t.Fatal("complete recreation ancestry missing", err, len(cohorts))
			}
			if posts != cycle+2 || recreateRecoveries != cycle+1 {
				t.Fatal("recreate task replayed or not recovered", posts, recreateRecoveries)
			}
			if mode == "recreate-missing-archive" || mode == "recreate-tampered-archive" {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for i, r := range nc.Status.Configuration.ActionApplications {
					if tagRetiredSelected(r.Resource) {
						if mode == "recreate-missing-archive" {
							nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications[:i], nc.Status.Configuration.ActionApplications[i+1:]...)
						} else {
							nc.Status.Configuration.ActionApplications[i].Operation.ConfigGeneration++
						}
						break
					}
				}
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				for range 3 {
					tick()
				}
				if posts != 2 || deletes != 1 || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("recreated owner accepted damaged ancestry")
				}
				return
			}
			setIntent(model(fmt.Sprintf("updated-cycle-%d", cycle), false))
			converge()
			setIntent(model("", true))
			converge()
			if deletes != cycle+2 || len(rows) != 0 {
				t.Fatal("recreated lifecycle did not clean up once", deletes, len(rows))
			}
		}
	}

}
