// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package topology

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	DefaultGraphMaxNodes          = 256
	DefaultGraphMaxEdges          = 512
	DefaultGraphMaxInputNeighbors = 1024
	DefaultGraphMaxDeclaredLinks  = 1024
	DefaultGraphMaxDiagnostics    = 2048
	MaxGraphFieldLength           = 256
	MaxGraphNodes                 = 4096
	MaxGraphEdges                 = 8192
	MaxGraphInputNeighbors        = 16384
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
	Identity      string
	PeerID        string
	Source        string
	Interface     string
	RoutingDomain string
	State         string
}

type GraphPolicy struct {
	MaxNodes          int
	MaxEdges          int
	MaxInputNeighbors int
	MaxDeclaredLinks  int
	MaxDiagnostics    int
	Now               time.Time
	MaxObservationAge time.Duration
	Declared          []DeclaredLink
}

type DeclaredLink struct {
	Local         string
	Peer          string
	Source        string
	Interface     string
	RoutingDomain string
}

type Graph struct {
	Nodes        []string
	Edges        []GraphEdge
	Diagnostics  []GraphDiagnostic
	Complete     bool
	EvidenceHash string
}

type GraphEdge struct {
	Local         string
	Peer          string
	Identity      string
	Source        string
	Interface     string
	RoutingDomain string
	State         string
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
	maxDiagnostics := policy.MaxDiagnostics
	if maxDiagnostics <= 0 {
		maxDiagnostics = DefaultGraphMaxDiagnostics
	}
	if maxNodes > MaxGraphNodes || maxEdges > MaxGraphEdges || maxInputNeighbors > MaxGraphInputNeighbors || maxDeclaredLinks > DefaultGraphMaxDeclaredLinks || maxDiagnostics > DefaultGraphMaxDiagnostics {
		return Graph{}, fmt.Errorf("topology graph policy exceeds hard bounds")
	}
	if len(observations) > maxNodes {
		return Graph{}, fmt.Errorf("topology graph has %d observations; limit is %d", len(observations), maxNodes)
	}

	graph := Graph{Complete: true}
	nodes := make(map[string]struct{}, len(observations))
	for _, observation := range observations {
		local := strings.TrimSpace(observation.PhysicalID)
		if local == "" {
			return Graph{}, fmt.Errorf("topology graph contains an observation without physical identity")
		}
		if len(local) > MaxGraphFieldLength || len(observation.UnknownReason) > MaxGraphFieldLength {
			return Graph{}, fmt.Errorf("topology observation field exceeds %d bytes", MaxGraphFieldLength)
		}
		if policy.MaxObservationAge > 0 {
			if policy.Now.IsZero() || observation.ObservedAt.IsZero() {
				return Graph{}, fmt.Errorf("topology observation time is required for freshness policy")
			}
			if observation.ObservedAt.After(policy.Now.Add(30*time.Second)) || policy.Now.Sub(observation.ObservedAt) > policy.MaxObservationAge {
				graph.Complete = false
				graph.Diagnostics = append(graph.Diagnostics, GraphDiagnostic{
					Code: "StaleObservation", Severity: "Error", Local: local,
					Message: "observation is outside the configured freshness bound",
				})
			}
		}
		if _, duplicate := nodes[local]; duplicate {
			graph.Diagnostics = append(graph.Diagnostics, GraphDiagnostic{
				Code: "DuplicateDeviceIdentity", Severity: "Error", Local: local,
				Message: "more than one observation claims the same physical identity",
			})
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
	sort.Strings(graph.Nodes)

	seen := make(map[string]GraphEdge, maxEdges)
	inputNeighbors := 0
	for _, observation := range observations {
		local := strings.TrimSpace(observation.PhysicalID)
		for _, neighbor := range observation.Neighbors {
			inputNeighbors++
			if inputNeighbors > maxInputNeighbors {
				return Graph{}, fmt.Errorf("topology graph input has more than %d neighbors", maxInputNeighbors)
			}
			peer := strings.TrimSpace(neighbor.PeerID)
			if len(peer) > MaxGraphFieldLength || len(neighbor.Interface) > MaxGraphFieldLength || len(neighbor.RoutingDomain) > MaxGraphFieldLength || len(neighbor.Source) > MaxGraphFieldLength || len(neighbor.Identity) > MaxGraphFieldLength {
				return Graph{}, fmt.Errorf("topology adjacency field exceeds %d bytes", MaxGraphFieldLength)
			}
			identity := strings.TrimSpace(neighbor.Identity)
			source := strings.TrimSpace(neighbor.Source)
			if identity == "" {
				identity = graphEdgeIdentity(local, peer, source, neighbor.Interface, neighbor.RoutingDomain)
			}
			key := canonicalKey(local, identity)
			edge := GraphEdge{
				Local: local, Peer: peer, Identity: identity, Source: source,
				Interface: strings.TrimSpace(neighbor.Interface), RoutingDomain: strings.TrimSpace(neighbor.RoutingDomain),
				State: strings.TrimSpace(neighbor.State),
			}
			if previous, duplicate := seen[key]; duplicate {
				graph.Complete = false
				message := "the same source-qualified adjacency was observed more than once"
				if canonicalJSON(edge) != canonicalJSON(previous) {
					message = "the same source-qualified adjacency was observed with conflicting fields"
					if canonicalJSON(edge) < canonicalJSON(previous) {
						seen[key] = edge
					}
				}
				graph.Diagnostics = append(graph.Diagnostics, GraphDiagnostic{
					Code: "DuplicateAdjacency", Severity: "Error", Local: local, Peer: peer, Message: message,
				})
				continue
			}
			seen[key] = edge
			if len(seen) > maxEdges {
				return Graph{}, fmt.Errorf("topology graph has more than %d unique edges", maxEdges)
			}
		}
	}
	graph.Edges = make([]GraphEdge, 0, len(seen))
	for _, edge := range seen {
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
	graph.Diagnostics = append(graph.Diagnostics, asymmetricDiagnostics(graph.Edges, nodes)...)
	if len(policy.Declared) > maxDeclaredLinks {
		return Graph{}, fmt.Errorf("topology graph policy has more than %d declared links", maxDeclaredLinks)
	}
	graph.Diagnostics = append(graph.Diagnostics, declaredDrift(graph.Edges, policy.Declared)...)
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

func asymmetricDiagnostics(edges []GraphEdge, nodes map[string]struct{}) []GraphDiagnostic {
	seen := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		seen[canonicalKey(edge.Local, edge.Peer, edge.Source, edge.RoutingDomain, edge.Interface)] = struct{}{}
	}
	var diagnostics []GraphDiagnostic
	for _, edge := range edges {
		if _, known := nodes[edge.Peer]; !known {
			continue
		}
		if _, reverse := seen[canonicalKey(edge.Peer, edge.Local, edge.Source, edge.RoutingDomain, edge.Interface)]; reverse {
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
		observed[linkKey(edge.Local, edge.Peer, edge.Source, edge.Interface, edge.RoutingDomain)] = struct{}{}
		observedWithoutSource[linkKey(edge.Local, edge.Peer, "", edge.Interface, edge.RoutingDomain)] = struct{}{}
	}
	declaredSet := make(map[string]struct{}, len(declared))
	declaredSetWithoutSource := make(map[string]struct{}, len(declared))
	var diagnostics []GraphDiagnostic
	for _, link := range declared {
		key := linkKey(link.Local, link.Peer, link.Source, link.Interface, link.RoutingDomain)
		if link.Source == "" {
			declaredSetWithoutSource[linkKey(link.Local, link.Peer, "", link.Interface, link.RoutingDomain)] = struct{}{}
		} else {
			declaredSet[key] = struct{}{}
		}
		_, ok := observed[key]
		if link.Source == "" {
			_, ok = observedWithoutSource[linkKey(link.Local, link.Peer, "", link.Interface, link.RoutingDomain)]
		}
		if !ok {
			diagnostics = append(diagnostics, GraphDiagnostic{Code: "DeclaredLinkMissing", Severity: "Error", Local: link.Local, Peer: link.Peer, Message: "declared link is not present in observed topology"})
		}
	}
	for _, edge := range edges {
		if len(declared) == 0 {
			break
		}
		_, ok := declaredSet[linkKey(edge.Local, edge.Peer, edge.Source, edge.Interface, edge.RoutingDomain)]
		if !ok {
			_, ok = declaredSetWithoutSource[linkKey(edge.Local, edge.Peer, "", edge.Interface, edge.RoutingDomain)]
		}
		if !ok {
			diagnostics = append(diagnostics, GraphDiagnostic{Code: "UnexpectedObservedLink", Severity: "Warning", Local: edge.Local, Peer: edge.Peer, Message: "observed link is outside the declared topology"})
		}
	}
	return diagnostics
}

func linkKey(local, peer, source, iface, domain string) string {
	return canonicalKey(strings.TrimSpace(local), strings.TrimSpace(peer), strings.TrimSpace(source), strings.TrimSpace(iface), strings.TrimSpace(domain))
}

func graphEdgeIdentity(local, peer, source, iface, domain string) string {
	raw := canonicalKey(local, peer, source, iface, domain)
	if len(raw) <= 128 {
		return raw
	}
	digest := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func canonicalKey(values ...string) string {
	return canonicalJSON(values)
}

func canonicalJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func graphHash(graph Graph) string {
	// The graph is already sorted and bounded. Hashing its canonical diagnostic
	// text gives callers a compact drift/provenance token without exposing it
	// as an authorization credential.
	var builder strings.Builder
	fmt.Fprintf(&builder, "complete|%t\n", graph.Complete)
	for _, node := range graph.Nodes {
		fmt.Fprintf(&builder, "n|%s\n", node)
	}
	for _, edge := range graph.Edges {
		fmt.Fprintf(&builder, "e|%s|%s|%s|%s|%s|%s|%s\n", edge.Local, edge.Peer, edge.Identity, edge.Source, edge.Interface, edge.RoutingDomain, edge.State)
	}
	for _, diagnostic := range graph.Diagnostics {
		fmt.Fprintf(&builder, "d|%s|%s|%s|%s|%s\n", diagnostic.Code, diagnostic.Local, diagnostic.Peer, diagnostic.Severity, diagnostic.Message)
	}
	digest := sha256.Sum256([]byte(builder.String()))
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
