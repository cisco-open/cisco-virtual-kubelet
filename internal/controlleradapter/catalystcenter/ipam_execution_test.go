// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"testing"
)

func TestIPAMExecutionConverges(t *testing.T) {
	pools := []ipPool{}
	reservations := []ipReservation{}
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		switch r.URL.Path {
		case sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
		case globalPoolsPath:
			if r.Method == "POST" {
				var p ipPool
				if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
					t.Error(err)
				}
				p.ID = "pool"
				pools = append(pools, p)
				writes++
				fmt.Fprint(w, `{"response":{"taskId":"pool-task"}}`)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"response": pools})
			}
		case sitePoolsPath:
			if r.Method == "POST" {
				var p ipReservation
				if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
					t.Error(err)
				}
				if p.SiteID != "area" || p.V4.GlobalPoolID != "pool" {
					t.Error("unresolved dependency")
				}
				p.ID = "reservation"
				reservations = append(reservations, p)
				writes++
				fmt.Fprint(w, `{"response":{"taskId":"reservation-task"}}`)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"response": reservations})
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	for step := 0; step < 3; step++ {
		executor, err := c.compileConfigurationExecutor([]byte(poolModel), "network_settings")
		if err != nil {
			t.Fatal(err)
		}
		write, err := executor.next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if step == 2 {
			if write != nil {
				t.Fatal("did not converge")
			}
			break
		}
		if write == nil {
			t.Fatal("missing write")
		}
		if _, err = write.submit(context.Background()); err != nil {
			t.Fatal(err)
		}
		ok, err := executor.verify(context.Background(), &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent})
		if err != nil || !ok {
			t.Fatalf("verified=%v err=%v", ok, err)
		}
	}
	if writes != 2 {
		t.Fatalf("writes=%d", writes)
	}
}
func TestIPAMPayloadPreservesUnspecified(t *testing.T) {
	want := fixturePools()[0].Space
	old := fixturePools()[0].Space
	want.DNS = nil
	want.DHCP = nil
	want.Gateway = nil
	body := ipamSpacePayload(want, old)
	if body["gatewayIpAddress"] != "192.0.2.1" {
		t.Fatal("gateway lost")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	json.Unmarshal(raw, &decoded)
	if decoded["dhcpServers"] == nil || decoded["dnsServers"] == nil {
		t.Fatal("lists became null")
	}
	if _, ok := body["globalPoolId"]; ok {
		t.Fatal("global pool parent emitted")
	}
}

func TestIPAMExecutionRejectsMovedReservation(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Fatal("mutation before preflight")
		}
		switch r.URL.Path {
		case globalPoolsPath:
			json.NewEncoder(w).Encode(map[string]any{"response": fixturePools()})
		case sitePoolsPath:
			rs := fixtureReservations()
			rs[0].SiteID = "other-site"
			json.NewEncoder(w).Encode(map[string]any{"response": rs})
		case sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
		default:
			w.WriteHeader(404)
		}
	})
	executor, err := c.compileConfigurationExecutor([]byte(poolModel), "network_settings")
	if err != nil {
		t.Fatal(err)
	}
	if write, err := executor.next(context.Background()); err == nil || write != nil {
		t.Fatal("reservation relocation was allowed")
	}
}

func TestIPAMExecutionUpdatesAndPinsIdentity(t *testing.T) {
	pools := fixturePools()
	reservations := fixtureReservations()
	dns := []string{"192.0.2.54"}
	pools[0].Space.DNS = &dns
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		switch r.URL.Path {
		case globalPoolsPath:
			json.NewEncoder(w).Encode(map[string]any{"response": pools})
		case sitePoolsPath:
			json.NewEncoder(w).Encode(map[string]any{"response": reservations})
		case sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
		case globalPoolsPath + "/pool":
			if r.Method != "PUT" {
				t.Error("expected update")
			}
			var p ipPool
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				t.Error(err)
			}
			p.ID = "pool"
			pools = []ipPool{p}
			writes++
			fmt.Fprint(w, `{"response":{"taskId":"update-task"}}`)
		default:
			t.Error("unexpected path")
			w.WriteHeader(404)
		}
	})
	executor, err := c.compileConfigurationExecutor([]byte(poolModel), "network_settings")
	if err != nil {
		t.Fatal(err)
	}
	write, err := executor.next(context.Background())
	if err != nil || write == nil {
		t.Fatalf("write=%v err=%v", write, err)
	}
	if write.action != "Update" || write.identity.target != "pool" {
		t.Fatal("update identity not pinned")
	}
	if _, err = write.submit(context.Background()); err != nil {
		t.Fatal(err)
	}
	op := &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target}
	if ok, err := executor.verify(context.Background(), op); err != nil || !ok {
		t.Fatalf("verified=%v err=%v", ok, err)
	}
	if next, err := executor.next(context.Background()); err != nil || next != nil {
		t.Fatal("update did not converge", err)
	}
	op.TargetID = "replaced"
	if ok, err := executor.verify(context.Background(), op); err == nil || ok {
		t.Fatal("replacement accepted")
	}
	if writes != 1 {
		t.Fatal("update replayed")
	}
}
