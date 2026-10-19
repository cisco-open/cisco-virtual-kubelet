// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const emptyDiscoveryModel = `{"catalyst_center":{"inventory":{"discovery":[]}}}`
const omittedDiscoveryModel = `{"catalyst_center":{"inventory":{}}}`

type discoveryPruneWorld struct {
	jobs           map[string]discoveryMetadata
	tasks          map[string]map[string]any
	writes         []string
	mode           string
	createActive   bool
	allowedCreates map[string]bool
}

func discoveryPruneFixture(t *testing.T) (*adapter, *configapi.NetworkControllerConfig, *discoveryPruneWorld) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"inventory"}
	config.Generation = 2
	config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	config.Spec.Source.Inline.Raw = []byte(emptyDiscoveryModel)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	world := &discoveryPruneWorld{jobs: map[string]discoveryMetadata{}, tasks: map[string]map[string]any{}, allowedCreates: map[string]bool{"fixture": true}}
	world.jobs["fixture"] = discoveryMetadata{ID: "42", Name: "fixture", Type: "Range", Addresses: "192.0.2.1-192.0.2.1", Protocol: "SSH", CredentialIDs: []string{"credential-id"}, Condition: "Complete", Status: "Inactive"}
	foreign := world.jobs["fixture"]
	foreign.Name = "foreign"
	foreign.ID = "foreign-id"
	world.jobs["foreign"] = foreign
	payload := map[string]any{"name": "fixture", "discoveryType": "SINGLE", "ipAddressList": "192.0.2.1", "protocolOrder": "SSH", "globalCredentialIdList": []string{"credential-id"}}
	submitted := metav1.NewTime(time.Now().Add(-time.Second))
	verified := metav1.Now()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: config.Name, ConfigUID: string(config.UID)}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: "inventory", Resource: ownedDiscoveryReceiptKey("fixture", "42"), Operation: cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: 1, ControllerGeneration: nc.Generation, Section: "inventory", Resource: ownedDiscoveryResource("fixture", payload), Action: "Create", Phase: "Verified", TaskID: "original-create", SubmittedAt: &submitted}, VerifiedAt: &verified}}}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		reply := func(value any) { json.NewEncoder(w).Encode(map[string]any{"response": value}) }
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.Method == "GET" && r.URL.Path == discoveryPath+"/1/100":
			rows := []discoveryMetadata{}
			for _, row := range world.jobs {
				rows = append(rows, row)
			}
			reply(rows)
		case r.Method == "GET" && r.URL.Path == globalCredentialsPath:
			reply(map[string]any{"cliCredential": []map[string]any{{"id": "credential-id", "description": "lab-cli"}}})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, taskPath):
			task, ok := world.tasks[strings.TrimPrefix(r.URL.Path, taskPath)]
			if !ok {
				t.Error("unknown task")
				w.WriteHeader(404)
				return
			}
			reply(task)
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, discoveryPath+"/"):
			id := strings.TrimPrefix(r.URL.Path, discoveryPath+"/")
			if id == "foreign-id" {
				t.Error("foreign deletion")
				w.WriteHeader(500)
				return
			}
			world.writes = append(world.writes, "DELETE "+id)
			if world.mode == "backend-error" {
				w.WriteHeader(409)
				return
			}
			found := false
			for name, row := range world.jobs {
				if row.ID == id {
					delete(world.jobs, name)
					found = true
				}
			}
			if !found {
				t.Error("deleting unknown ID")
				w.WriteHeader(404)
				return
			}
			tid := fmt.Sprint("task-", len(world.writes))
			stamp := time.Now().UnixMilli()
			world.tasks[tid] = map[string]any{"id": tid, "startTime": stamp, "endTime": stamp, "isError": false}
			if world.mode == "lost-ack" {
				reply(map[string]any{})
				return
			}
			reply(Task{ID: tid})
		case r.Method == "POST" && r.URL.Path == discoveryPath:
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || !world.allowedCreates[fmt.Sprint(body["name"])] {
				t.Error("unexpected create")
				w.WriteHeader(400)
				return
			}
			name := body["name"].(string)
			world.writes = append(world.writes, "POST "+name)
			id := fmt.Sprint("created-", len(world.writes))
			row := world.jobs["foreign"]
			row.Name = name
			row.ID = id
			row.Protocol = body["protocolOrder"].(string)
			if world.createActive {
				row.Condition = "In Progress"
				row.Status = "Active"
			}
			world.jobs[name] = row
			tid := fmt.Sprint("task-", len(world.writes))
			stamp := time.Now().UnixMilli()
			data, _ := json.Marshal(map[string]any{"discoveryId": id})
			world.tasks[tid] = map[string]any{"id": tid, "data": string(data), "startTime": stamp, "endTime": stamp, "isError": false}
			reply(Task{ID: tid})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	return a, config, world
}
func TestDiscoveryOwnedPrunePublicCycle(t *testing.T) {
	for _, mode := range []string{"prune", "retain", "lost-ack", "backend-error", "source-change", "prune-omitted", "retain-omitted", "prune-failed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, world := discoveryPruneFixture(t)
			if mode == "prune-failed" {
				row := world.jobs["fixture"]
				row.Condition, row.Status = "Failed", "Inactive"
				world.jobs["fixture"] = row
				mode = "prune"
			}
			if strings.HasSuffix(mode, "-omitted") {
				config.Spec.Source.Inline.Raw = []byte(omittedDiscoveryModel)
				mode = strings.TrimSuffix(mode, "-omitted")
			}
			if mode == "retain" {
				config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
			}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			world.mode = mode
			recovered := map[string]bool{}
			changed := false
			tick := func() {
				t.Helper()
				restart := *a
				if _, err := (&configReconciler{a: &restart}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
					t.Fatal(err)
				}
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if op := nc.Status.Configuration.Operation; op != nil && op.Phase == "Accepted" && !recovered[op.ID] {
					recovered[op.ID] = true
					op.Phase = "Unknown"
					raw, _ := json.Marshal(nc.Status.Configuration)
					_ = json.Unmarshal(raw, &nc.Status.Configuration)
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Fatal(err)
					}
					if mode == "source-change" && !changed {
						changed = true
						config.Generation++
						config.Spec.Source.Inline.Raw = discoveryModel()
						if err := a.statusWriter.Update(ctx, config); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			run := func() {
				t.Helper()
				for range 45 {
					tick()
					if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
						break
					}
				}
			}
			run()
			state := journalState(t, a)
			if mode == "retain" {
				if len(world.writes) != 0 || len(world.jobs) != 2 || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || state.ActionApplications[0].Operation.Action != "Create" {
					t.Fatal("Retain removed resources", world.writes, config.Status.Conditions)
				}
				return
			}
			if mode != "prune" {
				if len(world.writes) != 1 || state.Operation == nil || state.ActionApplications[0].Operation.Action != "Create" || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("uncertain cleanup released or replayed", world.writes, config.Status.Conditions)
				}
				return
			}
			if len(world.writes) != 1 || len(world.jobs) != 1 || state.Operation != nil || len(state.ActionApplications) != 1 || !validOwnedDiscoveryReceipt(state.ActionApplications[0]) || !strings.HasSuffix(state.ActionApplications[0].Operation.Resource, discoveryPruneSuffix) || len(recovered) != 1 || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("owned prune did not converge", world.writes, config.Status.Conditions)
			}
			config.Generation++
			config.Spec.Source.Inline.Raw = []byte(omittedDiscoveryModel)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			run()
			for range 3 {
				tick()
			}
			if len(world.writes) != 1 || len(recovered) != 1 {
				t.Fatal("empty tombstone replayed")
			}
			config.Generation++
			config.Spec.Source.Inline.Raw = discoveryModel()
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			run()
			state = journalState(t, a)
			if len(world.writes) != 2 || len(recovered) != 2 || state.Operation != nil || state.ActionApplications[0].Operation.Action != "Create" || state.ActionApplications[0].Resource == ownedDiscoveryReceiptKey("fixture", "42") || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("reintroduction failed", world.writes, config.Status.Conditions)
			}
			config.Generation++
			config.Spec.Source.Inline.Raw = []byte(emptyDiscoveryModel)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			run()
			for range 3 {
				tick()
			}
			state = journalState(t, a)
			if len(world.writes) != 3 || len(recovered) != 3 || len(world.jobs) != 1 || world.jobs["foreign"].ID != "foreign-id" || state.Operation != nil || state.ActionApplications[0].Operation.Action != "Delete" || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("final cleanup failed", world.writes, config.Status.Conditions)
			}
		})
	}
}
func TestDiscoveryPruneFinalDispatchAndIdentity(t *testing.T) {
	for _, mode := range []string{"stable", "active", "renamed", "replaced", "missing", "changed-effect", "receipt-pruned", "owner-changed", "paused", "source-changed", "foreign-unrelated"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, world := discoveryPruneFixture(t)
			nc, _ := a.currentController(ctx)
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			plan, handled, err := a.compileOwnedDiscoveryApply(nc, config, source)
			if err != nil || !handled {
				t.Fatal("compile", err)
			}
			write, err := plan.executor.next(ctx)
			if err != nil || write == nil {
				t.Fatal("plan", err)
			}
			op, err := a.prepareConfiguration(ctx, config, source, plan.section, write.resource, write.action, write.identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.startConfiguration(ctx, config, source, op.ID, write.identity); err != nil {
				t.Fatal(err)
			}
			row := world.jobs["fixture"]
			switch mode {
			case "active":
				row.Status = "Active"
				world.jobs["fixture"] = row
			case "renamed":
				delete(world.jobs, "fixture")
				row.Name = "renamed"
				world.jobs[row.Name] = row
			case "replaced":
				row.ID = "replacement"
				world.jobs["fixture"] = row
			case "missing":
				delete(world.jobs, "fixture")
			case "changed-effect":
				row.Protocol = "TELNET"
				world.jobs["fixture"] = row
			case "foreign-unrelated":
				foreign := world.jobs["foreign"]
				foreign.Protocol = "TELNET"
				world.jobs["foreign"] = foreign
			case "source-changed":
				config.Generation++
				config.Spec.Source.Inline.Raw = []byte(omittedDiscoveryModel)
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			default:
				nc, _ = a.currentController(ctx)
				switch mode {
				case "receipt-pruned":
					nc.Status.Configuration.ActionApplications = nil
				case "owner-changed":
					nc.Status.Configuration.Owners[0].ConfigUID = "other"
				case "paused":
					nc.Spec.Paused = true
				}
				if mode == "paused" {
					if err := a.statusWriter.Update(ctx, nc); err != nil {
						t.Fatal(err)
					}
				} else if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			_, err = write.submit(ctx)
			allowed := mode == "stable" || mode == "active" || mode == "foreign-unrelated"
			expected := 0
			if allowed {
				expected = 1
			}
			if (err == nil) != allowed || len(world.writes) != expected {
				t.Fatal("dispatch mismatch", err, world.writes)
			}
			if _, err := write.submit(ctx); err == nil || len(world.writes) != expected {
				t.Fatal("replayed consumed plan")
			}
		})
	}
}

