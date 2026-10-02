// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package topologygraph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

const (
	DefaultGraphMaxNodes          = 256
	DefaultGraphMaxEdges          = 512
	DefaultGraphMaxInputNeighbors = 1024
	DefaultGraphMaxDeclaredLinks  = 1024
	DefaultGraphMaxPeerMappings   = 1024
	DefaultGraphMaxDiagnostics    = 2048
	MaxGraphFieldLength           = 256
	MaxGraphNodes                 = 4096
	MaxGraphEdges                 = 8192
	MaxGraphInputNeighbors        = 16384
	MaxGraphPeerMappings          = 1024
)

// GraphObservation is the manager-facing form of one authenticated network
// snapshot. The graph builder deliberately accepts observations, rather than
// Kubernetes objects, so it cannot grant authority or mutate labels.
type GraphObservation struct {
	PhysicalID    string
	ObservedAt    time.Time
	Complete      bool
	UnknownReason string
	Neighbors     []GraphNeighbor
}

type GraphNeighbor struct {
	Identity        string
	PeerID          string
	Source          string
	Interface       string
	RemoteInterface string
	RoutingDomain   string
	State           string
}

type GraphPolicy struct {
	MaxNodes          int
	MaxEdges          int
	MaxInputNeighbors int
	MaxDeclaredLinks  int
	MaxPeerMappings   int
	MaxDiagnostics    int
	Now               time.Time
	MaxObservationAge time.Duration
	Declared          []DeclaredLink
	PeerMappings      []PeerIdentityMapping
}

// PeerIdentityMapping is administrator-owned evidence that a protocol-local
// peer identifier names one manager-bound physical device. It is diagnostic
// input only: discovery never creates mappings and mappings never authorize a
// rollout or mutate scheduling labels.
type PeerIdentityMapping struct {
	Source        string `json:"source"`
	ObservedPeer  string `json:"observedPeer"`
	RoutingDomain string `json:"routingDomain,omitempty"`
	PhysicalID    string `json:"physicalID"`
	External      bool   `json:"external,omitempty"`
}

type DeclaredLink struct {
	Local           string `json:"local"`
	Peer            string `json:"peer"`
	Source          string `json:"source,omitempty"`
	Interface       string `json:"interface"`
	RemoteInterface string `json:"remoteInterface"`
	RoutingDomain   string `json:"routingDomain,omitempty"`
}

type Graph struct {
	Nodes        []string
	Edges        []GraphEdge
	Diagnostics  []GraphDiagnostic
	Complete     bool
	EvidenceHash string
}

type GraphEdge struct {
	Local           string
	Peer            string
	ObservedPeer    string `json:",omitempty"`
	Identity        string
	Source          string
	Interface       string
	RemoteInterface string
	RoutingDomain   string
	State           string
}

type GraphDiagnostic struct {
	Code     string
	Severity string
	Local    string
	Peer     string
	Message  string
}

