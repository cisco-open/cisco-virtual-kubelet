// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package topology

import (
	"strings"
	"testing"
)

func TestGraphPolicyMapsPeerIdentityAndMatchesDeclaredLink(t *testing.T) {
	document, err := ParseGraphPolicyDocument(`{
  "version":"v1",
  "peerMappings":[
    {"source":"cdp","observedPeer":"C9K-2","physicalID":"SERIAL-B"},
    {"source":"cdp","observedPeer":"C9K-1","physicalID":"SERIAL-A"}
  ],
  "declaredLinks":[
    {"local":"SERIAL-A","peer":"SERIAL-B","source":"cdp","interface":"Gi1","remoteInterface":"Gi2"},
    {"local":"SERIAL-B","peer":"SERIAL-A","source":"cdp","interface":"Gi2","remoteInterface":"Gi1"}
  ]
}`)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := BuildGraph([]GraphObservation{
		{PhysicalID: "serial-a", Complete: true, Neighbors: []GraphNeighbor{{PeerID: "C9K-2", Source: "cdp", Interface: "Gi1", RemoteInterface: "Gi2"}}},
		{PhysicalID: "serial-b", Complete: true, Neighbors: []GraphNeighbor{{PeerID: "C9K-1", Source: "cdp", Interface: "Gi2", RemoteInterface: "Gi1"}}},
	}, GraphPolicy{PeerMappings: document.PeerMappings, Declared: document.DeclaredLinks})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Complete || len(graph.Edges) != 2 {
		t.Fatalf("graph = %#v", graph)
	}
	if graph.Edges[0].Peer != "serial-b" || graph.Edges[0].ObservedPeer != "C9K-2" {
		t.Fatalf("mapped edge = %#v", graph.Edges[0])
	}
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "UnknownPeer" || diagnostic.Code == "DeclaredLinkMissing" || diagnostic.Code == "UnexpectedObservedLink" {
			t.Fatalf("unexpected diagnostic: %#v", diagnostic)
		}
	}
}

func TestGraphPolicyFailsClosedForAmbiguousOrUnavailableMappings(t *testing.T) {
	_, err := ParseGraphPolicyDocument(`{"version":"v1","peerMappings":[{"source":"cdp","observedPeer":"leaf","physicalID":"serial-a"},{"source":"cdp","observedPeer":"leaf","physicalID":"serial-b"}]}`)
	if err == nil || !strings.Contains(err.Error(), "duplicates mapping") {
		t.Fatalf("ambiguous mapping error = %v", err)
	}
	graph, err := BuildGraph([]GraphObservation{{PhysicalID: "serial-a", Complete: true}}, GraphPolicy{PeerMappings: []PeerIdentityMapping{{Source: "cdp", ObservedPeer: "leaf", PhysicalID: "serial-b"}}})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Complete {
		t.Fatalf("unavailable mapping produced complete graph: %#v", graph)
	}
	want := map[string]bool{"MappedPeerUnavailable": false, "UnusedPeerMapping": false}
	for _, diagnostic := range graph.Diagnostics {
		if _, ok := want[diagnostic.Code]; ok {
			want[diagnostic.Code] = true
		}
	}
	for code, found := range want {
		if !found {
			t.Fatalf("missing %s diagnostic: %#v", code, graph.Diagnostics)
		}
	}
}

