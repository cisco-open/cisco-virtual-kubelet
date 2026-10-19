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
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestWirelessProfileManifestRestart(t *testing.T) {
	qualifyWirelessProfileManifestRestart(t, false, "")
}
func TestWirelessPolicyCohortManifestRestart(t *testing.T) {
	qualifyWirelessProfileManifestRestart(t, true, "")
}
func TestWirelessPolicyCohortLostAckRetainsJournal(t *testing.T) {
	qualifyWirelessProfileManifestRestart(t, true, "lost-ack")
}
func TestWirelessPolicyCohortPartialReadbackRetained(t *testing.T) {
	qualifyWirelessProfileManifestRestart(t, true, "partial")
}
func TestWirelessNamedVLANManifestRestart(t *testing.T) {
	qualifyWirelessProfileManifestRestart(t, false, "vlan-selector")
}
func qualifyWirelessProfileManifestRestart(t *testing.T, cohort bool, variant string, qualifications ...*wirelessProfileRecoveryQualification) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"network_profiles"}
	var qualification *wirelessProfileRecoveryQualification
	if len(qualifications) > 0 {
		qualification = qualifications[0]
	}
	model := func(wlan string) []byte {
		sites := []any{"Global/Lab"}
		if wlan == "WLAN2" {
			sites = append(sites, "Global/Lab2")
		}
		row := map[string]any{"name": "P", "sites": sites,
			"site_tags":    []any{map[string]any{"name": "ST", "sites": sites, "ap_profile_name": "AP"}},
			"policy_tags":  []any{map[string]any{"name": "PT", "sites": sites}},
			"ssid_details": []any{map[string]any{"name": "S", "wlan_profile_name": wlan}}}
		if variant == "vlan-selector" {
			row["ssid_details"].([]any)[0].(map[string]any)["vlan_group_name"] = "VG-" + wlan
		}
		if cohort {
			row["ap_zones"] = []any{map[string]any{"name": "Z1"}, map[string]any{"name": "Z2"}}
			row["policy_tags"] = []any{map[string]any{"name": "PT1", "sites": sites, "ap_zones": []string{"Z1"}}, map[string]any{"name": "PT2", "sites": sites, "ap_zones": []string{"Z2"}}}
		}
		if qualification != nil && qualification.receipts != nil && strings.HasPrefix(qualification.receipts.mode, "membership-zone-") {
			if !cohort {
				row["ap_zones"] = []any{map[string]any{"name": "Z1"}}
			}
			row["ap_zones"] = append(row["ap_zones"].([]any), map[string]any{"name": "Z3"})
			row["policy_tags"].([]any)[0].(map[string]any)["ap_zones"] = []string{"Z1", "Z3"}
		}
		return wirelessProfileTestModel(row)
	}
	config.Spec.Source.Inline.Raw = model("WLAN1")
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if qualification != nil {
		qualification.installFixture(t, a, config)
	}
	rows := []map[string]any{}

	writes := 0
	assignments := []map[string]string{{"id": "foreign-site"}}
	assignmentWrites := 0
	tagRows := map[string][]map[string]any{}
	if qualification != nil && qualification.receipts != nil && qualification.receipts.mode == "import" {
		qualification.receipts.seedImports(tagRows, cohort)
	}
	tagWrites := 0
	stamp := int64(0)
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if qualification != nil && qualification.receipts != nil && qualification.receipts.pruneRequest(t, a, w, r, tagRows, &assignments, qualification) {
			return
		}
		if qualification != nil && qualification.receipts != nil && qualification.receipts.membershipRequest(t, a, w, r) {
			return
		}
		if qualification != nil && qualification.receipts != nil && qualification.receipts.compositionDelete(t, a, w, r, &assignments, tagRows, qualification) {
			return
		}
		if qualification != nil && qualification.refuseRead(t, a, r) {
			w.WriteHeader(503)
			return
		}
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == wirelessAPPath:
			if qualification != nil && qualification.receipts != nil {
				fmt.Fprint(w, `{"response":[{"id":"ap-id","apProfileName":"AP"},{"id":"ap-other","apProfileName":"AP-OTHER"}]}`)
				return
			}
			fmt.Fprint(w, `{"response":[{"id":"ap-id","apProfileName":"AP"}]}`)
		case r.URL.Path == sitesPath && qualification != nil && qualification.receipts != nil && strings.HasPrefix(qualification.receipts.mode, "membership-"):
			json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}, {ID: "lab-site", Name: "Lab", NameHierarchy: "Global/Lab", Type: "area", ParentID: "global"}, {ID: "lab2-site", Name: "Lab2", NameHierarchy: "Global/Lab2", Type: "area", ParentID: "global"}, {ID: "child-site", Name: "Child", NameHierarchy: "Global/Lab2/Child", Type: "area", ParentID: "lab2-site"}}})
		case r.URL.Path == sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}, {ID: "lab-site", Name: "Lab", NameHierarchy: "Global/Lab", Type: "area", ParentID: "global"}, {ID: "lab2-site", Name: "Lab2", NameHierarchy: "Global/Lab2", Type: "area", ParentID: "global"}}})
		case r.URL.Path == wirelessSSIDPath("global"):
			fmt.Fprint(w, `{"response":[{"id":"ssid-id","ssid":"S","authType":"OPEN","wlanType":"Enterprise"}]}`)
		case r.URL.Path == wirelessInterfacesPath:
			fmt.Fprint(w, `{"response":[{"id":"interface-id","interfaceName":"management","vlanId":0}]}`)
		case r.Method == "GET" && r.URL.Path == wirelessProfilesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.Method == "POST" && r.URL.Path == wirelessProfilesPath || r.Method == "PUT" && r.URL.Path == wirelessProfilesPath+"/profile-id":
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid payload")
			}
			body["id"] = "profile-id"
			rows = []map[string]any{body}
			writes++
			stamp = time.Now().UnixMilli()
			if qualification != nil {
				qualification.respondTask(t, a, w, stamp)
				return
			}
			fmt.Fprint(w, `{"response":{"taskId":"task-id"}}`)
		case r.URL.Path == profileSiteAssignmentsPath("profile-id"):
			if len(rows) != 1 {
				t.Error("assignment before profile creation")
			}
			if r.Method == "GET" {
				json.NewEncoder(w).Encode(map[string]any{"response": assignments})
				return
			}
			if r.Method != "POST" {
				t.Error("non-additive assignment")
			}
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || (body["id"] != "lab-site" && body["id"] != "lab2-site") {
				t.Error("invalid assignment payload")
			}
			assignments = append(assignments, body)
			assignmentWrites++
			stamp = time.Now().UnixMilli()
			if qualification != nil {
				qualification.respondTask(t, a, w, stamp)
				return
			}
			fmt.Fprint(w, `{"response":{"taskId":"task-id"}}`)

		case strings.HasPrefix(r.URL.Path, wirelessProfilesPath+"/profile-id/siteTags") || strings.HasPrefix(r.URL.Path, wirelessProfilesPath+"/profile-id/policyTags"):
			kind := "siteTags"
			idKey := "siteTagId"
			if strings.Contains(r.URL.Path, "policyTags") {
				kind = "policyTags"
				idKey = "policyTagId"
			}
			if r.Method == "GET" {
				if qualification != nil && qualification.receipts != nil {
					qualification.receipts.compositionTagRead(t, a, tagRows, r)
				}
				rows := tagRows[kind]
				if rows == nil {
					rows = []map[string]any{}
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
				return
			}

			if len(rows) != 1 {
				t.Error("tag before profile")
			}
			nameKey := "siteTagName"
			if kind == "policyTags" {
				nameKey = "policyTagName"
			}
			items := []map[string]any{}
			switch r.Method {
			case "POST":
				recreating := qualification != nil && qualification.receipts != nil && qualification.receipts.recreating() && qualification.receipts.stage == 3
				if !strings.HasSuffix(r.URL.Path, "/bulk") || len(tagRows[kind]) != 0 || assignmentWrites != 1 && !recreating {
					t.Error("invalid create order")
				}
				var body struct {
					Items []map[string]any `json:"items"`
				}
				count := 1
				if cohort && kind == "policyTags" {
					count = 2
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Items) != count {
					t.Error("invalid cohort create payload")
					w.WriteHeader(400)
					return
				}
				items = body.Items
			case "PUT":
				var item map[string]any
				if json.NewDecoder(r.Body).Decode(&item) != nil {
					t.Error("invalid update")
					w.WriteHeader(400)
					return
				}
				found := false
				for _, prior := range tagRows[kind] {
					if prior[nameKey] == item[nameKey] {
						found = true
						if r.URL.Path != wirelessProfileTagPath("profile-id", kind == "policyTags")+"/"+prior[idKey].(string) {
							t.Error("replaced update identity")
						}
					}
				}
				if !found || assignmentWrites != 2 {
					t.Error("update without original cohort")
				}
				items = []map[string]any{item}
			default:
				t.Error("unexpected mutation")
				w.WriteHeader(405)
				return
			}
			for _, item := range items {
				ids, ok := item["siteIds"].([]any)
				expectedMembers := assignmentWrites
				if qualification != nil && qualification.receipts != nil && strings.HasPrefix(qualification.receipts.mode, "membership-") && qualification.receipts.stage == 2 {
					expectedMembers = 1
				}
				if qualification != nil && qualification.receipts != nil && qualification.receipts.repeatingRecreation() && (qualification.receipts.stage == 4 || qualification.receipts.recreateEpoch > 1) {
					expectedMembers = 1
				}
				if !ok || len(ids) != expectedMembers {
					t.Error("wrong site membership")
				}
				item[idKey] = kind + "-" + item[nameKey].(string) + "-id"
				if qualification != nil && qualification.receipts != nil && qualification.receipts.recreating() && qualification.receipts.stage == 3 && !qualification.receipts.stableRecreationIDs() {
					item[idKey] = fmt.Sprintf("%s-recreated-%d", item[idKey], qualification.receipts.recreateEpoch)
				}
				replaced := false
				for i, prior := range tagRows[kind] {
					if prior[nameKey] == item[nameKey] {
						// A PUT preserves its existing server identity in every epoch.
						item[idKey] = prior[idKey]
						tagRows[kind][i] = item
						replaced = true
					}
				}
				if !replaced {
					tagRows[kind] = append(tagRows[kind], item)
				}
			}

			if variant == "partial" && kind == "policyTags" {
				tagRows[kind] = tagRows[kind][:1]
			}
			tagWrites++
			if variant == "lost-ack" && kind == "policyTags" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			stamp = time.Now().UnixMilli()
			if qualification != nil {
				qualification.respondTask(t, a, w, stamp)
				return
			}
			fmt.Fprint(w, `{"response":{"taskId":"task-id"}}`)
		case qualification != nil && strings.HasPrefix(r.URL.Path, taskPath):
			qualification.respondObservation(t, a, w, r)
		case r.URL.Path == taskPath+"task-id":
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "task-id", "isError": false, "startTime": stamp, "endTime": stamp}})
		case r.URL.Path == taskPath+"task-id/tree":
			json.NewEncoder(w).Encode(map[string]any{"response": []map[string]any{{"id": "task-id", "isError": false, "startTime": stamp, "endTime": stamp}}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	accepted := 0
	acceptedTags := map[string]int{}
	tick := func() {
		t.Helper()
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		if op := journalState(t, a).Operation; op != nil && op.Phase == "Accepted" {
			accepted++
			if strings.Contains(op.Resource, "wireless-site-tags/") || strings.Contains(op.Resource, "wireless-policy-tags/") {
				acceptedTags[op.Action+":"+op.Resource]++
			}
		}
		if qualification != nil {
			total := writes + assignmentWrites + tagWrites
			if qualification.receipts != nil {
				total += qualification.receipts.tagDeletes
				if qualification.receipts.pruning() {
					total += qualification.receipts.deletes
				}
			}
			qualification.afterTick(t, a, config, rows, tagRows, total)
		}
	}
	if qualification != nil && qualification.receipts != nil {
		for range 120 {
			tick()
			if qualification.receipts.observe(t, a, config, tick, qualification, rows, tagRows, writes+assignmentWrites+tagWrites+qualification.receipts.tagDeletes+func() int {
				if qualification.receipts.pruning() {
					return qualification.receipts.deletes
				}
				return 0
			}(), tagWrites, model, cohort) {
				return
			}
		}
		t.Fatal("private tag qualification never settled", config.Status.Conditions)
	}
	if qualification != nil && qualification.failureExpected() {
		for range 60 {
			tick()
			if qualification.interrupted != nil {
				for range 5 {
					tick()
				}
				qualification.assertRetained(t, a, config, writes+assignmentWrites+tagWrites)
				return
			}
		}
		t.Fatal("selected profile/tag operation was never interrupted")
	}
	if variant == "partial" {
		for range 30 {
			tick()
			if op := journalState(t, a).Operation; op != nil && op.Phase == "Verifying" && strings.Contains(op.Resource, wirelessPolicyCohortPrefix) {
				break
			}
		}
		op := journalState(t, a).Operation
		if op == nil || op.Phase != "Verifying" || op.TaskID != "task-id" || !strings.Contains(op.Resource, wirelessPolicyCohortPrefix) || len(tagRows["policyTags"]) != 1 {
			t.Fatal("partial cohort did not retain its task for verification", op)
		}
		retained := op.DeepCopy()
		for range 3 {
			tick()
		}
		if !reflect.DeepEqual(retained, journalState(t, a).Operation) || tagWrites != 2 {
			t.Fatal("partial cohort journal advanced or request repeated")
		}
		tagRows["policyTags"] = append(tagRows["policyTags"], map[string]any{"policyTagName": "PT2", "policyTagId": "policyTags-PT2-id", "siteIds": []any{"lab-site"}, "apZones": []any{"Z2"}})
		for range 20 {
			tick()
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				break
			}
		}
		if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || journalState(t, a).Operation != nil || tagWrites != 2 {
			t.Fatal("complete delayed readback did not recover without replay", config.Status)
		}
		return
	}
	if variant == "lost-ack" {
		for range 25 {
			tick()
			if op := journalState(t, a).Operation; op != nil && op.Phase == "Unknown" {
				break
			}
		}
		op := journalState(t, a).Operation
		if op == nil || op.Phase != "Unknown" || op.TaskID != "" || !strings.Contains(op.Resource, wirelessPolicyCohortPrefix) || len(tagRows["policyTags"]) != 2 {
			t.Fatal("lost cohort acknowledgement not retained", op)
		}
		retained := op.DeepCopy()
		for range 3 {
			tick()
		}
		if !reflect.DeepEqual(retained, journalState(t, a).Operation) || tagWrites != 2 || writes != 1 {
			t.Fatal("unknown cohort advanced or replayed")
		}
		return
	}
	settle := func() {
		t.Helper()
		for range 40 {
			tick()
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				return
			}
		}
		t.Fatal("did not settle", config.Status)
	}
	settle()
	if qualification != nil {
		qualification.assertComplete(t, a)
	}
	tick()
	if writes != 1 || assignmentWrites != 1 || len(assignments) != 2 || accepted < 4 || tagWrites != 2 || journalState(t, a).Operation != nil {
		t.Fatal("create recovery failed")
	}
	config.Generation++
	config.Spec.Source.Inline.Raw = model("WLAN2")
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	settle()
	tick()
	expectedTags := 4
	if cohort {
		expectedTags = 5
	}
	if writes != 2 || assignmentWrites != 2 || accepted < 8 || tagWrites != expectedTags || len(acceptedTags) != expectedTags || journalState(t, a).Operation != nil {
		t.Fatal("update replayed or not accepted")
	}
	reports, err := a.client.compileConfigReports(model("WLAN2"), []string{"network_profiles"})
	if err != nil {
		t.Fatal(err)
	}
	drift, err := reports[0].observe(ctx)
	if err != nil || len(drift) != 0 || writes != 2 || tagWrites != expectedTags {
		t.Fatal("report changed state or reports drift", err)
	}
	if qualification != nil {
		qualification.assertComplete(t, a)
	}
}
func TestWirelessProfileCompilerIncludesTags(t *testing.T) {
	for _, key := range []string{"site_tags", "policy_tags"} {
		row := map[string]any{"name": "P", key: []any{map[string]any{"name": "T", "sites": []any{"Global/Lab"}}}}
		compiled, err := (&client{}).compileNetworkProfileConfiguration(wirelessProfileTestModel(row))
		if err != nil || compiled.tags.count() != 1 {
			t.Fatalf("%s missing: %v", key, err)
		}
	}
}