// BuildGraph creates a bounded, deterministic observed graph. Missing peers,
// incomplete source snapshots and asymmetric observations are diagnostics;
// they never get converted into a healthy or writable topology policy.
func BuildGraph(observations []GraphObservation, policy GraphPolicy) (Graph, error) {
	maxNodes := policy.MaxNodes
	if maxNodes <= 0 {
		maxNodes = DefaultGraphMaxNodes
	}
	maxEdges := policy.MaxEdges
	if maxEdges <= 0 {
		maxEdges = DefaultGraphMaxEdges
	}
	maxInputNeighbors := policy.MaxInputNeighbors
	if maxInputNeighbors <= 0 {
		maxInputNeighbors = DefaultGraphMaxInputNeighbors
	}
	maxDeclaredLinks := policy.MaxDeclaredLinks
	if maxDeclaredLinks <= 0 {
		maxDeclaredLinks = DefaultGraphMaxDeclaredLinks
	}
	maxPeerMappings := policy.MaxPeerMappings
	if maxPeerMappings <= 0 {
		maxPeerMappings = DefaultGraphMaxPeerMappings
	}
	maxDiagnostics := policy.MaxDiagnostics
	if maxDiagnostics <= 0 {
		maxDiagnostics = DefaultGraphMaxDiagnostics
	}
	if maxNodes > MaxGraphNodes || maxEdges > MaxGraphEdges || maxInputNeighbors > MaxGraphInputNeighbors || maxDeclaredLinks > DefaultGraphMaxDeclaredLinks || maxPeerMappings > MaxGraphPeerMappings || maxDiagnostics > DefaultGraphMaxDiagnostics {
		return Graph{}, fmt.Errorf("topology graph policy exceeds hard bounds")
	}
	if len(observations) > maxNodes {
		return Graph{}, fmt.Errorf("topology graph has %d observations; limit is %d", len(observations), maxNodes)
	}

	graph := Graph{Complete: true}
	nodes := make(map[string]struct{}, len(observations))
	duplicateNodes := make(map[string]struct{})
	for _, observation := range observations {
		local := strings.TrimSpace(observation.PhysicalID)
		if local == "" {
			return Graph{}, fmt.Errorf("topology graph contains an observation without physical identity")
		}
		if len(local) > MaxGraphFieldLength || len(observation.UnknownReason) > MaxGraphFieldLength {
			return Graph{}, fmt.Errorf("topology observation field exceeds %d bytes", MaxGraphFieldLength)
		}
		if policy.MaxObservationAge > 0 {
			if policy.Now.IsZero() {
				return Graph{}, fmt.Errorf("topology observation time is required for freshness policy")
			}
			if observation.ObservedAt.IsZero() {
				graph.Complete = false
				graph.Diagnostics = append(graph.Diagnostics, GraphDiagnostic{
					Code: "ObservationTimeMissing", Severity: "Error", Local: local,
					Message: "observation has no authenticated collection time",
				})
			} else if observation.ObservedAt.After(policy.Now.Add(30*time.Second)) || policy.Now.Sub(observation.ObservedAt) > policy.MaxObservationAge {
				graph.Complete = false
				graph.Diagnostics = append(graph.Diagnostics, GraphDiagnostic{
					Code: "StaleObservation", Severity: "Error", Local: local,
					Message: "observation is outside the configured freshness bound",
				})
			}
		}
		if _, duplicate := nodes[local]; duplicate {
			if _, alreadyReported := duplicateNodes[local]; !alreadyReported {
				graph.Diagnostics = append(graph.Diagnostics, GraphDiagnostic{
					Code: "DuplicateDeviceIdentity", Severity: "Error", Local: local,
					Message: "more than one observation claims the same physical identity",
				})
				duplicateNodes[local] = struct{}{}
			}
			graph.Complete = false
			continue
		}
		nodes[local] = struct{}{}
		graph.Nodes = append(graph.Nodes, local)
		if !observation.Complete {
			graph.Complete = false
			graph.Diagnostics = append(graph.Diagnostics, GraphDiagnostic{
				Code: "IncompleteObservation", Severity: "Error", Local: local,
				Message: boundedMessage(observation.UnknownReason, "observation did not prove complete source coverage"),
			})
		}
	}
	peerMappings, err := validatedPeerMappings(policy.PeerMappings, maxPeerMappings)
	if err != nil {
		return Graph{}, err
	}
	usedPeerMappings := make(map[string]struct{}, len(peerMappings))
	externalNodes := make(map[string]struct{})
	for _, mapping := range peerMappings {
		if !mapping.External {
			continue
		}
		if _, alreadyExternal := externalNodes[mapping.PhysicalID]; alreadyExternal {
			continue
		}
		if _, exists := nodes[mapping.PhysicalID]; exists {
			return Graph{}, fmt.Errorf("external peer mapping %q overlaps a manager-bound device", mapping.PhysicalID)
		}
		if len(nodes) >= maxNodes {
			return Graph{}, fmt.Errorf("topology graph has more than %d nodes after external mappings", maxNodes)
		}
		nodes[mapping.PhysicalID] = struct{}{}
		graph.Nodes = append(graph.Nodes, mapping.PhysicalID)
		externalNodes[mapping.PhysicalID] = struct{}{}
	}
	sort.Strings(graph.Nodes)

	seen := make(map[string][]GraphEdge, maxEdges)
	inputNeighbors := 0
	for _, observation := range observations {
		local := strings.TrimSpace(observation.PhysicalID)
		for _, neighbor := range observation.Neighbors {
			inputNeighbors++
			if inputNeighbors > maxInputNeighbors {
				return Graph{}, fmt.Errorf("topology graph input has more than %d neighbors", maxInputNeighbors)
			}
			observedPeer := strings.TrimSpace(neighbor.PeerID)
			peer := observedPeer
			if len(peer) > MaxGraphFieldLength || len(neighbor.Interface) > MaxGraphFieldLength || len(neighbor.RemoteInterface) > MaxGraphFieldLength || len(neighbor.RoutingDomain) > MaxGraphFieldLength || len(neighbor.Source) > MaxGraphFieldLength || len(neighbor.Identity) > MaxGraphFieldLength || len(neighbor.State) > MaxGraphFieldLength {
				return Graph{}, fmt.Errorf("topology adjacency field exceeds %d bytes", MaxGraphFieldLength)
			}
			identity := strings.TrimSpace(neighbor.Identity)
			source := strings.TrimSpace(neighbor.Source)
			routingDomain := strings.TrimSpace(neighbor.RoutingDomain)
			mappingKey := peerMappingKey(source, observedPeer, routingDomain)
			if mapped, ok := peerMappings[mappingKey]; ok {
				peer = mapped.PhysicalID
				usedPeerMappings[mappingKey] = struct{}{}
			}
			if identity == "" {
				identity = graphEdgeIdentity(local, peer, source, neighbor.Interface, neighbor.RemoteInterface, neighbor.RoutingDomain)
			}
			key := canonicalKey(local, identity)
			edge := GraphEdge{
				Local: local, Peer: peer, Identity: identity, Source: source,
				Interface: strings.TrimSpace(neighbor.Interface), RemoteInterface: strings.TrimSpace(neighbor.RemoteInterface), RoutingDomain: routingDomain,
				State: strings.TrimSpace(neighbor.State),
			}
			if peer != observedPeer {
				edge.ObservedPeer = observedPeer
			}
			seen[key] = append(seen[key], edge)
			if len(seen) > maxEdges {
				return Graph{}, fmt.Errorf("topology graph has more than %d unique edges", maxEdges)
			}
		}
	}
	graph.Edges = make([]GraphEdge, 0, len(seen))
	for _, candidates := range seen {
		sort.Slice(candidates, func(i, j int) bool {
			return canonicalJSON(candidates[i]) < canonicalJSON(candidates[j])
		})
		edge := candidates[0]
		if len(candidates) > 1 {
			graph.Complete = false
			message := "the same source-qualified adjacency was observed more than once"
			if canonicalJSON(candidates[0]) != canonicalJSON(candidates[len(candidates)-1]) {
				message = "the same source-qualified adjacency was observed with conflicting fields"
			}
			graph.Diagnostics = append(graph.Diagnostics, GraphDiagnostic{
				Code: "DuplicateAdjacency", Severity: "Error", Local: edge.Local, Peer: edge.Peer, Message: message,
			})
		}
		graph.Edges = append(graph.Edges, edge)
		if _, known := nodes[edge.Peer]; !known {
			graph.Complete = false
			graph.Diagnostics = append(graph.Diagnostics, GraphDiagnostic{
				Code: "UnknownPeer", Severity: "Warning", Local: edge.Local, Peer: edge.Peer,
				Message: "peer is not present in the supplied observation set",
			})
		}
	}
	sort.Slice(graph.Edges, func(i, j int) bool {
		if graph.Edges[i].Local != graph.Edges[j].Local {
			return graph.Edges[i].Local < graph.Edges[j].Local
		}
		return graph.Edges[i].Identity < graph.Edges[j].Identity
	})
	graph.Diagnostics = append(graph.Diagnostics, asymmetricDiagnostics(graph.Edges, nodes, externalNodes)...)
	for key, mapping := range peerMappings {
		if _, used := usedPeerMappings[key]; !used {
			graph.Diagnostics = append(graph.Diagnostics, GraphDiagnostic{
				Code: "UnusedPeerMapping", Severity: "Warning", Peer: mapping.PhysicalID,
				Message: "administrator peer mapping did not match any accepted adjacency",
			})
		}
		if _, known := nodes[mapping.PhysicalID]; !known && !mapping.External {
			graph.Diagnostics = append(graph.Diagnostics, GraphDiagnostic{
				Code: "MappedPeerUnavailable", Severity: "Error", Peer: mapping.PhysicalID,
				Message: "administrator peer mapping targets no manager-bound device in the selected scope",
			})
		}
	}
	declared, err := validatedDeclaredLinks(policy.Declared, maxDeclaredLinks)
	if err != nil {
		return Graph{}, err
	}
	graph.Diagnostics = append(graph.Diagnostics, declaredDrift(graph.Edges, declared)...)
	sort.Slice(graph.Diagnostics, func(i, j int) bool {
		left := graph.Diagnostics[i].Code + "\x00" + graph.Diagnostics[i].Local + "\x00" + graph.Diagnostics[i].Peer + "\x00" + graph.Diagnostics[i].Message
		right := graph.Diagnostics[j].Code + "\x00" + graph.Diagnostics[j].Local + "\x00" + graph.Diagnostics[j].Peer + "\x00" + graph.Diagnostics[j].Message
		return left < right
	})
	if len(graph.Diagnostics) > 0 {
		for _, diagnostic := range graph.Diagnostics {
			if diagnostic.Severity == "Error" {
				graph.Complete = false
				break
			}
		}
	}
	if len(graph.Diagnostics) > maxDiagnostics {
		return Graph{}, fmt.Errorf("topology graph produced more than %d diagnostics", maxDiagnostics)
	}
	graph.EvidenceHash = graphHash(graph)
	return graph, nil
}

