// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controller"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var credentialRevisionModes = []string{"rotate-inline", "rotate-configmap", "leaf-reintroduction", "leaf-reuse-refused", "prune-recreate", "metadata", "prune-inline", "prune-configmap", "retain", "old-counter", "projection-lag", "archive-return", "seal-loss", "lost-accepted-ack", "lost-retirement-ack", "lost-permit-ack", "ambiguous-delete", "unknown-delete", "configmap-source-loss", "scope-loss-after-submit", "empty-inline", "empty-configmap", "empty-retain", "empty-recreate", "empty-lost-accepted-ack", "empty-lost-retirement-ack", "empty-lost-permit-ack", "empty-ambiguous-delete", "empty-unknown-delete", "empty-configmap-source-loss", "empty-scope-loss-after-submit", "empty-seal-loss", "empty-archive-return", "empty-native-return", "metadata-noauth-configmap", "metadata-global-inline", "metadata-global-configmap", "metadata-noauth", "metadata-reintroduce", "metadata-version-reuse", "metadata-prune", "metadata-prune-recreate", "metadata-mixed", "metadata-lost-accepted-ack", "metadata-lost-retirement-ack", "metadata-lost-permit-ack", "metadata-ambiguous-delete", "metadata-unknown-delete", "metadata-configmap-source-loss", "metadata-scope-loss-after-submit", "metadata-lost-metadata-accepted-ack", "metadata-lost-metadata-permit-ack", "metadata-lost-metadata-receipt-ack", "metadata-rejected-metadata-receipt", "metadata-ambiguous-create", "metadata-configmap-create-source-loss", "metadata-unknown-create"}

func TestDiscoveryCredentialCustodyRevision(t *testing.T) {
	for _, mode := range credentialRevisionModes {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialCleanupObservationScenario(t, "handoff-observe/custody-public-revision-"+mode, nil)
		})
	}
}

type credentialRevisionJob struct {
	name    string
	version int64
}
type credentialRevisionTask struct {
	id      string
	started int64
}

