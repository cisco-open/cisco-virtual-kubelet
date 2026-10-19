// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"sync"
	"testing"
)

type wirelessPolicyCohortFixture struct {
	rows  []map[string]any
	posts int
	mode  string
}

func newWirelessPolicyCohortFixture(t *testing.T, mode string) (*wirelessProfileTagExecutor, *wirelessPolicyCohortFixture) {
	t.Helper()
	state := &wirelessPolicyCohortFixture{mode: mode, rows: []map[string]any{}}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.Method == "POST" && r.URL.Path == wirelessProfileTagPath("profile", true)+"/bulk":
			state.posts++
			var body struct {
				Items []map[string]any `json:"items"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Items) != 2 {
				t.Error("not a two-tag cohort")
				w.WriteHeader(400)
				return
			}
			for _, item := range body.Items {
				item["policyTagId"] = "id-" + item["policyTagName"].(string)
				state.rows = append(state.rows, item)
				if mode == "partial" {
					break
				}
			}
			switch mode {
			case "lost-ack":
				w.WriteHeader(503)
			case "malformed":
				fmt.Fprint(w, `{"response":{}}`)
			case "202-empty":
				w.WriteHeader(202)
				fmt.Fprint(w, `{"response":{}}`)
			case "202":
				w.WriteHeader(202)
				fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
			default:
				fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
			}
		case r.Method != http.MethodGet:
			t.Error("unexpected native mutation", r.Method, r.URL.Path)
			w.WriteHeader(500)
		case r.URL.Path == wirelessSSIDPath("global"):
			fmt.Fprint(w, `{"response":[]}`)
		case r.URL.Path == wirelessProfilesPath:
			fmt.Fprint(w, `{"response":[{"id":"profile","wirelessProfileName":"P","apZones":[{"apZoneName":"Z1","ssids":[]},{"apZoneName":"Z2","ssids":[]},{"apZoneName":"Third","ssids":[]}]}]}`)
		case r.URL.Path == sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}, {ID: "a", Name: "A", NameHierarchy: "Global/A", ParentID: "global", Type: "area"}, {ID: "b", Name: "B", NameHierarchy: "Global/B", ParentID: "global", Type: "area"}}})
		case r.URL.Path == profileSiteAssignmentsPath("profile"):
			fmt.Fprint(w, `{"response":[{"id":"a"},{"id":"b"}]}`)
		case r.Method == "GET" && r.URL.Path == wirelessProfileTagPath("profile", true):
			json.NewEncoder(w).Encode(map[string]any{"response": state.rows})
		default:
			t.Error("unexpected cohort request", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	tags, err := parseWirelessProfileTags([]any{map[string]any{"name": "T1", "sites": []any{"Global/A"}, "ap_zones": []any{"Z1"}}, map[string]any{"name": "T2", "sites": []any{"Global/A"}, "ap_zones": []any{"Z2"}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	return &wirelessProfileTagExecutor{client: c, desired: []wirelessProfileIntent{{name: "P", policyTags: tags}}, policy: true}, state
}
func TestWirelessPolicyCohortCreateRecovery(t *testing.T) {
	for _, mode := range []string{"complete", "partial", "source-change", "foreign-native", "lost-ack", "malformed", "202", "202-empty"} {
		t.Run(mode, func(t *testing.T) {
			e, state := newWirelessPolicyCohortFixture(t, mode)
			ctx := context.Background()
			write, err := e.next(ctx)
			if err != nil || write == nil || !strings.HasPrefix(write.resource, wirelessPolicyCohortPrefix) {
				t.Fatal("missing cohort plan", err)
			}
			switch mode {
			case "source-change":
				e.desired[0].policyTags[0].sites = []string{"Global/B"}
			case "foreign-native":
				state.rows = append(state.rows, map[string]any{"policyTagId": "foreign", "policyTagName": "Foreign", "siteIds": []string{"a"}, "apZones": []string{"Third"}})
			}
			task, err := write.submit(ctx)
			good := mode == "complete" || mode == "partial" || mode == "202"
			if (err == nil) != good {
				t.Fatal("wrong submit outcome", err)
			}
			posts := 1
			if mode == "source-change" || mode == "foreign-native" {
				posts = 0
			}
			if state.posts != posts {
				t.Fatal("unexpected request count", state.posts)
			}
			if _, err := write.submit(ctx); err == nil || state.posts != posts {
				t.Fatal("one-use cohort replayed")
			}
			if !good {
				return
			}
			op := &cvk.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, ParentID: write.identity.parent, TaskID: task.ID}
			verified, err := e.verify(ctx, op)
			if mode == "partial" {
				if verified {
					t.Fatal("partial cohort verified")
				}
				if next, err := e.next(ctx); err == nil || next != nil || state.posts != 1 {
					t.Fatal("partial cohort released another create", err)
				}
				return
			}
			if err != nil || !verified {
				t.Fatal("complete cohort did not verify", err)
			}
			if next, err := e.next(ctx); err != nil || next != nil {
				t.Fatal("complete cohort did not converge", err)
			}
			op.Resource += "changed"
			if ok, err := e.verify(ctx, op); ok || err == nil {
				t.Fatal("changed retained binding verified")
			}
		})
	}
}

func TestWirelessPolicyCohortConcurrentSubmission(t *testing.T) {
	e, state := newWirelessPolicyCohortFixture(t, "complete")
	ctx := context.Background()
	write, err := e.next(ctx)
	if err != nil || write == nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() { defer group.Done(); <-start; _, err := write.submit(ctx); results <- err }()
	}
	close(start)
	group.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 || state.posts != 1 {
		t.Fatal("concurrent consumers did not produce exactly one cohort request", successes, state.posts)
	}
}

func TestWirelessPolicyCohortMixedManifestExplainsNativeLimit(t *testing.T) {
	ctx := context.Background()
	e, state := newWirelessPolicyCohortFixture(t, "complete")
	state.rows = []map[string]any{{"policyTagId": "existing", "policyTagName": "T1", "siteIds": []string{"a"}, "apZones": []string{"Z1"}}}
	a, config := journalFixture(t)
	a.client = e.client
	config.Spec.ManagedSections = []string{"network_profiles"}
	config.Spec.Source.Inline.Raw = wirelessProfileTestModel(map[string]any{"name": "P", "policy_tags": []any{map[string]any{"name": "T1", "sites": []string{"Global/A"}, "ap_zones": []string{"Z1"}}, map[string]any{"name": "T2", "sites": []string{"Global/A"}, "ap_zones": []string{"Z2"}}}})
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, condition := range config.Status.Conditions {
		if condition.Reason == "PolicyTagCohortRequired" && strings.Contains(condition.Message, "existing names") && strings.Contains(condition.Message, "unassigned") {
			found = true
		}
	}
	if !found || config.Status.Phase != configapi.NetworkControllerConfigPhaseFailed || state.posts != 0 || len(state.rows) != 1 || state.rows[0]["policyTagId"] != "existing" || journalState(t, a).Operation != nil {
		t.Fatal("native limitation was not explained without changing existing cohort", config.Status)
	}
}