func validatedPeerMappings(mappings []PeerIdentityMapping, limit int) (map[string]PeerIdentityMapping, error) {
	if len(mappings) > limit {
		return nil, fmt.Errorf("topology graph policy has more than %d peer mappings", limit)
	}
	result := make(map[string]PeerIdentityMapping, len(mappings))
	for i, mapping := range mappings {
		source := strings.TrimSpace(mapping.Source)
		observedPeer := strings.TrimSpace(mapping.ObservedPeer)
		routingDomain := strings.TrimSpace(mapping.RoutingDomain)
		if source == "" || observedPeer == "" {
			return nil, fmt.Errorf("topology peer mapping %d requires source and observedPeer", i)
		}
		if len(source) > MaxGraphFieldLength || len(observedPeer) > MaxGraphFieldLength || len(routingDomain) > MaxGraphFieldLength {
			return nil, fmt.Errorf("topology peer mapping field exceeds %d bytes", MaxGraphFieldLength)
		}
		physicalID, err := topologyidentity.CanonicalPhysicalIdentity(mapping.PhysicalID)
		if err != nil {
			return nil, fmt.Errorf("topology peer mapping %d physicalID: %w", i, err)
		}
		key := peerMappingKey(source, observedPeer, routingDomain)
		if existing, duplicate := result[key]; duplicate {
			return nil, fmt.Errorf("topology peer mapping %d duplicates mapping to %q", i, existing.PhysicalID)
		}
		result[key] = PeerIdentityMapping{
			Source: source, ObservedPeer: observedPeer, RoutingDomain: routingDomain,
			PhysicalID: physicalID, External: mapping.External,
		}
	}
	return result, nil
}

