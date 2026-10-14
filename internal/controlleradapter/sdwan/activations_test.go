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
