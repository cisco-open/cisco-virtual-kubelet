// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");

package topology

import (
	"strings"
	"testing"
	"time"
)

func TestBuildGraphIsBoundedDeterministicAndReportsDrift(t *testing.T) {
	observations := []GraphObservation{
		{PhysicalID: "leaf-b", Complete: true, Neighbors: []GraphNeighbor{{Identity: "ospf|leaf-b|Gi1|0", PeerID: "leaf-a", Interface: "Gi1", RemoteInterface: "Gi1", RoutingDomain: "0", State: "FULL"}}},
		{PhysicalID: "leaf-a", Complete: true, Neighbors: []GraphNeighbor{{Identity: "cdp|leaf-a|Gi1|", PeerID: "leaf-b", Interface: "Gi1", RemoteInterface: "Gi1", State: "discovered"}}},
	}
	graph, err := BuildGraph(observations, GraphPolicy{Declared: []DeclaredLink{
		{Local: "leaf-a", Peer: "leaf-b", Interface: "Gi1", RemoteInterface: "Gi1"},
		{Local: "leaf-b", Peer: "leaf-a", Interface: "Gi1", RemoteInterface: "Gi1", RoutingDomain: "0"},
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
		{Local: "leaf-a", Peer: "leaf-b", Interface: "Gi1", RemoteInterface: "Gi1"},
		{Local: "leaf-b", Peer: "leaf-a", Interface: "Gi1", RemoteInterface: "Gi1", RoutingDomain: "0"},
	}})
	if err != nil || second.EvidenceHash != graph.EvidenceHash {
		t.Fatalf("graph is not deterministic: first=%#v second=%#v err=%v", graph, second, err)
	}
	changed := observations
	changed[0].Neighbors[0].State = "DOWN"
	third, err := BuildGraph(changed, GraphPolicy{Declared: []DeclaredLink{
		{Local: "leaf-a", Peer: "leaf-b", Interface: "Gi1", RemoteInterface: "Gi1"},
		{Local: "leaf-b", Peer: "leaf-a", Interface: "Gi1", RemoteInterface: "Gi1", RoutingDomain: "0"},
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

func TestBuildGraphDiagnosesMissingObservationTime(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	graph, err := BuildGraph([]GraphObservation{{
		PhysicalID:    "leaf-a",
		Complete:      false,
		UnknownReason: "manager has not accepted a network observation",
	}}, GraphPolicy{Now: now, MaxObservationAge: 5 * time.Minute})
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}
	if graph.Complete {
		t.Fatalf("graph with missing observation time was marked complete: %#v", graph)
	}
	want := map[string]bool{"ObservationTimeMissing": false, "IncompleteObservation": false}
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

func TestBuildGraphCanonicalizesConflictsAndFreshness(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	first := GraphObservation{
		PhysicalID: "leaf-a", ObservedAt: now.Add(-time.Second), Complete: true,
		Neighbors: []GraphNeighbor{{Identity: "cdp|peer|Gi1|", PeerID: "leaf-b", Source: "cdp", Interface: "Gi1", State: "up"}},
	}
	conflict := first
	conflict.Neighbors = []GraphNeighbor{{Identity: "cdp|peer|Gi1|", PeerID: "leaf-b", Source: "cdp", Interface: "Gi1", State: "down"}}
	left, err := BuildGraph([]GraphObservation{first, conflict}, GraphPolicy{Now: now, MaxObservationAge: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	right, err := BuildGraph([]GraphObservation{conflict, first}, GraphPolicy{Now: now, MaxObservationAge: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if left.Complete || left.EvidenceHash != right.EvidenceHash || len(left.Edges) != 1 || left.Diagnostics[0].Code != "DuplicateAdjacency" {
		t.Fatalf("conflict was not deterministic/fail-closed: left=%#v right=%#v", left, right)
	}
	stale, err := BuildGraph([]GraphObservation{{PhysicalID: "leaf-a", ObservedAt: now.Add(-2 * time.Minute), Complete: true}}, GraphPolicy{Now: now, MaxObservationAge: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if stale.Complete || len(stale.Diagnostics) == 0 || stale.Diagnostics[0].Code != "StaleObservation" {
		t.Fatalf("stale observation was not rejected: %#v", stale)
	}
}

func TestBuildGraphReportsKnownPeerAsymmetryAndInputLimits(t *testing.T) {
	graph, err := BuildGraph([]GraphObservation{
		{PhysicalID: "leaf-a", Complete: true, Neighbors: []GraphNeighbor{{PeerID: "leaf-b", Source: "ospf", Interface: "Gi1", RoutingDomain: "0"}}},
		{PhysicalID: "leaf-b", Complete: true},
	}, GraphPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "AsymmetricLink" || diagnostic.Code == "ReverseIdentityInsufficient" {
			found = true
		}
	}
	if !found {
		t.Fatalf("known-peer asymmetry was not reported: %#v", graph.Diagnostics)
	}
	if _, err := BuildGraph([]GraphObservation{{PhysicalID: "leaf-a", Neighbors: make([]GraphNeighbor, 2)}}, GraphPolicy{MaxInputNeighbors: 1}); err == nil {
		t.Fatal("input neighbor limit was not enforced")
	}
}

func TestBuildGraphHashSeparatesDelimiterBearingFields(t *testing.T) {
	left, err := BuildGraph([]GraphObservation{{
		PhysicalID: "leaf-a", Complete: true,
		Neighbors: []GraphNeighbor{{PeerID: "leaf-b|leaf-c", Source: "cdp", Interface: "Gi1", State: "up"}},
	}}, GraphPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	right, err := BuildGraph([]GraphObservation{{
		PhysicalID: "leaf-a", Complete: true,
		Neighbors: []GraphNeighbor{{PeerID: "leaf-b", Source: "cdp", Interface: "Gi1|leaf-c", State: "up"}},
	}}, GraphPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if left.EvidenceHash == right.EvidenceHash {
		t.Fatalf("delimiter-bearing fields collided: %s", left.EvidenceHash)
	}
}

func TestBuildGraphUsesRemoteInterfaceForReverseIdentity(t *testing.T) {
	graph, err := BuildGraph([]GraphObservation{
		{PhysicalID: "leaf-a", Complete: true, Neighbors: []GraphNeighbor{{PeerID: "leaf-b", Source: "cdp", Interface: "Gi1", RemoteInterface: "Gi2"}}},
		{PhysicalID: "leaf-b", Complete: true, Neighbors: []GraphNeighbor{{PeerID: "leaf-a", Source: "cdp", Interface: "Gi2", RemoteInterface: "Gi1"}}},
	}, GraphPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "AsymmetricLink" || diagnostic.Code == "ReverseIdentityInsufficient" {
			t.Fatalf("valid port-reversed adjacency was not recognized: %#v", graph.Diagnostics)
		}
	}
}

func TestBuildGraphConflictDiagnosticIsInputOrderIndependent(t *testing.T) {
	first := GraphObservation{PhysicalID: "leaf-a", Complete: true, Neighbors: []GraphNeighbor{{Identity: "stable", PeerID: "peer-z", Source: "cdp", Interface: "Gi1"}}}
	second := GraphObservation{PhysicalID: "leaf-a", Complete: true, Neighbors: []GraphNeighbor{{Identity: "stable", PeerID: "peer-a", Source: "cdp", Interface: "Gi1"}}}
	left, err := BuildGraph([]GraphObservation{first, second}, GraphPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	right, err := BuildGraph([]GraphObservation{second, first}, GraphPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if left.EvidenceHash != right.EvidenceHash || canonicalJSON(left.Diagnostics) != canonicalJSON(right.Diagnostics) {
		t.Fatalf("conflict diagnostics depend on input order: left=%#v right=%#v", left, right)
	}
}

func TestBuildGraphThreeWayConflictIsInputOrderIndependent(t *testing.T) {
	observations := []GraphObservation{
		{PhysicalID: "leaf-a", Complete: true, Neighbors: []GraphNeighbor{{Identity: "stable", PeerID: "peer-z", Source: "cdp", Interface: "Gi1"}}},
		{PhysicalID: "leaf-a", Complete: true, Neighbors: []GraphNeighbor{{Identity: "stable", PeerID: "peer-a", Source: "cdp", Interface: "Gi1"}}},
		{PhysicalID: "leaf-a", Complete: true, Neighbors: []GraphNeighbor{{Identity: "stable", PeerID: "peer-m", Source: "cdp", Interface: "Gi1"}}},
	}
	permutations := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	var wantHash, wantDiagnostics string
	for _, order := range permutations {
		input := []GraphObservation{observations[order[0]], observations[order[1]], observations[order[2]]}
		graph, err := BuildGraph(input, GraphPolicy{})
		if err != nil {
			t.Fatalf("BuildGraph(%v) error = %v", order, err)
		}
		if graph.Complete || len(graph.Diagnostics) != 3 || graph.Diagnostics[0].Code != "DuplicateAdjacency" || graph.Diagnostics[1].Code != "DuplicateDeviceIdentity" || graph.Diagnostics[2].Code != "UnknownPeer" {
			t.Fatalf("BuildGraph(%v) did not fail closed with bounded diagnostics: %#v", order, graph)
		}
		diagnostics := canonicalJSON(graph.Diagnostics)
		if wantHash == "" {
			wantHash, wantDiagnostics = graph.EvidenceHash, diagnostics
			continue
		}
		if graph.EvidenceHash != wantHash || diagnostics != wantDiagnostics {
			t.Fatalf("three-way conflict depends on input order %v: hash=%s diagnostics=%s; want hash=%s diagnostics=%s", order, graph.EvidenceHash, diagnostics, wantHash, wantDiagnostics)
		}
	}
}

func TestBuildGraphRejectsOverlongState(t *testing.T) {
	if _, err := BuildGraph([]GraphObservation{{
		PhysicalID: "leaf-a", Complete: true,
		Neighbors: []GraphNeighbor{{PeerID: "leaf-b", State: strings.Repeat("x", MaxGraphFieldLength+1)}},
	}}, GraphPolicy{}); err == nil {
		t.Fatal("overlong adjacency state was accepted")
	}
}

func TestBuildGraphKeepsProtocolVRFAndLAGContextsDistinct(t *testing.T) {
	observations := []GraphObservation{
		{PhysicalID: "leaf-a", Complete: true, Neighbors: []GraphNeighbor{
			{PeerID: "leaf-b", Source: "cdp", Interface: "Port-channel10", RemoteInterface: "Port-channel20", State: "discovered"},
			{PeerID: "leaf-b", Source: "ospf", Interface: "Port-channel10", RemoteInterface: "Port-channel20", RoutingDomain: "blue/process-100/area-0", State: "FULL"},
		}},
		{PhysicalID: "leaf-b", Complete: true, Neighbors: []GraphNeighbor{
			{PeerID: "leaf-a", Source: "cdp", Interface: "Port-channel20", RemoteInterface: "Port-channel10", State: "discovered"},
			{PeerID: "leaf-a", Source: "ospf", Interface: "Port-channel20", RemoteInterface: "Port-channel10", RoutingDomain: "blue/process-100/area-0", State: "FULL"},
		}},
	}
	graph, err := BuildGraph(observations, GraphPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Complete || len(graph.Edges) != 4 {
		t.Fatalf("protocol/VRF/LAG graph = %#v", graph)
	}
	identities := make(map[string]struct{}, len(graph.Edges))
	for _, edge := range graph.Edges {
		identities[edge.Identity] = struct{}{}
		if edge.Interface != "Port-channel10" && edge.Interface != "Port-channel20" {
			t.Fatalf("LAG identity was not retained: %#v", edge)
		}
	}
	if len(identities) != 4 {
		t.Fatalf("source-qualified adjacency identities collided: %#v", graph.Edges)
	}
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "AsymmetricLink" || diagnostic.Code == "DuplicateAdjacency" {
			t.Fatalf("valid parallel protocol adjacencies were conflated: %#v", graph.Diagnostics)
		}
	}
}

func TestBuildGraphFailsClosedForProtocolOrVRFMismatch(t *testing.T) {
	tests := []struct {
		name    string
		reverse GraphNeighbor
	}{
		{
			name:    "protocol",
			reverse: GraphNeighbor{PeerID: "leaf-a", Source: "cdp", Interface: "Port-channel20", RemoteInterface: "Port-channel10", RoutingDomain: "blue", State: "FULL"},
		},
		{
			name:    "routing domain",
			reverse: GraphNeighbor{PeerID: "leaf-a", Source: "ospf", Interface: "Port-channel20", RemoteInterface: "Port-channel10", RoutingDomain: "red", State: "FULL"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph, err := BuildGraph([]GraphObservation{
				{PhysicalID: "leaf-a", Complete: true, Neighbors: []GraphNeighbor{{PeerID: "leaf-b", Source: "ospf", Interface: "Port-channel10", RemoteInterface: "Port-channel20", RoutingDomain: "blue", State: "FULL"}}},
				{PhysicalID: "leaf-b", Complete: true, Neighbors: []GraphNeighbor{test.reverse}},
			}, GraphPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			asymmetric := 0
			for _, diagnostic := range graph.Diagnostics {
				if diagnostic.Code == "AsymmetricLink" {
					asymmetric++
				}
			}
			if asymmetric != 2 {
				t.Fatalf("mismatched %s did not remain two unproven directional adjacencies: %#v", test.name, graph)
			}
		})
	}
}

func TestBuildGraphExactBoundsAndTruncatedCoverage(t *testing.T) {
	graph, err := BuildGraph([]GraphObservation{{
		PhysicalID: "leaf-a", Complete: false, UnknownReason: "neighbor collection truncated at the configured bound",
		Neighbors: []GraphNeighbor{{PeerID: "outside", Source: "cdp", Interface: "Gi1", RemoteInterface: "Gi2"}},
	}}, GraphPolicy{MaxNodes: 1, MaxEdges: 1, MaxInputNeighbors: 1, MaxDiagnostics: 3})
	if err != nil {
		t.Fatalf("exact configured bounds were rejected: %v", err)
	}
	if graph.Complete {
		t.Fatalf("truncated coverage was marked complete: %#v", graph)
	}
	foundIncomplete := false
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "IncompleteObservation" {
			foundIncomplete = true
		}
	}
	if !foundIncomplete {
		t.Fatalf("truncated coverage reason was not retained: %#v", graph.Diagnostics)
	}
}