func TestGraphPolicyCanDeclareObservedExternalPeer(t *testing.T) {
	graph, err := BuildGraph([]GraphObservation{{
		PhysicalID: "serial-a", Complete: true,
		Neighbors: []GraphNeighbor{{PeerID: "provider-edge", Source: "cdp", Interface: "Gi1", RemoteInterface: "Gi9"}},
	}}, GraphPolicy{PeerMappings: []PeerIdentityMapping{{
		Source: "cdp", ObservedPeer: "provider-edge", PhysicalID: "external:provider-edge", External: true,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Complete || len(graph.Nodes) != 2 || graph.Edges[0].Peer != "external:provider-edge" {
		t.Fatalf("external peer graph = %#v", graph)
	}
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "UnknownPeer" || diagnostic.Code == "MappedPeerUnavailable" || diagnostic.Code == "AsymmetricLink" {
			t.Fatalf("declared external peer produced diagnostic: %#v", diagnostic)
		}
	}
}

func TestGraphPolicyStrictBoundsAndCanonicalHash(t *testing.T) {
	left, err := ParseGraphPolicyDocument(`{"version":"v1","peerMappings":[{"source":"cdp","observedPeer":"B","physicalID":"SERIAL-B"},{"source":"cdp","observedPeer":"A","physicalID":"SERIAL-A"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	right, err := ParseGraphPolicyDocument(`{"peerMappings":[{"physicalID":"serial-a","observedPeer":"A","source":"cdp"},{"physicalID":"serial-b","observedPeer":"B","source":"cdp"}],"version":"v1"}`)
	if err != nil {
		t.Fatal(err)
	}
	leftHash, _ := GraphPolicyHash(left)
	rightHash, _ := GraphPolicyHash(right)
	if leftHash != rightHash {
		t.Fatalf("canonical hashes differ: %s != %s", leftHash, rightHash)
	}
	bad := []string{
		`{"version":"v1","unknown":true}`,
		`{"version":"v2"}`,
		`{"version":"v1","declaredLinks":[{"local":"serial-a","peer":"serial-b","interface":"Gi1"}]}`,
		`{"version":"v1"} {}`,
	}
	for _, raw := range bad {
		if _, err := ParseGraphPolicyDocument(raw); err == nil {
			t.Fatalf("invalid graph policy accepted: %s", raw)
		}
	}
}

func TestGraphPolicyEnforcesMappingDeclarationAndDiagnosticBounds(t *testing.T) {
	mappings := []PeerIdentityMapping{
		{Source: "cdp", ObservedPeer: "a", PhysicalID: "serial-a"},
		{Source: "cdp", ObservedPeer: "b", PhysicalID: "serial-b"},
	}
	if _, err := BuildGraph([]GraphObservation{{PhysicalID: "serial-a", Complete: true}}, GraphPolicy{PeerMappings: mappings, MaxPeerMappings: 1}); err == nil || !strings.Contains(err.Error(), "peer mappings") {
		t.Fatalf("mapping bound error = %v", err)
	}
	if _, err := BuildGraph([]GraphObservation{{PhysicalID: "serial-a", Complete: true}}, GraphPolicy{
		MaxNodes: 1, PeerMappings: []PeerIdentityMapping{{Source: "cdp", ObservedPeer: "outside", PhysicalID: "external:outside", External: true}},
	}); err == nil || !strings.Contains(err.Error(), "external mappings") {
		t.Fatalf("external node bound error = %v", err)
	}
	declared := []DeclaredLink{
		{Local: "serial-a", Peer: "serial-b", Interface: "Gi1", RemoteInterface: "Gi2"},
		{Local: "serial-b", Peer: "serial-a", Interface: "Gi2", RemoteInterface: "Gi1"},
	}
	if _, err := BuildGraph([]GraphObservation{{PhysicalID: "serial-a", Complete: true}}, GraphPolicy{Declared: declared, MaxDeclaredLinks: 1}); err == nil || !strings.Contains(err.Error(), "declared links") {
		t.Fatalf("declaration bound error = %v", err)
	}
	neighbors := []GraphNeighbor{
		{PeerID: "unknown-a", Source: "cdp", Interface: "Gi1", RemoteInterface: "Gi2"},
		{PeerID: "unknown-b", Source: "cdp", Interface: "Gi3", RemoteInterface: "Gi4"},
	}
	if _, err := BuildGraph([]GraphObservation{{PhysicalID: "serial-a", Complete: true, Neighbors: neighbors}}, GraphPolicy{MaxDiagnostics: 1}); err == nil || !strings.Contains(err.Error(), "diagnostics") {
		t.Fatalf("diagnostic bound error = %v", err)
	}
	if _, err := BuildGraph([]GraphObservation{{PhysicalID: "serial-a", Complete: true}}, GraphPolicy{PeerMappings: []PeerIdentityMapping{{
		Source: strings.Repeat("s", MaxGraphFieldLength+1), ObservedPeer: "peer", PhysicalID: "serial-b",
	}}}); err == nil || !strings.Contains(err.Error(), "field exceeds") {
		t.Fatalf("mapping field bound error = %v", err)
	}
}
