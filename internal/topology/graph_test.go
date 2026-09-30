// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");

package topology

import (
	"strings"
	"testing"
)

func TestBuildGraphIsBoundedDeterministicAndReportsDrift(t *testing.T) {
	observations := []GraphObservation{
		{PhysicalID: "leaf-b", Complete: true, Neighbors: []GraphNeighbor{{Identity: "ospf|leaf-b|Gi1|0", PeerID: "leaf-a", Interface: "Gi1", RoutingDomain: "0", State: "FULL"}}},
		{PhysicalID: "leaf-a", Complete: true, Neighbors: []GraphNeighbor{{Identity: "cdp|leaf-a|Gi1|", PeerID: "leaf-b", Interface: "Gi1", State: "discovered"}}},
	}
	graph, err := BuildGraph(observations, GraphPolicy{Declared: []DeclaredLink{
		{Local: "leaf-a", Peer: "leaf-b", Interface: "Gi1"},
		{Local: "leaf-b", Peer: "leaf-a", Interface: "Gi1", RoutingDomain: "0"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Complete || len(graph.Nodes) != 2 || len(graph.Edges) != 2 || !strings.HasPrefix(graph.EvidenceHash, "sha256:") {
		t.Fatalf("graph=%#v", graph)
	}
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "DeclaredLinkMissing" || diagnostic.Code == "UnexpectedObservedLink" {
			t.Fatalf("unexpected drift diagnostic: %#v", diagnostic)
		}
	}
	second, err := BuildGraph(observations, GraphPolicy{Declared: []DeclaredLink{
		{Local: "leaf-a", Peer: "leaf-b", Interface: "Gi1"},
		{Local: "leaf-b", Peer: "leaf-a", Interface: "Gi1", RoutingDomain: "0"},
	}})
	if err != nil || second.EvidenceHash != graph.EvidenceHash {
		t.Fatalf("graph is not deterministic: first=%#v second=%#v err=%v", graph, second, err)
	}
	changed := observations
	changed[0].Neighbors[0].State = "DOWN"
	third, err := BuildGraph(changed, GraphPolicy{Declared: []DeclaredLink{
		{Local: "leaf-a", Peer: "leaf-b", Interface: "Gi1"},
		{Local: "leaf-b", Peer: "leaf-a", Interface: "Gi1", RoutingDomain: "0"},
	}})
	if err != nil || third.EvidenceHash == graph.EvidenceHash {
		t.Fatalf("graph hash did not include edge state: first=%s third=%s err=%v", graph.EvidenceHash, third.EvidenceHash, err)
	}
}

func TestBuildGraphFailsClosedForUnknownAndAsymmetricPeers(t *testing.T) {
	graph, err := BuildGraph([]GraphObservation{{
		PhysicalID: "leaf-a", Complete: false, UnknownReason: "ospf unavailable",
		Neighbors: []GraphNeighbor{{PeerID: "leaf-b", Interface: "Gi1", State: "FULL"}},
	}}, GraphPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Complete {
		t.Fatalf("incomplete graph was marked complete: %#v", graph)
	}
	want := map[string]bool{"IncompleteObservation": false, "UnknownPeer": false}
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

func TestBuildGraphRejectsLimitsAndDuplicateDeviceIdentity(t *testing.T) {
	if _, err := BuildGraph([]GraphObservation{{PhysicalID: "leaf-a"}, {PhysicalID: "leaf-b"}}, GraphPolicy{MaxNodes: 1}); err == nil {
		t.Fatal("node limit was not enforced")
	}
	graph, err := BuildGraph([]GraphObservation{{PhysicalID: "leaf-a"}, {PhysicalID: "leaf-a"}}, GraphPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Complete || len(graph.Diagnostics) == 0 || graph.Diagnostics[0].Code != "DuplicateDeviceIdentity" {
		t.Fatalf("duplicate identity was not diagnosed: %#v", graph)
	}
}
