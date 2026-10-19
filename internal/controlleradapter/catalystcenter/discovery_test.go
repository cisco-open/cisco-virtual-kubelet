// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
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
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func discoveryModel() []byte {
	return []byte(`{"catalyst_center":{"inventory":{"discovery":[{"name":"fixture","type":"SINGLE","ip_address_list":"192.0.2.1","protocol_order":"SSH","global_credential_list":["lab-cli"]}]}}}`)
}
func TestDiscoveryManifestCreateRestartAndNoOp(t *testing.T) {
	for _, combined := range []bool{false, true} {
		t.Run(fmt.Sprint("combined=", combined), func(t *testing.T) { discoveryManifestCycle(t, combined) })
	}
}
func TestDiscoveryLegacyAcceptedRecovery(t *testing.T) { discoveryManifestCycle(t, false, true) }
func TestDiscoveryOwnedReplacementCycle(t *testing.T) {
	discoveryManifestCycleOptions(t, false, false, true)
}
func discoveryManifestCycle(t *testing.T, combined bool, legacy ...bool) {
	discoveryManifestCycleOptions(t, combined, len(legacy) > 0 && legacy[0], false)
}
func discoveryManifestCycleOptions(t *testing.T, combined, legacyMode, replace bool) {
	discoveryManifestScenario(t, discoveryCycleScenario{combined: combined, legacy: legacyMode, replacement: replace})
}

type discoveryCycleScenario struct {
	combined, legacy, replacement, inventoryOnlyTags, reverseSections bool
	composeLater, selected, defaults                                  bool
	nullable                                                          bool
	install                                                           func(*adapter, *configapi.NetworkControllerConfig)
}

