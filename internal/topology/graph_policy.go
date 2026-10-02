// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package topology

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	GraphPolicyDataKey  = "graph.json"
	GraphPolicyVersion  = "v1"
	MaxGraphPolicyBytes = 256 * 1024
)

// GraphPolicyDocument is administrator-declared diagnostic input stored as a
// separate key in the admission-protected topology-policy ConfigMap. Keeping
// it outside policy.json prevents graph changes from becoming rollout
// authority or changing campaign approval hashes.
type GraphPolicyDocument struct {
	Version       string                `json:"version"`
	PeerMappings  []PeerIdentityMapping `json:"peerMappings,omitempty"`
	DeclaredLinks []DeclaredLink        `json:"declaredLinks,omitempty"`
}

// ParseGraphPolicyDocument strictly decodes and canonicalizes graph input.
// Unknown fields are rejected so misspelled safety declarations do not look
// active to an operator.
func ParseGraphPolicyDocument(raw string) (GraphPolicyDocument, error) {
	if strings.TrimSpace(raw) == "" {
		return GraphPolicyDocument{}, fmt.Errorf("%s is empty", GraphPolicyDataKey)
	}
	if len(raw) > MaxGraphPolicyBytes {
		return GraphPolicyDocument{}, fmt.Errorf("%s exceeds %d bytes", GraphPolicyDataKey, MaxGraphPolicyBytes)
	}
	var document GraphPolicyDocument
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return GraphPolicyDocument{}, fmt.Errorf("decode %s: %w", GraphPolicyDataKey, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return GraphPolicyDocument{}, fmt.Errorf("decode %s: %w", GraphPolicyDataKey, err)
	}
	if document.Version != GraphPolicyVersion {
		return GraphPolicyDocument{}, fmt.Errorf("unsupported topology graph policy version %q", document.Version)
	}
	if _, err := validatedPeerMappings(document.PeerMappings, DefaultGraphMaxPeerMappings); err != nil {
		return GraphPolicyDocument{}, err
	}
	declared, err := validatedDeclaredLinks(document.DeclaredLinks, DefaultGraphMaxDeclaredLinks)
	if err != nil {
		return GraphPolicyDocument{}, err
	}
	document.DeclaredLinks = declared
	for i := range document.PeerMappings {
		document.PeerMappings[i].Source = strings.TrimSpace(document.PeerMappings[i].Source)
		document.PeerMappings[i].ObservedPeer = strings.TrimSpace(document.PeerMappings[i].ObservedPeer)
		document.PeerMappings[i].RoutingDomain = strings.TrimSpace(document.PeerMappings[i].RoutingDomain)
		document.PeerMappings[i].PhysicalID, _ = CanonicalPhysicalIdentity(document.PeerMappings[i].PhysicalID)
	}
	sort.Slice(document.PeerMappings, func(i, j int) bool {
		return canonicalJSON(document.PeerMappings[i]) < canonicalJSON(document.PeerMappings[j])
	})
	sort.Slice(document.DeclaredLinks, func(i, j int) bool {
		return canonicalJSON(document.DeclaredLinks[i]) < canonicalJSON(document.DeclaredLinks[j])
	})
	return document, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("trailing JSON data")
}

// CanonicalGraphPolicyJSON returns the bounded semantic form used for Helm
// output and provenance hashing.
func CanonicalGraphPolicyJSON(document GraphPolicyDocument) (string, error) {
	encoded, err := json.Marshal(document)
	if err != nil {
		return "", err
	}
	canonical, err := ParseGraphPolicyDocument(string(encoded))
	if err != nil {
		return "", err
	}
	encoded, err = json.Marshal(canonical)
	return string(encoded), err
}

func GraphPolicyHash(document GraphPolicyDocument) (string, error) {
	canonical, err := CanonicalGraphPolicyJSON(document)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(canonical))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validatedDeclaredLinks(links []DeclaredLink, limit int) ([]DeclaredLink, error) {
	if len(links) > limit {
		return nil, fmt.Errorf("topology graph policy has more than %d declared links", limit)
	}
	normalized := make([]DeclaredLink, 0, len(links))
	seen := make(map[string]struct{}, len(links))
	for i, link := range links {
		local, err := CanonicalPhysicalIdentity(strings.TrimSpace(link.Local))
		if err != nil {
			return nil, fmt.Errorf("topology declared link %d local: %w", i, err)
		}
		peer, err := CanonicalPhysicalIdentity(strings.TrimSpace(link.Peer))
		if err != nil {
			return nil, fmt.Errorf("topology declared link %d peer: %w", i, err)
		}
		link = DeclaredLink{
			Local: local, Peer: peer, Source: strings.TrimSpace(link.Source),
			Interface: strings.TrimSpace(link.Interface), RemoteInterface: strings.TrimSpace(link.RemoteInterface),
			RoutingDomain: strings.TrimSpace(link.RoutingDomain),
		}
		if link.Interface == "" || link.RemoteInterface == "" {
			return nil, fmt.Errorf("topology declared link %d requires interface and remoteInterface", i)
		}
		for _, field := range []string{link.Source, link.Interface, link.RemoteInterface, link.RoutingDomain} {
			if len(field) > MaxGraphFieldLength {
				return nil, fmt.Errorf("topology declared link field exceeds %d bytes", MaxGraphFieldLength)
			}
		}
		key := linkKey(link.Local, link.Peer, link.Source, link.Interface, link.RemoteInterface, link.RoutingDomain)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("topology declared link %d is a duplicate", i)
		}
		seen[key] = struct{}{}
		normalized = append(normalized, link)
	}
	return normalized, nil
}
