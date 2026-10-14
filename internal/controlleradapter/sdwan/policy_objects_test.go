// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func policyFixtures(t *testing.T) []Resource {
	t.Helper()
	data, e := os.ReadFile("../../../examples/configs/sdwan/policy-objects.yaml")
	if e != nil {
		t.Fatal(e)
	}
	rs, e := Compile(data)
	if e != nil {
		t.Fatal(e)
	}
	return rs
}
func TestPolicyFixtures(t *testing.T) {
	rs := policyFixtures(t)
	if len(rs) != 24 {
		t.Fatalf("got %d resources", len(rs))
	}
	for _, r := range rs {
		t.Run(r.Key, func(t *testing.T) {
			same, e := Equal(r, r.Body)
			if e != nil || !same {
				t.Fatal(e)
			}
			if r.Section != "policy_objects" {
				t.Fatal("section")
			}
		})
	}
}
func TestPolicyRejectUnknownIntent(t *testing.T) {
	for _, doc := range []string{
		`{"sdwan":{"policy_objects":{"color_lists":[{"name":"x","colors":["blue"],"extra":1}]}}}`,
		`{"sdwan":{"policy_objects":{"ipv4_prefix_lists":[{"name":"x","entries":[{"prefix":"192.0.2.0/24","oops":1}]}]}}}`,
		`{"sdwan":{"policy_objects":{"color_lists":[{"name":"x","colors":[]}]}}}`,
	} {
		if _, e := Compile([]byte(doc)); e == nil {
			t.Fatal("unsupported intent accepted")
		}
	}
}
func TestLivePolicyNamesAbsent(t *testing.T) {
	c := liveClient(t)
	for _, r := range policyFixtures(t) {
		o, e := c.Observe(context.Background(), r)
		if e != nil || o != nil {
			t.Fatalf("%s not absent: %v", r.Key, e)
		}
	}
}
func TestLivePolicyLifecycle(t *testing.T) {
	if os.Getenv("CVK_SDWAN_MUTATE") != "1" {
		t.Skip("requires fixture mutation opt-in")
	}
	c := liveClient(t)
	ctx := context.Background()
	createHierarchyFixtures(t, c)
	for _, r := range policyFixtures(t) {
		t.Run(r.Key, func(t *testing.T) {
			o, e := c.Observe(ctx, r)
			if e != nil || o != nil {
				t.Fatal("fixture must be absent", e)
			}
			id, e := c.Create(ctx, r)
			if e != nil {
				t.Fatal("create", e)
			}
			t.Cleanup(func() {
				if e := c.DeleteResourceFixture(context.Background(), r, id); e != nil {
					t.Error("cleanup", e)
				}
			})
			o, e = waitPolicy(t, c, r)
			if e != nil || o == nil {
				var direct map[string]any
				err := c.Request(ctx, "GET", r.Collection+"/"+id, nil, &direct)
				t.Logf("direct read name=%v type=%v error=%v", direct["name"], direct["type"], err)
				t.Fatal("readback", e)
			}
			same, e := Equal(r, o.Body)
			if e != nil || !same {
				t.Fatal("create verification", e)
			}
			if e = c.Update(ctx, r, o); e != nil {
				t.Fatal("update", e)
			}
			o, e = waitPolicy(t, c, r)
			if e != nil || o == nil {
				t.Fatal("update readback", e)
			}
			same, e = Equal(r, o.Body)
			if e != nil || !same {
				t.Fatal("update verification", e)
			}
		})
	}
}

func waitPolicy(t *testing.T, c *Client, r Resource) (*Observation, error) {
	t.Helper()
	var o *Observation
	var e error
	for i := 0; i < 15; i++ {
		o, e = c.Observe(context.Background(), r)
		if o != nil || e != nil {
			return o, e
		}
		time.Sleep(time.Second)
	}
	return o, e
}

func TestPolicyReferenceValidation(t *testing.T) {
	if _, e := Compile([]byte(`{"sdwan":{"policy_objects":{"sla_classes":[{"name":"sla","app_probe_class":"missing"}]}}}`)); e == nil {
		t.Fatal("undeclared dependency accepted")
	}
}
func TestPolicyNativeMapping(t *testing.T) {
	data := []byte(`{"sdwan":{"policy_objects":{"site_lists":[{"name":"sites","site_ids":[100],"site_id_ranges":[{"from":200,"to":210}]}],"policers":[{"name":"rate","burst_bytes":100000,"rate_bps":1000000,"exceed_action":"drop"}]}}}`)
	rs, e := Compile(data)
	if e != nil {
		t.Fatal(e)
	}
	expected := []string{`{"entries":[{"burst":"100000","exceed":"drop","rate":"1000000"}],"name":"rate","type":"policer"}`, `{"entries":[{"siteId":"100"},{"siteId":"200-210"}],"name":"sites","type":"site"}`}
	for i, r := range rs {
		actual, _ := json.Marshal(r.Body)
		if string(actual) != expected[i] {
			t.Fatalf("mapping %s", actual)
		}
	}
}