func TestDiscoveryMixedOwnedReplacement(t *testing.T) {
	for _, mode := range []discoveryCycleScenario{{combined: true, replacement: true}, {combined: true, replacement: true, reverseSections: true}, {combined: true, replacement: true, inventoryOnlyTags: true}} {
		t.Run(fmt.Sprintf("inventoryOnly=%v/reversed=%v", mode.inventoryOnlyTags, mode.reverseSections), func(t *testing.T) { discoveryManifestScenario(t, mode) })
	}
}
func TestDiscoveryPureToMixedOwnership(t *testing.T) {
	discoveryManifestScenario(t, discoveryCycleScenario{combined: true, replacement: true, composeLater: true})
}
func TestDiscoveryMixedLegacyAcceptedRecovery(t *testing.T) {
	discoveryManifestScenario(t, discoveryCycleScenario{combined: true, legacy: true})
}
func discoveryManifestScenario(t *testing.T, mode discoveryCycleScenario) {
	combined, legacyMode, replace := mode.combined, mode.legacy, mode.replacement
	restoredLegacy := false
	recovered := map[string]bool{}
	model := discoveryModel()
	if mode.nullable {
		model = discoveryNullFixture(t, model, false)
	}
	if mode.selected {
		model = selectedDiscoveryFixture(t, model)
	}
	if mode.defaults {
		model, _ = discoveryDefaultsFixture(t, model, "SSH")
	}
	if combined {
		if !mode.inventoryOnlyTags {
			model = []byte(strings.Replace(string(model), `"inventory":`, `"templates":{"tags":[{"name":"shared","description":"template"}]},"inventory":`, 1))
		}
		model = []byte(strings.Replace(string(model), `"inventory":{`, `"inventory":{"tags":[{"name":"shared","description":"inventory"}],`, 1))
	}
	mixedModel := model
	if mode.composeLater {
		model = discoveryModel()
	}
	ctx := context.Background()
	a, config := journalFixture(t)
	if mode.defaults {
		config.Spec.ModelSource.Exporter = DiscoveryDefaultsExporter
	}
	config.Spec.ManagedSections = []string{"inventory"}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: model}
	if combined && !mode.inventoryOnlyTags && !mode.composeLater {
		config.Spec.ManagedSections = []string{"inventory", "templates"}
		if mode.reverseSections {
			config.Spec.ManagedSections = []string{"templates", "inventory"}
		}
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	writes, reads := 0, 0
	lastID := "42"
	var job *discoveryMetadata
	var tags []observedTag
	tagWrites := 0
	stamp := int64(0)
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case combined && r.URL.Path == tagsPath && r.Method == "GET":
			if tags == nil {
				tags = []observedTag{}
			}
			json.NewEncoder(w).Encode(map[string]any{"response": tags})
		case combined && r.URL.Path == tagsPath && r.Method == "POST":
			var tag observedTag
			if json.NewDecoder(r.Body).Decode(&tag) != nil || tag.Description == nil || *tag.Description != "inventory" {
				t.Error("tag precedence lost")
				w.WriteHeader(400)
				return
			}
			tag.ID = "22f33fa0-e275-41c9-a50d-d7e5135adb8c"
			system := false
			tag.SystemTag = &system
			tags = []observedTag{tag}
			tagWrites++
			stamp = time.Now().UnixMilli()
			fmt.Fprint(w, `{"response":{"taskId":"tag-task"}}`)
		case combined && r.URL.Path == taskPath+"tag-task":
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "tag-task", "serviceType": "Grouping Service", "data": "22f33fa0-e275-41c9-a50d-d7e5135adb8c", "startTime": stamp, "endTime": stamp, "isError": false}})
		case r.URL.Path == globalCredentialsPath:
			fmt.Fprint(w, `{"response":{"cliCredential":[{"id":"credential-id","description":"lab-cli"}]}}`)
		case r.URL.Path == discoveryPath+"/1/100":
			list := []discoveryMetadata{}
			if job != nil {
				reads++
				if reads > 2 {
					job.Condition = "Complete"
					job.Status = "Inactive"
				}
				list = append(list, *job)
			}
			json.NewEncoder(w).Encode(map[string]any{"response": list})
		case r.URL.Path == discoveryPath && r.Method == "POST":
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid create")
			}
			if len(body) != 5 || body["globalCredentialIdList"].([]any)[0] != "credential-id" {
				t.Error("credential resolution or payload failed")
			}
			writes++
			stamp = time.Now().UnixMilli()
			lastID = fmt.Sprint(41 + writes)
			reads = 0
			job = &discoveryMetadata{ID: lastID, Name: "fixture", Type: "Range", Addresses: "192.0.2.1-192.0.2.1", Protocol: body["protocolOrder"].(string), CredentialIDs: []string{"credential-id"}, Condition: "In Progress", Status: "Active"}
			fmt.Fprint(w, `{"response":{"taskId":"discovery-task"}}`)
		case job != nil && r.Method == "DELETE" && r.URL.Path == discoveryPath+"/"+job.ID:
			writes++
			stamp = time.Now().UnixMilli()
			job = nil
			fmt.Fprint(w, `{"response":{"taskId":"discovery-task"}}`)
		case r.URL.Path == taskPath+"discovery-task":
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "discovery-task", "data": fmt.Sprintf(`{"discoveryId":%s}`, lastID), "startTime": stamp, "endTime": stamp, "isError": false}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	if mode.install != nil {
		mode.install(a, config)
	}
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
		current := journalState(t, a)
		if replace && current.Operation != nil && ownedDiscoverySelected(current.Operation.Resource) && current.Operation.Phase == "Accepted" && !recovered[current.Operation.ID] {
			controller, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			controller.Status.Configuration.Operation.Phase = "Unknown"
			if err := a.statusWriter.Status().Update(ctx, controller); err != nil {
				t.Fatal(err)
			}
			recovered[current.Operation.ID] = true
		}
		if legacyMode && !restoredLegacy && current.Operation != nil && ownedDiscoverySelected(current.Operation.Resource) && current.Operation.Phase == "Accepted" {
			controller, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			resource := "discovery:fixture"
			if strings.HasPrefix(current.Operation.Resource, "discoveries/") {
				resource = "discoveries/" + resource
			}
			controller.Status.Configuration.Operation.Resource = resource
			if err := a.statusWriter.Status().Update(ctx, controller); err != nil {
				t.Fatal(err)
			}
			restoredLegacy = true
		}
	}
	var immutableTagCreator *ciskov1.NetworkControllerConfigurationActionApplication
	discoveryState := func() *ciskov1.NetworkControllerConfigurationStatus {
		state := journalState(t, a).DeepCopy()
		records, err := tagOwnedRecords(state)
		if err != nil {
			t.Fatal("invalid companion tag receipt", err)
		}
		if tagWrites > 0 {
			if len(records.created) != 1 || len(records.applied) != 0 || len(tags) != 1 {
				t.Fatal("companion tag creator missing, duplicated or adopted")
			}
			creator, exists := records.created[featureIdentityDigest(tags[0].Name)]
			_, id, _, err := tagReceiptIdentity(creator)
			if !exists || err != nil || id != tags[0].ID || creator.Operation.ConfigUID != string(config.UID) {
				t.Fatal("companion tag creator lost native or owner binding", err)
			}
			if immutableTagCreator == nil {
				immutableTagCreator = creator.DeepCopy()
			} else if !reflect.DeepEqual(*immutableTagCreator, creator) {
				t.Fatal("discovery transition rewrote companion tag creator")
			}
		} else if len(records.created) != 0 || len(records.applied) != 0 {
			t.Fatal("undeclared tag ownership was fabricated")
		}
		kept := make([]ciskov1.NetworkControllerConfigurationActionApplication, 0, len(state.ActionApplications))
		for _, r := range state.ActionApplications {
			if !tagOwnedSelected(r.Operation.Resource) {
				kept = append(kept, r)
			}
		}
		state.ActionApplications = kept
		return state
	}
	for range 30 {
		tick()
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			break
		}
	}
	if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || writes != 1 {
		t.Fatal("discovery did not converge", config.Status.Conditions, writes)
	}
	for range 3 {
		tick()
	}
	if writes != 1 || discoveryState().Operation != nil {
		t.Fatal("completed discovery replayed")
	}
	if !legacyMode {
		state := discoveryState()
		if len(state.ActionApplications) != 1 || state.ActionApplications[0].Resource != ownedDiscoveryReceiptKey("fixture", "42") || !validOwnedDiscoveryReceipt(state.ActionApplications[0]) {
			t.Fatal("owned discovery result receipt missing")
		}
	}
	if legacyMode && (!restoredLegacy || len(discoveryState().ActionApplications) != 0) {
		t.Fatal("legacy task replayed or adopted")
	}
	if mode.nullable {
		original := discoveryState().DeepCopy().ActionApplications
		for _, omit := range []bool{true, false} {
			config.Generation++
			config.Spec.Source.Inline.Raw = discoveryNullFixture(t, config.Spec.Source.Inline.Raw, omit)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			for range 12 {
				tick()
				if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
					break
				}
			}
			if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || writes != 1 || !reflect.DeepEqual(original, discoveryState().ActionApplications) {
				t.Fatal("null/omitted transition replayed or rewrote ownership", config.Status, writes)
			}
		}
	}
	if mode.composeLater {
		original := discoveryState().ActionApplications[0]
		config.Generation++
		config.Spec.ManagedSections = []string{"inventory", "templates"}
		config.Spec.Source.Inline.Raw = mixedModel
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		for range 40 {
			tick()
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				break
			}
		}
		state := discoveryState()
		if writes != 1 || tagWrites != 1 || len(state.ActionApplications) != 1 || state.ActionApplications[0].Operation.ID != original.Operation.ID || state.ActionApplications[0].Section != "inventory" || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("composition adopted/recreated native job or rewrote original lineage", writes, config.Status.Conditions)
		}
		model = mixedModel
	}
	if replace {
		old := discoveryState().ActionApplications[0]
		config.Generation++
		if mode.defaults {
			config.Spec.Source.Inline.Raw, _ = discoveryDefaultsFixture(t, config.Spec.Source.Inline.Raw, "SSH,TELNET")
		} else {
			config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(config.Spec.Source.Inline.Raw), `"protocol_order":"SSH"`, `"protocol_order":"SSH,TELNET"`, 1))
		}
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		sawDelete := false
		for range 50 {
			tick()
			state := discoveryState()
			if state.Operation != nil && state.Operation.Action == "Delete" {
				sawDelete = true
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				break
			}
		}
		state := discoveryState()
		if !sawDelete || writes != 3 || state.Operation != nil || len(state.ActionApplications) != 1 || !validOwnedDiscoveryReceipt(state.ActionApplications[0]) || state.ActionApplications[0].Resource == old.Resource || state.ActionApplications[0].Operation.Action != "Create" || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("owned replacement did not converge", writes, config.Status)
		}
		for range 3 {
			tick()
		}
		if writes != 3 {
			t.Fatal("replacement replayed")
		}
		if len(recovered) != 3 {
			t.Fatal("not all owned replacement stages recovered", len(recovered))
		}
	}
	if combined {
		owners := 2
		if mode.inventoryOnlyTags {
			owners = 1
		}
		if tagWrites != 1 || len(discoveryState().Owners) != owners {
			t.Fatal("combined ownership or tag replay", tagWrites)
		}
		total := int64(0)
		for _, section := range config.Status.SectionStatus {
			total += section.ResourceCount
		}
		if total != 2 {
			t.Fatal("incorrect combined resource count", total)
		}
	}
	// A missing ownership record must not turn a matching imported job into
	// permission to destroy/recreate it after the desired source changes.
	beforeChange := writes
	if !legacyMode {
		controller, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		controller.Status.Configuration.ActionApplications = nil
		if err := a.statusWriter.Status().Update(ctx, controller); err != nil {
			t.Fatal(err)
		}
	}
	config.Generation++
	config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(model), "192.0.2.1", "192.0.2.2", 1))
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	tick()
	if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || writes != beforeChange {
		t.Fatal("unowned replacement request silently accepted or rerun")
	}
}
func TestDiscoveryModelRejectsUnresolvedOrSecretIntent(t *testing.T) {
	for _, replace := range [][2]string{
		{`"type":"SINGLE"`, `"type":"UNKNOWN"`},
		{`"192.0.2.1"`, `"192.0.2.8-192.0.2.1"`},
		{`"192.0.2.1"`, `"2001:db8::1"`},
		{`"protocol_order":"SSH"`, `"password_list":["private"]`},
		{`"global_credential_list":["lab-cli"]`, `"global_credential_list":[]`},
		{`"name":"fixture"`, `"name":"fixture","create_per_site":true`},
		{`"name":"fixture"`, `"name":"fixture","cdp_level":1`},
	} {
		if _, err := parseInventoryIntent([]byte(strings.Replace(string(discoveryModel()), replace[0], replace[1], 1))); err == nil {
			t.Fatal("invalid discovery accepted", replace[1])
		}
	}
	if _, err := parseInventoryTags(discoveryModel()); err == nil {
		t.Fatal("tag projection discarded discovery")
	}
}
func TestDiscoveryCrossTypeCredentialAmbiguity(t *testing.T) {
	intent, err := parseInventoryIntent(discoveryModel())
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]map[string]globalCredentialMetadata{"cli_credentials": {"lab-cli": {ID: "cli"}}, "snmpv3_credentials": {"lab-cli": {ID: "snmp"}}}
	if _, err := resolveDiscoveryCredentials(intent.discoveries[0], all); err == nil {
		t.Fatal("ambiguous name adopted")
	}
	delete(all, "snmpv3_credentials")
	fields, err := resolveDiscoveryCredentials(intent.discoveries[0], all)
	if err != nil || fields["globalCredentialIdList"].([]string)[0] != "cli" {
		t.Fatal("unique reference rejected", err)
	}
}
func TestDiscoveryPaginationAndReadShapeFailures(t *testing.T) {
	for _, mode := range []string{"null", "duplicate-id", "duplicate-name", "bad-type"} {
		t.Run(mode, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if mode == "null" {
					fmt.Fprint(w, `{"response":null}`)
					return
				}
				list := []discoveryMetadata{{ID: "1", Name: "one", Type: "Range"}, {ID: "2", Name: "two", Type: "Range"}}
				switch mode {
				case "duplicate-id":
					list[1].ID = "1"
				case "duplicate-name":
					list[1].Name = "one"
				case "bad-type":
					list[1].Type = "unknown"
				}
				json.NewEncoder(w).Encode(map[string]any{"response": list})
			})
			if _, err := c.discoveries(context.Background()); err == nil {
				t.Fatal("invalid discovery inventory accepted")
			}
		})
	}
}

