// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestCustomApplicationMappingAndValidation(t *testing.T) {
	r := parcelFixtures(t, "custom-applications")[0]
	if r.Kind != "custom-app" || r.Section != "application_catalog" || r.Body["appName"] != "CVK-CUSTOM-APP" {
		t.Fatal("custom application identity mapping", r)
	}
	attributes := r.Body["attributes"].(map[string]any)
	if attributes["business-relevance"] != "default" {
		t.Fatal("Silver business relevance mapping", attributes)
	}
	for _, bad := range []string{
		`{"sdwan":{"application_catalog":{"custom_applications":[{"name":"bad","business_relevance":"Platinum"}]}}}`,
		`{"sdwan":{"application_catalog":{"custom_applications":[{"name":"bad","l3l4":[{"ip_addresses":["2001:db8::1"]}]}]}}}`,
		`{"sdwan":{"application_catalog":{"custom_applications":[{"name":"bad","unknown":true}]}}}`,
	} {
		if _, err := Compile([]byte(bad)); err == nil {
			t.Fatal("invalid custom application accepted", bad)
		}
	}
}

func TestLiveCustomApplicationLifecycle(t *testing.T) {
	if os.Getenv("CVK_SDWAN_MUTATE") != "1" {
		t.Skip("requires fixture mutation opt-in")
	}
	r := parcelFixtures(t, "custom-applications")[0]
	c := liveClient(t)
	ctx := context.Background()
	o, err := c.Observe(ctx, r)
	if err != nil || o != nil {
		t.Fatal("fixture name must be absent", err)
	}
	t.Cleanup(func() {
		observed, err := c.Observe(ctx, r)
		if err != nil {
			t.Error("cleanup observe", err)
			return
		}
		if observed != nil {
			if err := c.DeleteResourceFixture(ctx, r, observed.ID); err != nil {
				t.Error("cleanup delete", err)
			}
		}
	})
	id, err := c.Create(ctx, r)
	if err != nil {
		t.Fatal("create", err)
	}
	if !validID.MatchString(id) {
		t.Fatal("invalid create ID")
	}
	for i := 0; i < 15; i++ {
		o, err = c.Observe(ctx, r)
		if err != nil || o != nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil || o == nil {
		t.Fatal("readback", err)
	}
	same, err := Equal(r, o.Body)
	if err != nil || !same {
		t.Fatal("create verification", err)
	}
	r.Body["serverNames"] = []string{"cvk-fixture.example.net", "cvk-updated.example.net"}
	if err := c.Update(ctx, r, o); err != nil {
		t.Fatal("update", err)
	}
	o, err = c.Observe(ctx, r)
	if err != nil || o == nil {
		t.Fatal("updated readback", err)
	}
	same, err = Equal(r, o.Body)
	if err != nil || !same {
		t.Fatal("update verification", err)
	}
}

func TestCustomApplicationAsyncLifecycle(t *testing.T) {
	r := parcelFixtures(t, "custom-applications")[0]
	stored := map[string]any(nil)
	creates := 0
	c := testClient(t, func(w http.ResponseWriter, request *http.Request) {
		if auth(w, request) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/dataservice/template/policy/customapp/":
			switch request.Method {
			case "GET":
				if stored == nil {
					fmt.Fprint(w, `{"data":[]}`)
				} else {
					fmt.Fprint(w, `{"data":[{"appId":"app1","appName":"CVK-CUSTOM-APP"}]}`)
				}
			case "POST":
				creates++
				if json.NewDecoder(request.Body).Decode(&stored) != nil {
					t.Error("invalid POST body")
				}
				fmt.Fprint(w, `{"taskId":"task1"}`)
			default:
				t.Errorf("unexpected method %s", request.Method)
			}
		case "/dataservice/device/action/status/task1":
			fmt.Fprint(w, `{"summary":{"status":"done"},"validation":{"status":"success"},"data":[]}`)
		case "/dataservice/template/policy/customapp/app1":
			switch request.Method {
			case "GET":
				_ = json.NewEncoder(w).Encode(map[string]any{"data": stored})
			case "PUT":
				if json.NewDecoder(request.Body).Decode(&stored) != nil {
					t.Error("invalid PUT body")
				}
				fmt.Fprint(w, `{}`)
			case "DELETE":
				stored = nil
				fmt.Fprint(w, `{}`)
			default:
				t.Errorf("unexpected method %s", request.Method)
			}
		default:
			t.Errorf("unexpected path %s", request.URL.Path)
		}
	})
	ctx := context.Background()
	id, err := c.Create(ctx, r)
	if err != nil || id != "app1" || creates != 1 {
		t.Fatal("async create", id, err, creates)
	}
	o, err := c.Observe(ctx, r)
	if err != nil || o == nil {
		t.Fatal("observe", err)
	}
	same, err := Equal(r, o.Body)
	if err != nil || !same {
		t.Fatal("readback mismatch", err)
	}
	r.Body["serverNames"] = []string{"cvk-updated.example.net"}
	if err := c.Update(ctx, r, o); err != nil {
		t.Fatal("update", err)
	}
	o, err = c.Observe(ctx, r)
	if err != nil || o == nil {
		t.Fatal("updated observe", err)
	}
	same, err = Equal(r, o.Body)
	if err != nil || !same {
		t.Fatal("updated mismatch", err)
	}
	if err := c.DeleteResourceFixture(ctx, r, id); err != nil {
		t.Fatal("delete", err)
	}
	o, err = c.Observe(ctx, r)
	if err != nil || o != nil {
		t.Fatal("deleted object remains", err)
	}
}