func peerMappingKey(source, observedPeer, routingDomain string) string {
	return canonicalKey(strings.TrimSpace(source), strings.TrimSpace(observedPeer), strings.TrimSpace(routingDomain))
}

func asymmetricDiagnostics(edges []GraphEdge, nodes, externalNodes map[string]struct{}) []GraphDiagnostic {
	seen := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		seen[adjacencyKey(edge.Local, edge.Peer, edge.Source, edge.RoutingDomain, edge.Interface, edge.RemoteInterface)] = struct{}{}
	}
	var diagnostics []GraphDiagnostic
	for _, edge := range edges {
		if _, known := nodes[edge.Peer]; !known {
			continue
		}
		if _, external := externalNodes[edge.Peer]; external {
			continue
		}
		if edge.RemoteInterface == "" {
			diagnostics = append(diagnostics, GraphDiagnostic{
				Code: "ReverseIdentityInsufficient", Severity: "Warning", Local: edge.Local, Peer: edge.Peer,
				Message: "remote interface identity is absent; reverse adjacency cannot be proven",
			})
			continue
		}
		if _, reverse := seen[adjacencyKey(edge.Peer, edge.Local, edge.Source, edge.RoutingDomain, edge.RemoteInterface, edge.Interface)]; reverse {
			continue
		}
		diagnostics = append(diagnostics, GraphDiagnostic{
			Code: "AsymmetricLink", Severity: "Warning", Local: edge.Local, Peer: edge.Peer,
			Message: "the peer does not report a reverse adjacency in the supplied snapshot",
		})
	}
	return diagnostics
}