func TestDiscoveryVerifyRequiresTaskResourceAndCompletion(t *testing.T) {
	intent, err := parseInventoryIntent(discoveryModel())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"wrong-resource", "wrong-task", "running", "malformed-data"} {
		t.Run(mode, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case taskPath + "task":
					id, data := "task", `{"discoveryId":"42"}`
					if mode == "wrong-resource" {
						data = `{"discoveryId":"99"}`
					}
					if mode == "wrong-task" {
						id = "other"
					}
					if mode == "malformed-data" {
						data = `{"unexpected":42}`
					}
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": id, "data": data}})
				case globalCredentialsPath:
					fmt.Fprint(w, `{"response":{"cliCredential":[{"id":"credential-id","description":"lab-cli"}]}}`)
				case discoveryPath + "/1/100":
					job := discoveryMetadata{ID: "42", Name: "fixture", Type: "Range", Addresses: "192.0.2.1", Protocol: "SSH", CredentialIDs: []string{"credential-id"}, Condition: "Complete", Status: "Inactive"}
					if mode == "running" {
						job.Condition = "In Progress"
						job.Status = "Active"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []discoveryMetadata{job}})
				default:
					t.Error("unexpected request")
					w.WriteHeader(404)
				}
			})
			e := &discoveryExecutor{client: c, desired: intent.discoveries}
			ok, err := e.verify(context.Background(), &ciskov1.NetworkControllerConfigurationOperation{Action: "Create", Resource: "discovery:fixture", TaskID: "task"})
			if ok || (mode != "running" && err == nil) {
				t.Fatal("unbound or incomplete discovery verified", err)
			}
		})
	}
}
func TestDiscoveryPagination(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == authPath {
				fmt.Fprint(w, `{"Token":"test"}`)
				return
			}
			start, count := 1, 100
			if r.URL.Path == discoveryPath+"/101/100" {
				start, count = 101, 1
				if repeat {
					start = 1
				}
			}
			list := []discoveryMetadata{}
			for i := start; i < start+count; i++ {
				list = append(list, discoveryMetadata{ID: fmt.Sprint(i), Name: fmt.Sprintf("job-%d", i), Type: "Range"})
			}
			json.NewEncoder(w).Encode(map[string]any{"response": list})
		})
		jobs, err := c.discoveries(context.Background())
		if repeat {
			if err == nil {
				t.Fatal("repeated page accepted")
			}
		} else if err != nil || len(jobs) != 101 {
			t.Fatal("incomplete discovery pagination", err)
		}
	}
}