func TestWirelessProfileAssignmentRejectsReplacementBeforeSubmit(t *testing.T) {
	for _, replace := range []string{"site", "profile"} {
		t.Run(replace, func(t *testing.T) {
			profileID, siteID := "profile-id", "site-id"
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == wirelessProfilesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"id": profileID, "wirelessProfileName": "P"}}})
				case r.URL.Path == sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}, {ID: siteID, Name: "Lab", NameHierarchy: "Global/Lab", Type: "area", ParentID: "global"}}})
				case r.URL.Path == profileSiteAssignmentsPath(profileID):
					if r.Method == "GET" {
						fmt.Fprint(w, `{"response":[]}`)
					} else {
						writes++
						fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					}
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			e := &profileSiteExecutor{client: c, wireless: true, desired: []profileSiteIntent{{name: "P", sites: []string{"Global/Lab"}}}}
			write, err := e.next(context.Background())
			if err != nil || write == nil {
				t.Fatal("plan", err)
			}
			if replace == "site" {
				siteID = "replacement"
			} else {
				profileID = "replacement"
			}
			if _, err = write.submit(context.Background()); err == nil || writes != 0 {
				t.Fatal("replacement submitted")
			}
		})
	}
}

func TestWirelessProfileTagsFencePrerequisiteWrites(t *testing.T) {
	for _, scenario := range []string{"missing-site", "unassigned", "missing-zone", "custom-flex", "missing-ap-profile", "conflicting-tags", "changed-before-submit"} {
		t.Run(scenario, func(t *testing.T) {
			writes := 0
			sitePresent := true
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.Method != "GET":
					writes++
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
				case r.URL.Path == wirelessProfilesPath || r.URL.Path == wirelessSSIDPath("global"):
					fmt.Fprint(w, `{"response":[]}`)
				case r.URL.Path == wirelessAPPath:
					fmt.Fprint(w, `{"response":[{"id":"ap-id","apProfileName":"AP"}]}`)
				case r.URL.Path == sitesPath:
					sites := []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}}
					if sitePresent {
						sites = append(sites, Site{ID: "lab", Name: "Lab", NameHierarchy: "Global/Lab", Type: "area", ParentID: "global"})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": sites})
				default:
					t.Errorf("unexpected %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			tag := map[string]any{"name": "T", "sites": []any{"Global/Lab"}, "ap_profile_name": "AP"}
			row := map[string]any{"name": "P", "sites": []any{"Global/Lab"}, "site_tags": []any{tag}}
			switch scenario {
			case "missing-site":
				tag["sites"] = []any{"Global/Missing"}
			case "unassigned":
				delete(row, "sites")
			case "missing-ap-profile":
				delete(tag, "ap_profile_name")
			case "conflicting-tags":
				row["site_tags"] = []any{tag, map[string]any{"name": "Other", "sites": []any{"Global/Lab"}, "ap_profile_name": "AP"}}

			case "missing-zone":
				delete(tag, "ap_profile_name")
				delete(row, "site_tags")
				tag["ap_zones"] = []any{"Missing"}
				row["policy_tags"] = []any{tag}
			case "custom-flex":
				tag["flex_profile_name"] = "Custom"
			}
			e, err := c.compileNetworkProfileConfiguration(wirelessProfileTestModel(row))
			if err != nil {
				t.Fatal(err)
			}
			write, err := e.next(context.Background())
			if scenario == "changed-before-submit" {
				if err != nil || write == nil {
					t.Fatal("valid prerequisite blocked", err)
				}
				sitePresent = false
				_, err = write.submit(context.Background())
			} else if write != nil {
				t.Fatal("invalid tag released prerequisite")
			}
			if scenario == "missing-ap-profile" && err != errWirelessSiteTagAPProfile {
				t.Fatal("missing required AP profile not identified", err)
			}
			if scenario == "missing-ap-profile" || scenario == "conflicting-tags" {
				reason, fragment := "MissingSiteTagAPProfile", "ap_profile_name"
				if scenario == "conflicting-tags" {
					reason, fragment = "ConflictingWirelessTags", "overlap"
				}
				ctx := context.Background()
				a, config := journalFixture(t)
				a.client = c
				config.Spec.ManagedSections = []string{"network_profiles"}
				config.Spec.Source.Inline.Raw = wirelessProfileTestModel(row)
				if e := a.statusWriter.Update(ctx, config); e != nil {
					t.Fatal(e)
				}
				for range 2 {
					if _, e := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); e != nil {
						t.Fatal(e)
					}
				}
				if e := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); e != nil {
					t.Fatal(e)
				}
				found := false
				for _, condition := range config.Status.Conditions {
					if condition.Reason == reason && strings.Contains(condition.Message, fragment) {
						found = true
					}
				}
				if config.Status.Phase != configapi.NetworkControllerConfigPhaseFailed || !found || journalState(t, a).Operation != nil || writes != 0 {
					t.Fatal("missing AP reference did not stop before journal with actionable status", config.Status)
				}
			}
			if err == nil || writes != 0 {
				t.Fatal("invalid dependency did not fence writes", err, writes)
			}
		})
	}
}
