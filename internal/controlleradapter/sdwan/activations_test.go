// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestCentralizedActivationModel(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-centralized-activation.yaml")
	if err != nil {
		t.Fatal(err)
	}
	configured := string(raw)
	resources, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	last := resources[len(resources)-1]
	if last.Kind != "activation" || last.Name != "CVK-CENTRAL-POLICY" {
		t.Fatal("activation must follow its policy and definitions; last was", last.Kind, last.Key)
	}
	if _, err = Compile([]byte(strings.Replace(configured, "activated_policy: CVK-CENTRAL-POLICY", "activated_policy: UNDECLARED", 1))); err == nil {
		t.Fatal("undeclared activated policy accepted")
	}
}

func TestCentralizedActivationLifecycle(t *testing.T) {
	active := false
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, request *http.Request) {
		if auth(w, request) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/dataservice/template/policy/vsmart/":
			fmt.Fprint(w, `{"data":[{"policyId":"policy1","policyName":"fixture","policyType":"feature"}]}`)
		case "/dataservice/template/policy/vsmart/definition/policy1":
			fmt.Fprintf(w, `{"isPolicyActivated":%t}`, active)
		case "/dataservice/template/policy/vsmart/activate/policy1":
			if request.Method != "POST" || request.URL.Query().Get("confirm") != "true" {
				t.Error("invalid activation request")
			}
			calls++
			active = true
			fmt.Fprint(w, `{"id":"task1"}`)
		case "/dataservice/device/action/status/task1":
			fmt.Fprint(w, `{"summary":{"status":"done"},"validation":{"status":"success"},"data":[]}`)
		default:
			t.Errorf("unexpected activation path: %s", request.URL.Path)
		}
	})
	r := Resource{Key: "centralized_policies/activated_policy", Name: "fixture", Section: "centralized_policies", Kind: "activation", Type: "centralized-policy", Body: map[string]any{"activated": true}}
	ctx := context.Background()
	if observed, err := c.Observe(ctx, r); err != nil || observed != nil {
		t.Fatal("inactive policy observation", err)
	}
	id, err := c.Create(ctx, r)
	if err != nil || id != "policy1" || calls != 1 {
		t.Fatal("activation", id, err, calls)
	}
	observed, err := c.Observe(ctx, r)
	if err != nil || observed == nil {
		t.Fatal("active policy observation", err)
	}
	same, err := Equal(r, observed.Body)
	if err != nil || !same {
		t.Fatal("activation readback", err)
	}
}

func TestTopologyActivationModel(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/sdwan/topology-group-activation.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resources, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	last := resources[len(resources)-1]
	if len(resources) != 3 || last.Kind != "activation" || last.Type != "topology-group" {
		t.Fatal("topology activation must follow its group")
	}
	second := strings.Replace(string(raw), "  topology_groups:\n", "  topology_groups:\n    - name: CVK-TOPOLOGY-SECOND\n      topology_profile: CVK-TOPOLOGY-GROUP-PROFILE\n      activate: true\n", 1)
	if _, err := Compile([]byte(second)); err == nil {
		t.Fatal("multiple active topology groups accepted")
	}
}

func TestTopologyActivationLifecycle(t *testing.T) {
	active := false
	deploys := 0
	c := testClient(t, func(w http.ResponseWriter, request *http.Request) {
		if auth(w, request) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/dataservice/v1/topology-group":
			fmt.Fprint(w, `[{"id":"group1","name":"fixture"}]`)
		case "/dataservice/v1/topology-group/group1":
			fmt.Fprintf(w, `{"activeStatus":%t,"version":2}`, active)
		case "/dataservice/v1/topology-group/group1/device/deploy":
			if request.Method != "POST" {
				t.Error("invalid topology deployment method")
			}
			deploys++
			active = true
			fmt.Fprint(w, `{"parentTaskId":"task2"}`)
		case "/dataservice/device/action/status/task2":
			fmt.Fprint(w, `{"summary":{"status":"done"},"validation":{"status":"success"},"data":[]}`)
		default:
			t.Errorf("unexpected topology activation path: %s", request.URL.Path)
		}
	})
	r := Resource{Key: "topology_groups/activation/active", Name: "fixture", Section: "topology_groups", Kind: "activation", Type: "topology-group", Body: map[string]any{"activated": true}}
	ctx := context.Background()
	if observed, err := c.Observe(ctx, r); err != nil || observed != nil {
		t.Fatal("inactive topology observation", err)
	}
	id, err := c.Create(ctx, r)
	if err != nil || id != "group1" || deploys != 1 {
		t.Fatal("topology activation", id, err, deploys)
	}
	observed, err := c.Observe(ctx, r)
	if err != nil || observed == nil {
		t.Fatal("active topology observation", err)
	}
	same, err := Equal(r, observed.Body)
	if err != nil || !same {
		t.Fatal("topology activation readback", err)
	}
}