func TestDiscoveryPruneScopeAndEmptyModel(t *testing.T) {
	for _, raw := range []string{emptyDiscoveryModel, omittedDiscoveryModel} {
		a, config, _ := discoveryPruneFixture(t)
		config.Spec.Source.Inline.Raw = []byte(raw)
		nc, _ := a.currentController(context.Background())
		plan, handled, err := a.compileOwnedDiscoveryApply(nc, config, &configSourceSnapshot{raw: []byte(raw)})
		if err != nil || !handled || plan.executor.count() != 0 || !plan.executor.(*ownedDiscoveryExecutor).prune {
			t.Fatal("empty discovery prune compilation", err)
		}
		reports, err := a.client.compileConfigReports([]byte(raw), []string{"inventory"})
		if err != nil || len(reports) != 1 || reports[0].count != 0 {
			t.Fatal("empty discovery Report compilation", err)
		}
	}
	for _, mode := range []string{"null", "unknown", "tags", "devices", "prior-other-receipt", "prior-credential", "other-section", "CR-delete"} {
		t.Run(mode, func(t *testing.T) {
			a, config, _ := discoveryPruneFixture(t)
			nc, _ := a.currentController(context.Background())
			source := &configSourceSnapshot{raw: []byte(emptyDiscoveryModel)}
			switch mode {
			case "null":
				source.raw = []byte(`{"catalyst_center":{"inventory":{"discovery":null}}}`)
			case "unknown":
				source.raw = []byte(`{"catalyst_center":{"inventory":{"discovery":[],"unknown":{}}}}`)
			case "tags":
				source.raw = []byte(`{"catalyst_center":{"inventory":{"discovery":[],"tags":[]}}}`)
			case "devices":
				source.raw = []byte(`{"catalyst_center":{"inventory":{"discovery":[],"devices":[]}}}`)
			case "prior-other-receipt":
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: "other", Operation: cvk.NetworkControllerConfigurationOperation{ConfigUID: string(config.UID)}})
			case "prior-credential":
				nc.Status.Configuration.CredentialApplications = []cvk.NetworkControllerConfigurationCredentialApplication{{ConfigUID: string(config.UID)}}
			case "other-section":
				nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, cvk.NetworkControllerSectionOwner{Section: "templates", ConfigUID: string(config.UID)})
			case "CR-delete":
				config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
			}
			if _, handled, err := a.compileOwnedDiscoveryApply(nc, config, source); !handled || err == nil {
				t.Fatal("unsupported prune scope accepted", handled, err)
			}
		})
	}
}

