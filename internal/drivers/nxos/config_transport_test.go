// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package nxos

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
)

func TestParseNXOSConfigFetchOutputs(t *testing.T) {
	if got := parseNXOSHostname("hostname leaf-01\n"); got != "leaf-01" {
		t.Fatalf("hostname=%q", got)
	}
	if got := parseNXOSHostname("leaf-01\n"); got != "leaf-01" {
		t.Fatalf("show hostname output parsed as %q", got)
	}
	if got := parseNXOSHostname("{}"); got != "" {
		t.Fatalf("empty NX-API object parsed as hostname %q", got)
	}
	vlans := parseNXOSVLANBrief(`
VLAN Name                             Status    Ports
---- -------------------------------- --------- -------------------------------
1    default                          active    Eth1/1
101  cvk_probe                        active
102  cvk_unsup                        act/unsup Eth1/2, Eth1/3
                                             Eth1/4, Eth1/5
200  vlan with spaces                 active    Eth1/6
`)
	if len(vlans) != 4 || vlans[1]["id"] != 101 || vlans[1]["name"] != "cvk_probe" {
		t.Fatalf("vlans=%#v", vlans)
	}
	if vlans[2]["id"] != 102 || vlans[2]["status"] != "act/unsup" {
		t.Fatalf("NX-OS status token not parsed: %#v", vlans[2])
	}
	if vlans[3]["id"] != 200 || vlans[3]["name"] != "vlan with spaces" {
		t.Fatalf("wrapped/active-port VLAN output confused parser: %#v", vlans[3])
	}
	intfs := parseNXOSEthernetRunning(`
interface Ethernet1/1
  description uplink
  no shutdown
!
interface Ethernet1/2
  shutdown
`)
	if len(intfs) != 2 || intfs[0]["name"] != "1/1" || intfs[0]["description"] != "uplink" || intfs[1]["shutdown"] != true {
		t.Fatalf("interfaces=%#v", intfs)
	}
	if got := parseNXOSVersion("Cisco Nexus Operating System (NX-OS) Software\nNXOS: version 10.3(9)\n"); got != "10.3(9)" {
		t.Fatalf("version=%q", got)
	}
}

func TestNXAPIConfigTransportMutateCLI(t *testing.T) {
	var got nxapiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"ins_api":{"outputs":{"output":{"input":"ok","code":"200","msg":"Success","body":""}}}}`))
	}))
	defer server.Close()

	tr := &nxapiConfigTransport{client: &nxapiClient{baseURL: server.URL, client: server.Client()}}
	err := tr.Mutate(context.Background(), "", []transport.Op{
		{Verb: transport.VerbCLI, Body: []byte("hostname leaf-01")},
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if got.InsAPI.Type != "cli_conf" {
		t.Fatalf("type=%q", got.InsAPI.Type)
	}
	if got.InsAPI.Input != "configure terminal ; hostname leaf-01" {
		t.Fatalf("input=%q", got.InsAPI.Input)
	}
}

func TestNXAPIConfigTransportFetchesDMEObservedState(t *testing.T) {
	var loginCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/aaaLogin.json":
			loginCount++
			http.SetCookie(w, &http.Cookie{Name: "nxapi_auth", Value: "token"})
			_, _ = w.Write([]byte(`{"aaaLogin":{"attributes":{"token":"token"}}}`))
		case "/api/mo/sys.json":
			assertDMECookie(t, r)
			_, _ = w.Write([]byte(`{"totalCount":"1","imdata":[{"topSystem":{"attributes":{"name":"leaf-01"}}}]}`))
		case "/api/mo/sys/bd.json":
			assertDMECookie(t, r)
			if r.URL.Query().Get("target-subtree-class") != "l2BD" {
				t.Fatalf("query=%s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"totalCount":"1","imdata":[{"l2BD":{"attributes":{"fabEncap":"vlan-101","name":"cvk_probe"}}}]}`))
		case "/api/mo/sys/intf.json":
			assertDMECookie(t, r)
			if r.URL.Query().Get("target-subtree-class") != "l1PhysIf" {
				t.Fatalf("query=%s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"totalCount":"1","imdata":[{"l1PhysIf":{"attributes":{"id":"eth1/1","descr":"uplink","adminSt":"up","mtu":"9216"}}}]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	tr := &nxapiConfigTransport{client: &nxapiClient{
		rootURL:  server.URL,
		baseURL:  server.URL + "/ins",
		username: "admin",
		password: "pw",
		client:   server.Client(),
	}}

	raw, err := tr.Fetch(context.Background(), nxosschema.PathSystemHostname)
	if err != nil {
		t.Fatalf("Fetch(system): %v", err)
	}
	var system map[string]any
	if err := json.Unmarshal(raw, &system); err != nil {
		t.Fatalf("decode system: %v", err)
	}
	if system["hostname"] != "leaf-01" {
		t.Fatalf("system=%#v", system)
	}

	raw, err = tr.Fetch(context.Background(), nxosschema.PathVLANBrief)
	if err != nil {
		t.Fatalf("Fetch(vlan): %v", err)
	}
	var vlans map[string][]map[string]any
	if err := json.Unmarshal(raw, &vlans); err != nil {
		t.Fatalf("decode vlans: %v", err)
	}
	if len(vlans["vlans"]) != 1 || vlans["vlans"][0]["id"] != float64(101) || vlans["vlans"][0]["name"] != "cvk_probe" {
		t.Fatalf("vlans=%#v", vlans)
	}

	raw, err = tr.Fetch(context.Background(), nxosschema.PathInterfaceEthernet)
	if err != nil {
		t.Fatalf("Fetch(interface): %v", err)
	}
	var intfs map[string][]map[string]any
	if err := json.Unmarshal(raw, &intfs); err != nil {
		t.Fatalf("decode interfaces: %v", err)
	}
	if len(intfs["interfaces"]) != 1 ||
		intfs["interfaces"][0]["name"] != "1/1" ||
		intfs["interfaces"][0]["shutdown"] != false ||
		intfs["interfaces"][0]["mtu"] != float64(9216) {
		t.Fatalf("interfaces=%#v", intfs)
	}
	if loginCount != 1 {
		t.Fatalf("loginCount=%d, want 1", loginCount)
	}
}