func declaredDrift(edges []GraphEdge, declared []DeclaredLink) []GraphDiagnostic {
	observed := make(map[string]struct{}, len(edges))
	observedWithoutSource := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		observed[linkKey(edge.Local, edge.Peer, edge.Source, edge.Interface, edge.RemoteInterface, edge.RoutingDomain)] = struct{}{}
		observedWithoutSource[linkKey(edge.Local, edge.Peer, "", edge.Interface, edge.RemoteInterface, edge.RoutingDomain)] = struct{}{}
	}
	declaredSet := make(map[string]struct{}, len(declared))
	declaredSetWithoutSource := make(map[string]struct{}, len(declared))
	var diagnostics []GraphDiagnostic
	for _, link := range declared {
		key := linkKey(link.Local, link.Peer, link.Source, link.Interface, link.RemoteInterface, link.RoutingDomain)
		if link.Source == "" {
			declaredSetWithoutSource[linkKey(link.Local, link.Peer, "", link.Interface, link.RemoteInterface, link.RoutingDomain)] = struct{}{}
		} else {
			declaredSet[key] = struct{}{}
		}
		_, ok := observed[key]
		if link.Source == "" {
			_, ok = observedWithoutSource[linkKey(link.Local, link.Peer, "", link.Interface, link.RemoteInterface, link.RoutingDomain)]
		}
		if !ok {
			diagnostics = append(diagnostics, GraphDiagnostic{Code: "DeclaredLinkMissing", Severity: "Error", Local: link.Local, Peer: link.Peer, Message: "declared link is not present in observed topology"})
		}
	}
	for _, edge := range edges {
		if len(declared) == 0 {
			break
		}
		_, ok := declaredSet[linkKey(edge.Local, edge.Peer, edge.Source, edge.Interface, edge.RemoteInterface, edge.RoutingDomain)]
		if !ok {
			_, ok = declaredSetWithoutSource[linkKey(edge.Local, edge.Peer, "", edge.Interface, edge.RemoteInterface, edge.RoutingDomain)]
		}
		if !ok {
			diagnostics = append(diagnostics, GraphDiagnostic{Code: "UnexpectedObservedLink", Severity: "Warning", Local: edge.Local, Peer: edge.Peer, Message: "observed link is outside the declared topology"})
		}
	}
	return diagnostics
}

func linkKey(local, peer, source, iface, remoteIface, domain string) string {
	return canonicalKey(strings.TrimSpace(local), strings.TrimSpace(peer), strings.TrimSpace(source), strings.TrimSpace(iface), strings.TrimSpace(remoteIface), strings.TrimSpace(domain))
}

func graphEdgeIdentity(local, peer, source, iface, remoteIface, domain string) string {
	raw := adjacencyKey(local, peer, source, domain, iface, remoteIface)
	if len(raw) <= 128 {
		return raw
	}
	digest := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func adjacencyKey(local, peer, source, domain, iface, remoteIface string) string {
	return canonicalKey(strings.TrimSpace(local), strings.TrimSpace(peer), strings.TrimSpace(source), strings.TrimSpace(domain), strings.TrimSpace(iface), strings.TrimSpace(remoteIface))
}

func canonicalKey(values ...string) string {
	return canonicalJSON(values)
}

func canonicalJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func graphHash(graph Graph) string {
	// The graph is already sorted and bounded. Hash structured JSON rather
	// than delimiter-separated text: a peer containing a pipe or newline must
	// not collide with a different graph. This is a topology-content hash, not
	// sample provenance or an authorization credential.
	payload := struct {
		Complete    bool              `json:"complete"`
		Nodes       []string          `json:"nodes"`
		Edges       []GraphEdge       `json:"edges"`
		Diagnostics []GraphDiagnostic `json:"diagnostics"`
	}{graph.Complete, graph.Nodes, graph.Edges, graph.Diagnostics}
	encoded, _ := json.Marshal(payload)
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func boundedMessage(message, fallback string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		message = fallback
	}
	if len(message) > 256 {
		message = message[:256]
	}
	return message
}