func TestDiscoveryPruneWholeCohortAndOwnershipPreflight(t *testing.T) {
	for _, mode := range []string{"active-other-owned", "legacy", "foreign-owner", "externally-missing-kept", "duplicate", "foreign-tombstone-replacement", "unowned"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, world := discoveryPruneFixture(t)
			nc, _ := a.currentController(ctx)
			switch mode {
			case "active-other-owned":
				row := world.jobs["fixture"]
				row.Name = "second"
				row.ID = "second-id"
				row.Status = "Active"
				world.jobs[row.Name] = row
				r := *nc.Status.Configuration.ActionApplications[0].DeepCopy()
				effect, err := discoveryNativeEffect(row, discoveryRequiredMask)
				if err != nil {
					t.Fatal(err)
				}
				r.Resource = ownedDiscoveryReceiptKey(row.Name, row.ID)
				r.Operation.Resource = ownedDiscoveryResource(row.Name, effect)
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, r)
			case "legacy":
				r := &nc.Status.Configuration.ActionApplications[0]
				mask, hash, _ := ownedDiscoveryFingerprintParts(r.Operation.Resource)
				if mask == 0 {
					t.Fatal("fixture mask")
				}
				r.Operation.Resource = ownedDiscoveryPrefix + discoveryPublicDigest("fixture") + ":" + hash
			case "foreign-owner":
				nc.Status.Configuration.ActionApplications[0].Operation.ConfigUID = "other"
			case "externally-missing-kept":
				delete(world.jobs, "fixture")
				config.Spec.Source.Inline.Raw = discoveryModel()
			case "duplicate":
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, nc.Status.Configuration.ActionApplications[0])
			case "foreign-tombstone-replacement":
				r := &nc.Status.Configuration.ActionApplications[0]
				r.Operation.Action = "Delete"
				r.Operation.TargetID = "42"
				r.Operation.Resource += discoveryPruneSuffix
				row := world.jobs["fixture"]
				row.ID = "new-id"
				world.jobs["fixture"] = row
			case "unowned":
				nc.Status.Configuration.ActionApplications = nil
			}
			plan, handled, err := a.compileOwnedDiscoveryApply(nc, config, &configSourceSnapshot{raw: config.Spec.Source.Inline.Raw})
			if err != nil || !handled {
				t.Fatal("compile", err)
			}
			write, err := plan.executor.next(ctx)
			allowed := mode == "foreign-owner" || mode == "unowned"
			if mode == "active-other-owned" {
				if err != nil || write == nil {
					t.Fatal("owned active cohort cannot stop/remove", err)
				}
			} else if allowed {
				if err != nil || write != nil {
					t.Fatal("unowned allocation selected", err)
				}
			} else if err == nil || write != nil {
				t.Fatal("incomplete/foreign cohort authorized", err)
			}
			if len(world.writes) != 0 {
				t.Fatal("preflight mutated native jobs")
			}
		})
	}
}

