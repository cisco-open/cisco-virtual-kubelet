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
	"sync/atomic"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestTemplateNoDispatchReplan(t *testing.T) {
	for _, stage := range templateNoDispatchStages {
		for _, mode := range []string{"field-edit", "read-error", "lost-ack"} {
			t.Run(stage+"/"+mode, func(t *testing.T) { exerciseTemplateNoDispatchReplan(t, stage, mode, nil) })
		}
	}
}

var templateNoDispatchStages = []string{"project-create", "project-update", "draft-create", "draft-update", "commit", "composite-draft", "composite-commit"}

func exerciseTemplateNoDispatchReplan(t *testing.T, stage, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	appliedTesting := strings.HasPrefix(mode, "applied-")
	recovering := strings.HasPrefix(mode, "recovery-") || appliedTesting
	baselineTesting := strings.HasPrefix(mode, "baseline-") || appliedTesting
	ownTesting := (strings.HasPrefix(mode, "ownership-") || baselineTesting) && !appliedTesting
	ownMode := strings.TrimPrefix(mode, "ownership-")
	if baselineTesting {
		ownMode = strings.TrimPrefix(mode, "baseline-")
		if appliedTesting {
			ownMode = strings.TrimPrefix(mode, "applied-")
		}
	}
	recoveryMode := strings.TrimPrefix(mode, "recovery-")
	if appliedTesting {
		recoveryMode = "known"
	}
	recoveryKnown := recoveryMode == "known" || recoveryMode == "known-legacy"
	nativeIDs := map[string]string{"project": "11111111-1111-4111-8111-111111111111", "base": "22222222-2222-4222-8222-222222222222", "combined": "33333333-3333-4333-8333-333333333333"}
	nativeID := func(name string) string {
		if id, ok := nativeIDs[name]; ok {
			return id
		}
		return name
	}
	logicalName := func(id string) string {
		for name, value := range nativeIDs {
			if id == value {
				return name
			}
		}
		return id
	}
	a, config := journalFixture(t)
	projectOnly := strings.HasPrefix(stage, "project-")
	composite := strings.HasPrefix(stage, "composite-")
	common := func(name string) map[string]any {
		return map[string]any{"name": name, "language": "JINJA", "software_type": "IOS-XE", "device_types": []any{map[string]any{"product_family": "Switches and Hubs"}}}
	}
	project := map[string]any{"name": "Lab", "description": "desired"}
	if !projectOnly {
		defs := []any{common("base")}
		if composite {
			combined := common("combined")
			combined["composite"] = true
			combined["containing_templates"] = []string{"base"}
			defs = append(defs, combined)
		}
		project["dayn_templates"] = defs
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": map[string]any{"projects": []any{project}}}})
	files := map[string]string{"base.j2": "! CVK base\n"}
	intent, err := parseTemplateIntent(raw, files)
	if err != nil {
		t.Fatal(err)
	}
	config.Spec.ManagedSections = []string{"templates"}
	keyPath := ""
	var comparisonKey []byte
	if baselineTesting {
		keyPath, comparisonKey = configureTemplateNativeComparisonFixture(t, a, config)
		if ownMode == "missing-key" {
			if err := os.Remove(keyPath); err != nil {
				t.Fatal(err)
			}
		}
		if ownMode == "malformed-key" {
			if err := os.WriteFile(keyPath, []byte("invalid key material"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	config.Spec.Source.Inline = nil
	config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: "template-model", Key: "model.yaml"}
	if ownTesting && ownMode == "inline" {
		config.Spec.Source.ConfigMapRef = nil
		config.Spec.Source.Inline = &runtime.RawExtension{Raw: raw}
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if install != nil {
		install(a, config)
	}
	assets, _ := json.Marshal(files)
	model := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "template-model", Namespace: config.Namespace}, Data: map[string]string{"model.yaml": string(raw), nacmodel.TemplateAssetsKey: string(assets)}}
	if install == nil {
		model.UID = "template-source"
	}
	if err := a.statusWriter.Create(ctx, model); err != nil {
		t.Fatal(err)
	}
	clone := func(in map[string]any) map[string]any {
		b, _ := json.Marshal(in)
		var out map[string]any
		json.Unmarshal(b, &out)
		return out
	}
	versionID := func(name string, number int) string {
		prefix := "44444444"
		if name == "combined" {
			prefix = "55555555"
		}
		return fmt.Sprintf("%s-4444-4444-8444-%012x", prefix, number)
	}
	drafts := map[string]map[string]any{}
	committed := map[string][]map[string]any{}
	exists := stage != "project-create"
	description := "desired"
	if stage == "project-update" {
		description = "old"
	}
	if !projectOnly && stage != "draft-create" {
		drafts["base"] = clone(intent.definitions[0].payload)
		drafts["base"]["id"] = nativeID("base")
		drafts["base"]["projectId"] = nativeID("project")
		if stage == "draft-update" {
			drafts["base"]["templateContent"] = "old"
		}
		if composite {
			b := clone(drafts["base"])
			b["id"] = versionID("base", 1)
			b["version"] = "1"
			committed["base"] = []map[string]any{b}
			if stage == "composite-commit" || appliedTesting && stage == "composite-draft" {
				e := &templateExecutor{intent: intent}
				payload, _, ready, err := e.resolvedPayload(intent.definitions[1], map[string]templateObservation{"Lab#base": {summary: templateSummary{ID: nativeID("base")}, draft: drafts["base"], committed: b, version: &templateVersion{ID: versionID("base", 1), Number: 1}}})
				if err != nil || !ready {
					t.Fatal("composite seed", err)
				}
				drafts["combined"] = payload
				payload["id"] = nativeID("combined")
				payload["projectId"] = nativeID("project")
				if stage == "composite-draft" {
					payload["language"] = "VELOCITY"
				}
			}
		}
	}
	var seededCreator cvk.NetworkControllerConfigurationActionApplication
	if appliedTesting {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		source, err := loadControllerSource(ctx, a.statusReader, config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.compilePrivateTemplateDefinitions(nc, config, source); err != nil {
			t.Fatal(err)
		}
		kind, name, projectName := "r", "base", "Lab"
		if projectOnly {
			kind, name, projectName = "p", "Lab", ""
		} else if composite {
			kind, name = "c", "combined"
		}
		seededCreator = templateCreatorReceiptFixture(kind)
		op := &seededCreator.Operation
		op.Resource = templateCreatorResource(kind, projectName, name) + ":c:" + templateComparisonTag + ":" + source.templateComparison.keyID
		op.ConfigName, op.ConfigUID, op.ConfigGeneration, op.ControllerGeneration = config.Name, string(config.UID), config.Generation, nc.Generation
		op.SourceUID, op.SourceVersion = string(source.uid), source.version
		if !projectOnly {
			op.ParentID = nativeID("project")
		}
		id := nativeID(name)
		if projectOnly {
			id = nativeID("project")
		}
		// This fixture seeds an earlier synthetic creator; native cycles establish real origins.
		op.SubmittedAt = &metav1.Time{Time: time.Now().Add(-2 * time.Minute).UTC().Truncate(time.Second)}
		seededCreator.VerifiedAt = &metav1.Time{Time: time.Now().Add(-time.Minute).UTC().Truncate(time.Second)}
		if composite {
			op.PreparedAt = &metav1.Time{Time: time.Now().Add(-3 * time.Minute).UTC().Truncate(time.Second)}
		}
		seededCreator.Resource = templateCreatorResultResource(op.Resource, id, strings.Repeat("a", 64))
		nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "templates", ConfigName: config.Name, ConfigUID: string(config.UID)}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{seededCreator}}
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
		stored, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		seededCreator = *stored.Status.Configuration.ActionApplications[0].DeepCopy()
	}
	var mu sync.Mutex
	var writes atomic.Int32
	var recoveryTrees atomic.Int32
	var injected atomic.Bool
	var authorityInjected atomic.Bool
	var baselineReads atomic.Int32
	keyRestored := false
	retainedTask := ""
	retainedOperation := ""
	stamps := map[string]int64{}
	taskResults := map[string]string{}
	var receiptClient *templateCreatorReceiptClient
	if (ownTesting || appliedTesting) && (strings.HasPrefix(ownMode, "receipt-") || ownMode == "source-fields-pruned" || ownMode == "baseline-pruned" || ownMode == "baseline-wrong-key") {
		receiptClient = &templateCreatorReceiptClient{Client: a.statusWriter, mode: ownMode}
		a.statusWriter = receiptClient
	}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		// Route real UUID paths to their fixture labels without changing response IDs.
		for name, id := range nativeIDs {
			r.URL.Path = strings.ReplaceAll(r.URL.Path, id, name)
		}
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.URL.Path == devicesPath {
			fmt.Fprint(w, `{"response":[]}`)
			return
		}
		if strings.HasPrefix(r.URL.Path, taskPath) {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			stamp, ok := stamps[id]
			if !ok {
				t.Error("unknown task")
				w.WriteHeader(404)
				return
			}
			root := map[string]any{"id": id, "startTime": stamp, "endTime": stamp, "isError": false, "serviceType": "NCTP", "data": taskResults[id]}
			if ownTesting && id == retainedTask && !strings.HasSuffix(r.URL.Path, "/tree") {
				switch ownMode {
				case "missing-data":
					delete(root, "data")
				case "prose-data":
					root["data"] = "created " + taskResults[id]
				case "json-data":
					root["data"] = `{"id":"` + taskResults[id] + `"}`
				case "wrong-service":
					root["serviceType"] = "SWIM"
				case "foreign-task":
					root["id"] = "foreign"
				case "wrong-uuid":
					root["data"] = templateOwnedTestUUID
				}
			}
			if recovering && id == retainedTask && strings.HasSuffix(r.URL.Path, "/tree") {
				recoveryTrees.Add(1)
				if recoveryMode == "missing-tree" {
					w.WriteHeader(503)
					return
				}
				child := map[string]any{"id": "child", "parentId": id, "rootId": id, "startTime": stamp, "endTime": stamp, "isError": false}
				switch recoveryMode {
				case "failed-child":
					child["isError"] = true
				case "pending-child":
					delete(child, "endTime")
				case "foreign-child":
					child["rootId"] = "foreign"
				}
				json.NewEncoder(w).Encode(map[string]any{"response": []any{root, child}})
				return
			}
			writeDefinitionTaskFixture(w, r, root)
			return
		}
		if r.Method == "GET" {
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			if baselineTesting && (ownMode == "key-change-during-proof" || ownMode == "key-change-restore") && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Verifying" {
				target := templateProjectsPath + "/project"
				if !projectOnly {
					target = templatesPath + "/base"
					if composite {
						target = templatesPath + "/combined"
					}
				}
				if r.URL.Path == target && authorityInjected.CompareAndSwap(false, true) {
					if err := os.WriteFile(keyPath, []byte(strings.Repeat("cd", 32)), 0600); err != nil {
						t.Error(err)
					}
				}
			}
			if (ownTesting || appliedTesting) && (ownMode == "source-during-proof" || ownMode == "owner-during-proof") && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Verifying" && r.URL.Path == templateProjectsPath && authorityInjected.CompareAndSwap(false, true) {
				if ownMode == "source-during-proof" {
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(model), model); err != nil {
						t.Error(err)
					}
					model.Data["qualification-revision"] = "changed during native proof"
					if err := a.statusWriter.Update(ctx, model); err != nil {
						t.Error(err)
					}
				} else {
					nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Error(err)
					}
				}
			}
			if (mode == "field-edit" || mode == "read-error") && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Submitting" && injected.CompareAndSwap(false, true) {
				if mode == "read-error" {
					w.WriteHeader(503)
					return
				}
				if projectOnly {
					exists = true
					description = "external change"
				} else if composite {
					drafts["base"]["externalMetadata"] = "changed"
				} else if drafts["base"] == nil {
					drafts["base"] = clone(intent.definitions[0].payload)
					drafts["base"]["id"] = nativeID("base")
					drafts["base"]["projectId"] = nativeID("project")
					drafts["base"]["templateContent"] = "external body"
				} else {
					drafts["base"]["description"] = "external metadata"
				}
			}
			switch {
			case r.URL.Path == templateProjectsPath:
				list := []templateProject{}
				if exists {
					list = append(list, templateProject{ID: nativeID("project"), Name: "Lab", Description: &description})
				}
				json.NewEncoder(w).Encode(list)
			case r.URL.Path == templateProjectsPath+"/project":
				if !exists {
					w.WriteHeader(404)
					return
				}
				body := map[string]any{"id": nativeID("project"), "name": "Lab", "description": description, "templates": []any{}, "nativeMetadata": "private-native-state"}
				if baselineTesting && ownMode == "native-state-race" && injected.Load() {
					body["nativeMetadata"] = fmt.Sprint(baselineReads.Add(1))
				}
				if baselineTesting && ownMode == "wrong-declared-body" {
					body["description"] = "wrong declared description"
				}
				json.NewEncoder(w).Encode(body)
			case r.URL.Path == templatesPath:
				list := []templateSummary{}
				for name := range drafts {
					list = append(list, templateSummary{ID: nativeID(name), Name: name, ProjectID: nativeID("project")})
				}
				json.NewEncoder(w).Encode(list)
			case strings.HasPrefix(r.URL.Path, templateVersionsPath+"/"):
				name := strings.TrimPrefix(r.URL.Path, templateVersionsPath+"/")
				versions := []map[string]any{{"id": nativeID(name)}}
				for i, b := range committed[name] {
					entry := map[string]any{"id": b["id"], "version": fmt.Sprint(i + 1), "versionComment": b["versionComment"]}
					if appliedTesting && ownMode == "version-meta-missing" {
						delete(entry, "versionComment")
					}
					versions = append(versions, entry)
				}
				json.NewEncoder(w).Encode([]any{map[string]any{"templateId": nativeID(name), "versionsInfo": versions}})
			case strings.HasPrefix(r.URL.Path, templatesPath+"/"):
				id := strings.TrimPrefix(r.URL.Path, templatesPath+"/")
				if body, ok := drafts[id]; ok {
					response := clone(body)
					if baselineTesting && ((!composite && id == "base") || (composite && id == "combined")) {
						response["nativeMetadata"] = "private-native-state"
						if ownMode == "native-state-race" && injected.Load() {
							response["nativeMetadata"] = fmt.Sprint(baselineReads.Add(1))
						}
						if ownMode == "wrong-declared-body" {
							response["language"] = "VELOCITY"
						}
					}
					json.NewEncoder(w).Encode(response)
					return
				}
				for _, versions := range committed {
					for _, b := range versions {
						if b["id"] == id {
							response := clone(b)
							if appliedTesting && ownMode == "version-body-comment-omitted" {
								delete(response, "versionComment")
							}
							json.NewEncoder(w).Encode(response)
							return
						}
					}
				}
				w.WriteHeader(404)
			default:
				t.Errorf("unexpected read %s", r.URL.Path)
				w.WriteHeader(404)
			}
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid mutation")
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case templateProjectsPath:
			exists = true
			description = body["description"].(string)
		case templateProjectsPath + "/project/template", templatesPath:
			name := body["name"].(string)
			body["id"] = nativeID(name)
			body["projectId"] = nativeID("project")
			drafts[name] = body
		case templateVersionsPath:
			name := logicalName(body["templateId"].(string))
			b := clone(drafts[name])
			b["id"] = versionID(name, len(committed[name])+1)
			b["version"] = fmt.Sprint(len(committed[name]) + 1)
			b["versionComment"] = body["comments"]
			committed[name] = append(committed[name], b)
		default:
			t.Errorf("unexpected mutation %s", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		seq := writes.Add(1)
		id := fmt.Sprintf("template-task-%d", seq)
		stamps[id] = time.Now().UnixMilli()
		if r.Method == http.MethodPost && r.URL.Path == templateProjectsPath {
			taskResults[id] = nativeID("project")
		}
		if r.Method == http.MethodPost && r.URL.Path == templateProjectsPath+"/project/template" {
			taskResults[id] = body["id"].(string)
		}
		if mode == "lost-ack" {
			injected.Store(true)
			fmt.Fprint(w, `{"response":{}}`)
		} else {
			fmt.Fprintf(w, `{"response":{"taskId":%q}}`, id)
		}
	})
	key := ctrlclient.ObjectKeyFromObject(config)
	sawRefusal, converged := false, false
	tick := func() {
		t.Helper()
		// This controlled fixture advances an unsubmitted composite preparation
		// floor. Native qualification uses real elapsed time, never this shortcut.
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
			op := nc.Status.Configuration.Operation
			if op.Phase == "Prepared" && op.PreparedAt != nil && time.Since(op.PreparedAt.Time) < time.Minute {
				op.PreparedAt = &metav1.Time{Time: time.Now().Add(-2 * time.Minute)}
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
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
		if (recovering || ownTesting) && !injected.Load() {
			current, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if current.Status.Configuration != nil && current.Status.Configuration.Operation != nil && current.Status.Configuration.Operation.Phase == "Accepted" {
				op := current.Status.Configuration.Operation
				if recovering {
					op.Phase = "Unknown"
				}
				if recoveryMode == "known-legacy" && strings.HasPrefix(op.Resource, templateCreatePrefix) {
					op.Resource = "Lab"
					if !projectOnly {
						op.Resource = "draft:Lab#base"
						if composite {
							op.Resource = "draft:Lab#combined"
						}
					}
				}
				mu.Lock()
				retainedTask, retainedOperation = op.TaskID, op.ID
				if ownTesting && ownMode == "same-name-replacement" {
					name := "project"
					if !projectOnly {
						name = "base"
						if composite {
							name = "combined"
						}
						drafts[name]["id"] = templateOwnedTestUUID
					}
					nativeIDs[name] = templateOwnedTestUUID
				}
				if ownTesting && ownMode == "kind-changed" && !projectOnly {
					name := "base"
					if composite {
						name = "combined"
					}
					drafts[name]["composite"] = !composite
				}
				if recoveryMode == "missing-native" {
					if projectOnly {
						exists = false
					} else if composite {
						delete(drafts, "combined")
					} else {
						delete(drafts, "base")
					}
				}
				if appliedTesting && strings.HasPrefix(ownMode, "version-") {
					name := "base"
					if composite {
						name = "combined"
					}
					versions := committed[name]
					if len(versions) == 0 {
						t.Fatal("version fault requested before commit")
					}
					latest := versions[len(versions)-1]
					switch ownMode {
					case "version-missing-nonce":
						latest["versionComment"] = strings.Split(latest["versionComment"].(string), " [cvk:")[0]
					case "version-wrong-nonce":
						latest["versionComment"] = templateCommitComment(strings.Split(latest["versionComment"].(string), " [cvk:")[0], strings.Repeat("f", 32))
					case "version-noncanonical":
						latest["id"] = "foreign-native-version"
					case "version-duplicate-nonce":
						duplicate := clone(latest)
						duplicate["id"] = "88888888-8888-4888-8888-888888888888"
						duplicate["version"] = fmt.Sprint(len(versions) + 1)
						committed[name] = append(versions, duplicate)
					}
				}
				mu.Unlock()
				if recoveryMode == "missing-task" {
					op.TaskID = ""
				}
				if recoveryMode == "parent-changed" {
					op.ParentID = "foreign"
				}
				if recoveryMode == "owner-changed" {
					current.Status.Configuration.Owners[0].ConfigUID = "foreign"
				}
				if err := a.statusWriter.Status().Update(ctx, current); err != nil {
					t.Fatal(err)
				}
				if recoveryMode == "source-changed" {
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(model), model); err != nil {
						t.Fatal(err)
					}
					model.Data["qualification-revision"] = "changed"
					if err := a.statusWriter.Update(ctx, model); err != nil {
						t.Fatal(err)
					}
				}
				injected.Store(true)
			}
		}
		for _, condition := range config.Status.Conditions {
			if condition.Reason == "NativePreflightRefused" {
				state := journalState(t, a)
				if writes.Load() != 0 || state.Operation != nil || len(state.Owners) != 1 || config.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting || result.RequeueAfter <= 0 {
					t.Fatal("invalid no-dispatch retirement", writes.Load(), config.Status.Phase)
				}
				sawRefusal = true
			}
		}
	}
	for range 50 {
		tick()
		if baselineTesting && ownMode == "key-change-restore" && authorityInjected.Load() && !keyRestored {
			state := journalState(t, a)
			if writes.Load() != 1 || state.Operation == nil || state.Operation.Phase != "Verifying" || templateCreatedReceiptPresent(state, state.Operation) {
				t.Fatal("key loss did not fence the original creator")
			}
			if err := os.WriteFile(keyPath, comparisonKey, 0600); err != nil {
				t.Fatal(err)
			}
			keyRestored = true
		}
		if recovering && injected.Load() && !recoveryKnown {
			for range 6 {
				tick()
			}
			break
		}
		if mode == "lost-ack" && injected.Load() {
			for range 3 {
				tick()
			}
			break
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			converged = true
			break
		}
	}
	state := journalState(t, a)
	if state == nil {
		state = &cvk.NetworkControllerConfigurationStatus{}
	}
	if appliedTesting {
		records, err := templateAppliedRecords(state)
		creators, creatorErr := templateCreatedRecords(state)
		if creatorErr != nil || len(creators) != 1 {
			t.Fatal("creator history lost", creatorErr)
		}
		for _, r := range creators {
			if !reflect.DeepEqual(r, seededCreator) {
				t.Fatal("applied state replaced creator")
			}
		}
		positive := ownMode == "version-body-comment-omitted" || ownMode == "known" || ownMode == "key-change-restore" || ownMode == "receipt-conflict" || ownMode == "receipt-lost-ack"
		if positive {
			want := int32(1)
			if stage == "draft-update" || stage == "composite-draft" {
				want = 2
			}
			if err != nil || !converged || state.Operation != nil || len(records) != 1 || writes.Load() != want || recoveryTrees.Load() == 0 {
				t.Fatal("owned applied flow failed", err, writes.Load(), config.Status.Phase, len(records), config.Status.Conditions)
			}
			for _, r := range records {
				if r.Operation.Action != "Update" || !validTemplateComparisonDigest(strings.Split(r.Resource, ":")[7]) {
					t.Fatal("applied baseline missing")
				}
			}
			tick()
			if writes.Load() != want {
				t.Fatal("applied completion replayed")
			}
		} else {
			phase := "Verifying"
			if ownMode == "receipt-pruned" || ownMode == "source-fields-pruned" || ownMode == "baseline-pruned" || ownMode == "baseline-wrong-key" || ownMode == "receipt-version-pruned" || ownMode == "receipt-version-noncanonical" {
				phase = "Verified"
			}
			if converged || state.Operation == nil || state.Operation.ID != retainedOperation || state.Operation.Phase != phase || writes.Load() != 1 || len(records) != 0 {
				t.Fatal("invalid applied proof released/replayed", err, writes.Load(), config.Status.Phase)
			}
		}
		raw, _ := json.Marshal(state)
		if strings.Contains(string(raw), "private-native-state") || strings.Contains(string(raw), "! CVK base") || strings.Contains(string(raw), string(comparisonKey)) {
			t.Fatal("private state leaked")
		}
		return
	}
	if baselineTesting && (ownMode == "missing-key" || ownMode == "malformed-key") {
		if converged || writes.Load() != 0 || state.Operation != nil || len(state.ActionApplications) != 0 || injected.Load() {
			t.Fatal("invalid projection reached native creator execution")
		}
		return
	}
	if !injected.Load() {
		t.Fatal("preflight injection not reached", config.Status)
	}
	if ownTesting {
		positive := ownMode == "known" || ownMode == "inline" || ownMode == "receipt-conflict" || ownMode == "receipt-lost-ack" || ownMode == "noop-replacement" || ownMode == "update-replacement" || baselineTesting && ownMode == "key-change-restore"
		if positive {
			want := int32(1)
			if !projectOnly {
				want = 2
			}
			records, err := templateCreatedRecords(state)
			if err != nil || !converged || state.Operation != nil || writes.Load() != want || len(records) != 1 {
				t.Fatal("creator flow did not converge with one task-bound receipt", writes.Load(), config.Status.Phase, err)
			}
			for _, receipt := range records {
				id, _ := templateCreatedTarget(receipt)
				sourceUID, sourceVersion := string(model.UID), model.ResourceVersion
				if ownMode == "inline" {
					sourceUID, sourceVersion = "", ""
				}
				if receipt.Operation.ID != retainedOperation || receipt.Operation.TaskID != retainedTask || id != taskResults[retainedTask] || receipt.Operation.SourceUID != sourceUID || receipt.Operation.SourceVersion != sourceVersion {
					t.Fatal("creator receipt lost original task/source/native identity")
				}
				key, mac, err := templateCreatorBaseline(receipt)
				if err != nil || baselineTesting && (!validTemplateComparisonDigest(key) || !validTemplateComparisonDigest(mac)) || !baselineTesting && (key != "" || mac != "") {
					t.Fatal("baseline protocol did not match the original creator")
				}
			}
			serialized, err := json.Marshal(state)
			if err != nil || strings.Contains(string(serialized), files["base.j2"]) || strings.Contains(string(serialized), "private-native-state") || len(comparisonKey) > 0 && strings.Contains(string(serialized), string(comparisonKey)) {
				t.Fatal("template content entered creator status")
			}
			if receiptClient != nil {
				wantAttempts := int32(1)
				if ownMode == "receipt-conflict" {
					wantAttempts = 2
				}
				if receiptClient.attempts.Load() != wantAttempts {
					t.Fatal("incorrect creator receipt CAS count", receiptClient.attempts.Load())
				}
			}
			if ownMode == "noop-replacement" || ownMode == "update-replacement" {
				mu.Lock()
				name := "project"
				if !projectOnly {
					name = "base"
					if composite {
						name = "combined"
					}
					drafts[name]["id"] = templateOwnedTestUUID
				}
				nativeIDs[name] = templateOwnedTestUUID
				mu.Unlock()
				if ownMode == "update-replacement" {
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(model), model); err != nil {
						t.Fatal(err)
					}
					if projectOnly {
						model.Data["model.yaml"] = strings.Replace(model.Data["model.yaml"], "desired", "changed desired", 1)
					} else {
						model.Data[nacmodel.TemplateAssetsKey] = `{"base.j2":"! changed source\n"}`
					}
					if err := a.statusWriter.Update(ctx, model); err != nil {
						t.Fatal(err)
					}
				}
				tick()
				if writes.Load() != want || journalState(t, a).Operation != nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("owned UUID replacement was adopted or updated")
				}
			} else {
				tick()
				if writes.Load() != want {
					t.Fatal("owned completion replayed")
				}
			}
		} else {
			phase := "Verifying"
			if ownMode == "receipt-pruned" || ownMode == "source-fields-pruned" || ownMode == "baseline-pruned" || ownMode == "baseline-wrong-key" {
				phase = "Verified"
			}
			if converged || writes.Load() != 1 || state.Operation == nil || state.Operation.ID != retainedOperation || state.Operation.Phase != phase || templateCreatedReceiptPresent(state, state.Operation) {
				t.Fatal("unproved creator operation was released or replayed", writes.Load(), config.Status.Phase)
			}
			if (ownMode == "source-during-proof" || ownMode == "owner-during-proof") && !authorityInjected.Load() {
				t.Fatal("completion authority race was not injected")
			}
			if baselineTesting && ownMode == "key-change-during-proof" && !authorityInjected.Load() {
				t.Fatal("key race not injected")
			}
			if baselineTesting && ownMode == "native-state-race" && baselineReads.Load() < 2 {
				t.Fatal("native snapshot race not injected")
			}
		}
		return
	}
	if recovering {
		if recoveryKnown {
			want := int32(1)
			if stage == "draft-create" || stage == "draft-update" || stage == "composite-draft" {
				want = 2
			}
			if !converged || writes.Load() != want || recoveryTrees.Load() == 0 || state.Operation != nil || len(state.Owners) != 1 || len(state.CredentialApplications) != 0 {
				t.Fatal("acknowledged task recovery did not converge without replay", writes.Load(), config.Status.Phase)
			}
			for _, receipt := range state.ActionApplications {
				if strings.HasPrefix(receipt.Resource, templateCreatedPrefix) {
					if recoveryMode == "known-legacy" {
						t.Fatal("legacy recovery acquired creator ownership")
					}
					if _, err := templateCreatedTarget(receipt); err != nil {
						t.Fatal("invalid creator receipt", err)
					}
				} else if !validTemplateCommitReceipt(&receipt.Operation) || receipt.Operation.CreatedResourceID != "" {
					t.Fatal("invalid recovered action receipt")
				}
			}
			wantReceipts := 1
			if projectOnly {
				wantReceipts = 0
			}
			if recoveryMode != "known-legacy" && (stage == "project-create" || stage == "draft-create" || stage == "composite-draft") {
				wantReceipts++
			}
			if len(state.ActionApplications) != wantReceipts {
				t.Fatal("unexpected recovered commit receipt count")
			}
			tick()
			if writes.Load() != want {
				t.Fatal("recovered operation replayed")
			}
		} else {
			wantPhase := "Unknown"
			if recoveryMode == "missing-native" || recoveryMode == "parent-changed" && !projectOnly && !strings.HasPrefix(state.Operation.Resource, templateCreatePrefix) {
				wantPhase = "Verifying"
			}
			if writes.Load() != 1 || state.Operation == nil || state.Operation.ID != retainedOperation || state.Operation.Phase != wantPhase || len(state.CredentialApplications) != 0 || len(state.ActionApplications) != 0 {
				t.Fatal("unqualified recovery replayed or released its original fence", writes.Load(), config.Status.Phase)
			}
			if recoveryMode != "missing-task" && state.Operation.TaskID != retainedTask {
				t.Fatal("recovery changed the original acknowledged task")
			}
			if (recoveryMode == "missing-task" || recoveryMode == "source-changed" || recoveryMode == "owner-changed") && recoveryTrees.Load() != 0 {
				t.Fatal("unqualified task was observed")
			}
		}
		return
	}
	if mode == "lost-ack" {
		if writes.Load() != 1 || sawRefusal || state.Operation == nil || state.Operation.Phase != "Unknown" || state.Operation.TaskID != "" {
			t.Fatal("lost receipt was replayed or unfenced", writes.Load())
		}
	} else {
		want := int32(1)
		if stage == "draft-create" || stage == "draft-update" || stage == "composite-draft" {
			want = 2
		}
		if !sawRefusal || !converged || state.Operation != nil || writes.Load() != want {
			t.Fatal("replan failed", sawRefusal, converged, writes.Load(), config.Status)
		}
		tick()
		if writes.Load() != want {
			t.Fatal("converged action replayed")
		}
	}
}
