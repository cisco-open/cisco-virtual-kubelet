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
`)
	if len(vlans) != 2 || vlans[1]["id"] != 101 || vlans[1]["name"] != "cvk_probe" {
		t.Fatalf("vlans=%#v", vlans)
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
	for _, transportName := range []string{"", "restconf", "nxapi"} {
		spec.Transport = transportName
		tr, err := buildNXOSConfigTransport(spec, drivers.ConfigDriverOptions{})
		if err != nil {
			t.Fatalf("transport %q: %v", transportName, err)
		}
		if tr.Capabilities().Kind != transport.KindNXAPI {
			t.Fatalf("transport %q kind=%q", transportName, tr.Capabilities().Kind)
		}
		_ = tr.Close()
	}
	spec.Transport = "gnmi"
	if _, err := buildNXOSConfigTransport(spec, drivers.ConfigDriverOptions{}); err == nil {
		t.Fatal("expected gnmi to be rejected until NX-OS gNMI config transport is implemented")
	}
}