func TestDiscoveryPruneReintroductionRequiresLaterInput(t *testing.T) {
	a, config, _ := discoveryPruneFixture(t)
	nc, _ := a.currentController(context.Background())
	r := *nc.Status.Configuration.ActionApplications[0].DeepCopy()
	r.Operation.Action = "Delete"
	r.Operation.TargetID = "42"
	r.Operation.Resource += discoveryPruneSuffix
	r.Operation.ConfigGeneration = config.Generation
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{r}}
	create := r.Operation.DeepCopy()
	create.Action = "Create"
	create.TargetID = ""
	create.Resource = strings.TrimSuffix(create.Resource, discoveryPruneSuffix)
	if err := ownedDiscoveryCapacity(state, create); err == nil {
		t.Fatal("same input recreated pruned job")
	}
	create.ConfigGeneration++
	if err := ownedDiscoveryCapacity(state, create); err != nil {
		t.Fatal("later source revision denied", err)
	}
	create.ConfigGeneration = r.Operation.ConfigGeneration
	create.SourceUID = "source"
	create.SourceVersion = "2"
	if err := ownedDiscoveryCapacity(state, create); err != nil {
		t.Fatal("later bound ConfigMap revision denied", err)
	}
	malformed := create.DeepCopy()
	malformed.Resource += discoveryPruneSuffix
	if ownedDiscoveryOperation(malformed) {
		t.Fatal("prune marker accepted for Create")
	}
}