func TestNXAPIConfigTransportMutateDME(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/aaaLogin.json":
			http.SetCookie(w, &http.Cookie{Name: "nxapi_auth", Value: "token"})
			_, _ = w.Write([]byte(`{"aaaLogin":{"attributes":{"token":"token"}}}`))
		case "/api/mo/sys.json":
			if r.Method != http.MethodPost {
				t.Fatalf("method=%s", r.Method)
			}
			assertDMECookie(t, r)
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			_, _ = w.Write([]byte(`{"imdata":[]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	tr := &nxapiConfigTransport{client: &nxapiClient{
		rootURL:  server.URL,
		baseURL:  server.URL + "/ins",
		username: "admin",
		password: "pw",
		client:   server.Client(),
	}}
	err := tr.Mutate(context.Background(), "", []transport.Op{
		{Verb: transport.VerbMerge, Path: nxosschema.DNSystem, Body: []byte(`{"topSystem":{"attributes":{"name":"leaf-01"}}}`)},
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	top, ok := got["topSystem"].(map[string]any)
	if !ok {
		t.Fatalf("got=%#v", got)
	}
	attrs, _ := top["attributes"].(map[string]any)
	if attrs["name"] != "leaf-01" {
		t.Fatalf("attrs=%#v", attrs)
	}
}

func TestNXAPIConfigTransportSaveStartup(t *testing.T) {
	var got nxapiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"ins_api":{"outputs":{"output":{"input":"copy running-config startup-config","code":"200","msg":"Success","body":""}}}}`))
	}))
	defer server.Close()

	tr := &nxapiConfigTransport{client: &nxapiClient{baseURL: server.URL, client: server.Client()}}
	if err := tr.SaveStartup(context.Background()); err != nil {
		t.Fatalf("SaveStartup: %v", err)
	}
	if got.InsAPI.Type != "cli_conf" {
		t.Fatalf("type=%q", got.InsAPI.Type)
	}
	if got.InsAPI.Input != "copy running-config startup-config" {
		t.Fatalf("input=%q", got.InsAPI.Input)
	}
}

func TestBuildNXOSConfigTransportSelection(t *testing.T) {
	spec := &ciskov1.DeviceSpec{Address: "192.0.2.10", Username: "admin", Password: "pw"}
	for _, transportName := range []string{"", "rest", "restconf", "nxapi"} {
		spec.Transport = transportName
		tr, err := buildNXOSConfigTransport(spec, drivers.ConfigDriverOptions{})
		if err != nil {
			t.Fatalf("transport %q: %v", transportName, err)
		}
		if tr.Capabilities().Kind != transport.KindREST {
			t.Fatalf("transport %q kind=%q", transportName, tr.Capabilities().Kind)
		}
		_ = tr.Close()
	}
	spec.Transport = "gnmi"
	if _, err := buildNXOSConfigTransport(spec, drivers.ConfigDriverOptions{}); err == nil {
		t.Fatal("expected gnmi to be rejected until NX-OS gNMI config transport is implemented")
	}
}

func assertDMECookie(t *testing.T, r *http.Request) {
	t.Helper()
	cookie, err := r.Cookie("nxapi_auth")
	if err != nil {
		t.Fatalf("missing nxapi_auth cookie: %v", err)
	}
	if cookie.Value != "token" {
		t.Fatalf("cookie=%q", cookie.Value)
	}
}