func exerciseDiscoveryCredentialRevision(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, mode string, cm *core.ConfigMap, admin ctrlclient.Client, realAPI bool, nativeID string) {
	t.Helper()
	ctx := context.Background()
	metadataLifecycle := strings.HasPrefix(mode, "metadata-")
	faultMode := strings.TrimPrefix(strings.TrimPrefix(mode, "empty-"), "metadata-")
	key := ctrlclient.ObjectKeyFromObject(c)
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	initial := nc.Status.Configuration.DeepCopy()
	rootMessage := meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition).Message
	rootGeneration := c.Generation
	if len(initial.ActionApplications) != 3 || len(initial.CredentialApplications) != 2 {
		t.Fatal("missing public successor baseline")
	}
	ref := c.Spec.SecretRefs[0]
	var secretSource cvk.NetworkControllerIntentSecretSource
	for _, source := range nc.Spec.IntentSecretSources {
		if source.Alias == ref.Source {
			secretSource = source
		}
	}
	enableVersion := int64(0)
	setModel := func(wants []credentialRevisionJob, prune string) {
		t.Helper()
		if err := admin.Get(ctx, key, c); err != nil {
			t.Fatal(err)
		}
		oldSpec := c.Spec.DeepCopy()
		raw := discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 2}, false)
		var model map[string]any
		if json.Unmarshal(raw, &model) != nil {
			t.Fatal("model decode")
		}
		inventory := model["catalyst_center"].(map[string]any)["inventory"].(map[string]any)
		prototype := inventory["discovery"].([]any)[0].(map[string]any)
		rows := []any{}
		c.Spec.SecretRefs = nil
		for i, want := range wants {
			job := map[string]any{}
			for k, v := range prototype {
				job[k] = v
			}
			job["name"], job["password_list_version"] = want.name, want.version
			if want.version == 0 {
				delete(job, "password_list_version")
				delete(job, "user_name_list")
				if strings.HasPrefix(mode, "metadata-noauth") {
					job["snmp_version"], job["snmp_mode"], job["snmp_user_name"] = "v3", "NoAuthNoPriv", "operator"
				} else {
					job["global_credential_list"] = []any{"lab-cli"}
				}
			}
			if mode == "metadata" {
				job["user_name_list"] = []any{"updated-operator"}
			}
			if enableVersion > 0 {
				job["enable_password_list_version"] = enableVersion
			}
			rows = append(rows, job)
			if want.version == 0 {
				continue
			} // No version-0 declaration or projected bytes.
			next := ref
			next.Path = fmt.Sprintf("/discovery/%d/password_list", i)
			c.Spec.SecretRefs = append(c.Spec.SecretRefs, next)
			relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: c.Name, Section: next.Section, JSONPointer: next.Path, SourceAlias: next.Source, SecretName: secretSource.Name, SecretKey: secretSource.Key})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(a.intentSecretPath, relative)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			version := want.version
			if mode == "projection-lag" {
				version--
			}
			value := discoveryCredentialMarker
			if want.version != 2 {
				value = fmt.Sprintf("SYNTHETIC_V%d", want.version)
			}
			body, _ := json.Marshal([]string{value})
			if err := os.WriteFile(path, versionedSecretFixture(t, version, string(body)), 0600); err != nil {
				t.Fatal(err)
			}
			if enableVersion > 0 {
				extra := ref
				extra.Path = fmt.Sprintf("/discovery/%d/enable_password_list", i)
				c.Spec.SecretRefs = append(c.Spec.SecretRefs, extra)
				relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: c.Name, Section: extra.Section, JSONPointer: extra.Path, SourceAlias: extra.Source, SecretName: secretSource.Name, SecretKey: secretSource.Key})
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(a.intentSecretPath, relative)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				value, _ := json.Marshal([]string{fmt.Sprintf("ENABLE_V%d", enableVersion)})
				if err := os.WriteFile(path, versionedSecretFixture(t, enableVersion, string(value)), 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
		inventory["discovery"] = rows
		raw, _ = json.Marshal(model)
		if cm == nil {
			c.Spec.Source.Inline.Raw = raw
		} else {
			cm.Data["model"] = string(raw)
			if err := admin.Update(ctx, cm); err != nil {
				t.Fatal(err)
			}
		}
		if prune != "" {
			c.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicy(prune)
		}
		if !realAPI && !reflect.DeepEqual(*oldSpec, c.Spec) {
			c.Generation++
		}
		if err := admin.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	jobs := map[string]discoveryMetadata{"fixture": {ID: nativeID, Name: "fixture", Type: "SINGLE", Addresses: "192.0.2.1", Protocol: "SSH", Usernames: []string{"operator"}, Condition: "Complete", Status: "Inactive"}}
	tasks := map[string]credentialRevisionTask{}
	posts, deletes, otherWrites := 0, 0, 0
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"fixture"}`)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == globalCredentialsPath:
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"cliCredential": []map[string]any{{"id": "global-id", "description": "lab-cli"}}}})
		case r.Method == "GET" && r.URL.Path == discoveryPath+"/1/100":
			rows := []discoveryMetadata{}
			for _, job := range jobs {
				rows = append(rows, job)
			}
			if faultMode == "native-return" && deletes > 0 {
				rows = append(rows, discoveryMetadata{ID: nativeID, Name: "fixture", Type: "SINGLE", Addresses: "192.0.2.1", Protocol: "SSH", Usernames: []string{"operator"}, Condition: "Complete", Status: "Inactive"})
			}
			if faultMode == "archive-return" {
				rows = append(rows, discoveryMetadata{ID: "job-id", Name: "returned-archive", Type: "SINGLE"})
			}
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, discoveryPath+"/"):
			id := strings.TrimPrefix(r.URL.Path, discoveryPath+"/")
			found := ""
			for name, job := range jobs {
				if job.ID == id {
					found = name
				}
			}
			if found == "" {
				t.Error("delete did not target exact current UUID", id)
				w.WriteHeader(404)
				return
			}
			delete(jobs, found)
			deletes++
			task := fmt.Sprintf("revision-delete-%d", deletes)
			tasks[task] = credentialRevisionTask{started: time.Now().UnixMilli()}
			if faultMode == "configmap-source-loss" {
				if err := admin.Delete(ctx, cm); err != nil {
					t.Error(err)
				}
			}
			if faultMode == "ambiguous-delete" {
				fmt.Fprint(w, `{"response":{}}`)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": task}})
			}
		case r.Method == "POST" && r.URL.Path == discoveryPath:
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid create payload")
			}
			name, _ := body["name"].(string)
			if name == "" || jobs[name].ID != "" {
				t.Error("creation adopted or duplicated native job", name)
			}
			posts++
			id := fmt.Sprintf("revision-job-%d", posts)
			// Decode only the existing allowlisted public readback type.
			// Password/community payload properties are never retained in this map.
			raw, err := json.Marshal(body)
			var job discoveryMetadata
			if err != nil || json.Unmarshal(raw, &job) != nil {
				t.Error("invalid public discovery payload")
			}
			job.ID, job.Condition, job.Status = id, "Complete", "Inactive"
			jobs[name] = job
			task := fmt.Sprintf("revision-create-%d", posts)
			tasks[task] = credentialRevisionTask{id: id, started: time.Now().UnixMilli()}
			if faultMode == "configmap-create-source-loss" {
				if err := admin.Delete(ctx, cm); err != nil {
					t.Error(err)
				}
			}
			if faultMode == "ambiguous-create" {
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{}})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": task}})
			}
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, taskPath):
			task := strings.TrimPrefix(r.URL.Path, taskPath)
			data, ok := tasks[task]
			if !ok {
				t.Error("recovered a foreign task", task)
				w.WriteHeader(404)
				return
			}
			response := map[string]any{"id": task, "startTime": data.started, "endTime": data.started, "isError": false}
			if data.id != "" {
				response["data"] = fmt.Sprintf(`{"discoveryId":%q}`, data.id)
			}
			json.NewEncoder(w).Encode(map[string]any{"response": response})
		default:
			otherWrites++
			t.Error("unexpected revision request", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	worker := statusOnlyFeatureClient{Client: admin}
	a.statusWriter = worker
	defer func() { a.statusWriter = admin }()
	converge := func(limit int) bool {
		t.Helper()
		for i := 0; i < limit; i++ {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := admin.Get(ctx, key, c); err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			if state.Operation != nil && state.Operation.Phase == "Accepted" && (faultMode == "unknown-delete" && state.Operation.Action == "Delete" || faultMode == "unknown-create" && state.Operation.Action == "Create" && strings.HasPrefix(state.Operation.Resource, discoveryCredentialMetadataScopePrefix)) {
				if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
			}
			if faultMode == "scope-loss-after-submit" && deletes > 0 {
				meta.RemoveStatusCondition(&c.Status.Conditions, discoveryCredentialSourceCondition)
				if err := admin.Status().Update(ctx, c); err != nil {
					t.Fatal(err)
				}
			}
			if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && state.Operation == nil {
				return true
			}
		}
		return false
	}
	pruning := mode == "prune-recreate" || mode == "prune-inline" || mode == "prune-configmap" || mode == "retain"
	prefixDeletes, prefixPosts := 0, 0
	empty := strings.HasPrefix(mode, "empty-")
	if metadataLifecycle {
		setModel([]credentialRevisionJob{{"fixture", 0}}, "Delete")
		if mode == "metadata-mixed" {
			setModel([]credentialRevisionJob{{"fixture", 0}, {"another", 1}}, "Delete")
		}
		switch mode {
		case "metadata-reintroduce", "metadata-version-reuse", "metadata-prune", "metadata-prune-recreate":
			if !converge(40) || posts != 1 || deletes != 1 {
				t.Fatal("metadata transition did not settle", c.Status.Conditions, journalState(t, a).Operation)
			}
			state := journalState(t, a)
			if len(state.CredentialApplications) != len(initial.CredentialApplications) || !strings.HasPrefix(state.ActionApplications[len(state.ActionApplications)-1].Operation.Resource, discoveryCredentialMetadataScopePrefix) || len(state.ActionApplications[len(state.ActionApplications)-1].Operation.CredentialVersions) != 0 {
				t.Fatal("metadata creation fabricated credential delivery")
			}
			prefixPosts, prefixDeletes = posts, deletes
			switch mode {
			case "metadata-reintroduce":
				setModel([]credentialRevisionJob{{"fixture", 3}}, "Delete")
			case "metadata-version-reuse":
				setModel([]credentialRevisionJob{{"fixture", 2}}, "Delete")
			default:
				setModel(nil, "Delete")
				if mode == "metadata-prune-recreate" {
					if !converge(35) || posts != 1 || deletes != 2 || len(jobs) != 0 {
						t.Fatal("metadata prune failed", c.Status.Conditions)
					}
					setModel([]credentialRevisionJob{{"fixture", 0}}, "Delete")
				}
			}
		}
	} else if empty {
		policy := "Delete"
		if mode == "empty-retain" {
			policy = "Retain"
		}
		setModel(nil, policy)
		if mode == "empty-recreate" {
			if !converge(35) || len(jobs) != 0 || posts != 0 || deletes != 1 {
				t.Fatal("empty source did not retire the last owned job", c.Status.Conditions)
			}
			setModel([]credentialRevisionJob{{"fixture", 3}}, "Delete")
		}
	} else if strings.HasPrefix(mode, "leaf-") {
		enableVersion = 1
		setModel([]credentialRevisionJob{{"fixture", 2}}, "")
		if !converge(35) {
			t.Fatal("extra credential leaf could not be delivered", c.Status.Conditions)
		}
		enableVersion = 0
		setModel([]credentialRevisionJob{{"fixture", 2}}, "")
		if !converge(35) {
			t.Fatal("credential leaf removal failed", c.Status.Conditions)
		}
		if posts != 2 || deletes != 2 {
			t.Fatal("leaf removal lost its creator/retirement sequence", posts, deletes)
		}
		prefixDeletes = deletes
		enableVersion = 2
		if mode == "leaf-reuse-refused" {
			enableVersion = 1
		}
		setModel([]credentialRevisionJob{{"fixture", 2}}, "")
	} else if pruning {
		setModel([]credentialRevisionJob{{"fixture", 2}, {"another", 1}}, "Retain")
		if !converge(35) {
			t.Fatal("additional credential job did not converge", c.Status.Conditions, journalState(t, a).Operation)
		}
		if posts != 1 || deletes != 0 {
			t.Fatal("addition rewrote existing owned job")
		}
		policy := "Delete"
		if mode == "retain" {
			policy = "Retain"
		}
		setModel([]credentialRevisionJob{{"another", 1}}, policy)
		if mode == "prune-recreate" {
			if !converge(35) {
				t.Fatal("source prune did not settle", c.Status.Conditions)
			}
			setModel([]credentialRevisionJob{{"fixture", 3}, {"another", 1}}, "Delete")
		}

	} else {
		version := int64(3)
		if mode == "old-counter" {
			version = 1
		}
		if mode == "metadata" {
			version = 2
		}
		setModel([]credentialRevisionJob{{"fixture", version}}, "")
	}
	if faultMode == "seal-loss" {
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cond := meta.FindStatusCondition(current.Status.Conditions, discoveryRetirementCondition)
		var record discoveryOwnershipRetirement
		json.Unmarshal([]byte(cond.Message), &record)
		record.CredentialHistoryDigest = ""
		raw, _ := json.Marshal(record)
		cond.Message = string(raw)
		if err := admin.Status().Update(ctx, current); err != nil {
			t.Fatal(err)
		}
	}
	hits := 0
	if strings.HasPrefix(faultMode, "lost-") || faultMode == "rejected-metadata-receipt" {
		a.statusWriter = credentialCustodyCleanupFaultClient{Client: worker, mode: faultMode, uid: string(c.UID), hits: &hits}
	}
	complete := converge(40)
	metadataSuccess := metadataLifecycle && (faultMode == "global-inline" || faultMode == "global-configmap" || faultMode == "noauth" || faultMode == "noauth-configmap" || faultMode == "reintroduce" || faultMode == "prune" || faultMode == "prune-recreate" || faultMode == "mixed" || faultMode == "unknown-create" || faultMode == "lost-metadata-accepted-ack" || faultMode == "lost-metadata-receipt-ack" || faultMode == "rejected-metadata-receipt")
	success := metadataSuccess || empty && (faultMode == "inline" || faultMode == "configmap" || faultMode == "retain" || faultMode == "recreate") || mode == "leaf-reintroduction" || mode == "rotate-inline" || mode == "rotate-configmap" || mode == "metadata" || pruning || faultMode == "unknown-delete" || faultMode == "lost-accepted-ack" || faultMode == "lost-retirement-ack"
	if complete != success || otherWrites != 0 {
		t.Fatal("revision lost its authority or failed", mode, posts, deletes, c.Status.Conditions, journalState(t, a).Operation)
	}
	expectedPosts, expectedDeletes := 1, 1
	if metadataLifecycle {
		switch mode {
		case "metadata-reintroduce", "metadata-prune-recreate":
			expectedPosts, expectedDeletes = 2, 2
		case "metadata-prune":
			expectedPosts, expectedDeletes = 1, 2
		case "metadata-mixed":
			expectedPosts, expectedDeletes = 2, 1
		case "metadata-version-reuse":
			expectedPosts, expectedDeletes = 1, 1
		}
	}
	if empty && mode != "empty-recreate" {
		expectedPosts = 0
	}
	if mode == "empty-retain" {
		expectedDeletes = 0
	}
	if mode == "leaf-reintroduction" {
		expectedPosts, expectedDeletes = 3, 3
	}
	if mode == "prune-recreate" {
		expectedPosts, expectedDeletes = 2, 1
	}
	if mode == "retain" {
		expectedDeletes = 0
	}
	if !success {
		expectedPosts = prefixPosts
		if metadataLifecycle && prefixPosts > 0 {
			expectedDeletes = prefixDeletes
		}
		switch faultMode {
		case "old-counter", "projection-lag", "archive-return", "seal-loss", "lost-permit-ack":
			expectedDeletes = 0
		}
	}
	if metadataLifecycle && (faultMode == "ambiguous-create" || faultMode == "configmap-create-source-loss") {
		expectedPosts, expectedDeletes = 1, 1
	}
	if mode == "leaf-reuse-refused" {
		expectedPosts, expectedDeletes = 2, 2
	}
	if posts != expectedPosts || deletes != expectedDeletes {
		t.Fatal("revision replayed or partially applied", mode, posts, deletes)
	}
	if (strings.HasPrefix(faultMode, "lost-") || faultMode == "rejected-metadata-receipt") && hits != 1 {
		t.Fatal("revision acknowledgement fault not exercised", hits)
	}
	state := journalState(t, a)
	if metadataLifecycle && mode != "metadata-reintroduce" && mode != "metadata-mixed" && len(state.CredentialApplications) != len(initial.CredentialApplications) {
		t.Fatal("metadata execution changed delivered credential history")
	}
	if !reflect.DeepEqual(state.ActionApplications[:len(initial.ActionApplications)], initial.ActionApplications) || !reflect.DeepEqual(state.CredentialApplications[:len(initial.CredentialApplications)], initial.CredentialApplications) {
		t.Fatal("revision rewrote original or archived audit")
	}
	if !success {
		if faultMode == "configmap-create-source-loss" {
			if state.Operation != nil || len(state.CredentialApplications) != 2 || len(state.ActionApplications) != 5 || !strings.HasPrefix(state.ActionApplications[4].Operation.Resource, discoveryCredentialMetadataScopePrefix) || state.ActionApplications[4].Operation.TaskID != "revision-create-1" {
				t.Fatal("source-lost original metadata creation failed or fabricated delivery", state.Operation)
			}
		} else if faultMode == "configmap-source-loss" {
			if state.Operation != nil || len(state.CredentialApplications) != 2 || len(state.ActionApplications) != 4 {
				t.Fatal("source-lost original retirement failed or fabricated delivery", state.Operation)
			}
		} else if deletes > prefixDeletes && state.Operation == nil {
			t.Fatal("unproven operation lost its fence")
		}
		return
	}
	current, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if meta.FindStatusCondition(current.Status.Conditions, discoveryRetirementCondition).Message != rootMessage {
		t.Fatal("revision replaced immutable custody root")
	}
	if cm != nil && mode == "rotate-configmap" && c.Generation != rootGeneration {
		t.Fatal("ConfigMap-only revision incorrectly required a spec generation")
	}
	baseline := state.DeepCopy()
	if !converge(3) || posts != expectedPosts || deletes != expectedDeletes || !reflect.DeepEqual(baseline, journalState(t, a)) {
		t.Fatal("settled revision replayed")
	}
	if err := admin.Delete(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := admin.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	done := false
	for i := 0; i < 45; i++ {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := admin.Get(ctx, key, c); err != nil {
			t.Fatal(err)
		}
		current, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if discoveryDeletionCompleted(current, c) {
			done = true
			break
		}
	}
	if !done || len(jobs) != 0 || current.Status.Configuration.Operation != nil {
		t.Fatal("revised source could not complete config cleanup", c.Status.Conditions, journalState(t, a).Operation)
	}
	closed := journalState(t, a).DeepCopy()
	creators := 0
	for _, row := range closed.ActionApplications {
		if discoveryCredentialReceiptSelected(row.Resource) {
			creators++
		}
	}
	if len(closed.ActionApplications) != 2*creators || !reflect.DeepEqual(closed.ActionApplications[:len(initial.ActionApplications)], initial.ActionApplications) {
		t.Fatal("cleanup lost version/retirement lineage")
	}
	manager := &controller.NetworkControllerReconciler{Client: discoveryLifecycleIndexedClient{admin}, APIReader: admin}
	if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
		t.Fatal(err)
	}
	if err := admin.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	if hasDiscoveryConfigFinalizer(c) || len(c.Finalizers) != 1 || c.Finalizers[0] != "example.com/keeper" || !reflect.DeepEqual(closed, journalState(t, a)) {
		t.Fatal("manager failed revised-source finalization")
	}
	if empty || metadataLifecycle {
		a.statusWriter = admin
		exerciseDiscoveryCredentialHandoffObservation(t, a, c, "custody-public-cleanup-inline", realAPI, []configapi.NetworkControllerSecretRef{ref})
		after := journalState(t, a)
		if len(after.ActionApplications) != len(closed.ActionApplications)+2 || len(after.CredentialApplications) != len(closed.CredentialApplications)+1 || !reflect.DeepEqual(after.ActionApplications[:len(closed.ActionApplications)], closed.ActionApplications) || !reflect.DeepEqual(after.CredentialApplications[:len(closed.CredentialApplications)], closed.CredentialApplications) {
			t.Fatal("empty-source handoff rewrote history or failed its next lifecycle")
		}
		floor := int64(0)
		for _, app := range closed.CredentialApplications {
			for _, version := range app.Versions {
				if version.Version > floor {
					floor = version.Version
				}
			}
		}
		if after.CredentialApplications[len(closed.CredentialApplications)].Versions[0].Version != floor+1 {
			t.Fatal("empty-source handoff lost delivered version floors")
		}
	}
}