func TestDiscoveryPrunePreservesDeclaredImportAndForeignReceipt(t *testing.T) {
	ctx := context.Background()
	a, config, world := discoveryPruneFixture(t)
	config.Spec.Source.Inline.Raw = []byte(strings.ReplaceAll(string(discoveryModel()), `"fixture"`, `"foreign"`))
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	nc, _ := a.currentController(ctx)
	foreign := *nc.Status.Configuration.ActionApplications[0].DeepCopy()
	foreign.Operation.ID = strings.Repeat("c", 32)
	foreign.Operation.ConfigUID = "other-owner"
	foreign.Operation.ConfigName = "other-config"
	row := world.jobs["foreign"]
	row.Name = "elsewhere"
	effect, err := discoveryNativeEffect(row, discoveryRequiredMask)
	if err != nil {
		t.Fatal(err)
	}
	foreign.Operation.Resource = ownedDiscoveryResource(row.Name, effect)
	foreign.Resource = ownedDiscoveryReceiptKey(row.Name, "77")
	nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, foreign)
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	for range 40 {
		restart := *a
		if _, err := (&configReconciler{a: &restart}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			break
		}
	}
	state := journalState(t, a)
	if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || len(world.writes) != 1 || world.writes[0] != "DELETE 42" || len(world.jobs) != 1 || world.jobs["foreign"].ID != "foreign-id" || len(state.ActionApplications) != 2 {
		t.Fatal("declared import/foreign result was pruned or adopted", world.writes, config.Status.Conditions)
	}
	seen := false
	for _, r := range state.ActionApplications {
		if r.Operation.ConfigUID == "other-owner" {
			if !discoveryDispatchReceiptUnchanged([]cvk.NetworkControllerConfigurationActionApplication{foreign}, []cvk.NetworkControllerConfigurationActionApplication{r}, foreign.Operation.Resource) {
				t.Fatal("foreign receipt changed")
			}
			seen = true
		}
	}
	if !seen {
		t.Fatal("foreign receipt lost")
	}
}
