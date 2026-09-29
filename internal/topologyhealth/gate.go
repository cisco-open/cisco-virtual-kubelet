// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

// Package topologyhealth contains the pure, driver-neutral decision logic for
// optional topology-aware rollout health gates. It deliberately owns no
// Kubernetes or device clients; callers supply an authenticated observation.
package topologyhealth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type InterfaceObservation struct {
	Name        string
	OperUp      bool
	HeadroomPct *float64
}

type NeighborObservation struct {
	ID     string
	State  string
	Source string
}

// Observation is a bounded snapshot from one authenticated network worker.
// Complete must be false when the driver could not prove that the required
// source was fully read; an empty successful response is not complete evidence.
type Observation struct {
	ObservedAt         time.Time
	Complete           bool
	UnknownReason      string
	Interfaces         []InterfaceObservation
	Neighbors          []NeighborObservation
	ProducerRevision   string
	DeviceIdentityHash string
}

type Policy struct {
	MaxAge                     time.Duration
	RequiredInterfaces         []string
	RequiredNeighbors          []string
	RequireInterfacesUp        bool
	RequireNeighborsFull       bool
	MinimumHeadroomPercent     *float64
	RequireCompleteEvidence    bool
	ExpectedDeviceIdentityHash string
}

type Decision struct {
	Allowed      bool
	Reason       string
	Message      string
	ObservedAt   time.Time
	EvidenceHash string
}

func Evaluate(now time.Time, observation Observation, policy Policy) Decision {
	if now.IsZero() {
		return blocked("InvalidEvaluationTime", "evaluation time is required", observation)
	}
	if observation.ObservedAt.IsZero() {
		return blocked("EvidenceMissing", "network observation time is missing", observation)
	}
	if observation.DeviceIdentityHash == "" {
		return blocked("DeviceIdentityMissing", "network observation has no device identity binding", observation)
	}
	if policy.ExpectedDeviceIdentityHash != "" && observation.DeviceIdentityHash != policy.ExpectedDeviceIdentityHash {
		return blocked("DeviceIdentityMismatch", "network observation is bound to a different physical device", observation)
	}
	if observation.ObservedAt.After(now.Add(30 * time.Second)) {
		return blocked("EvidenceClockSkew", "network observation is in the future", observation)
	}
	if policy.MaxAge > 0 && now.Sub(observation.ObservedAt) > policy.MaxAge {
		return blocked("EvidenceStale", "network observation is older than the configured freshness bound", observation)
	}
	if policy.RequireCompleteEvidence && !observation.Complete {
		reason := "EvidenceIncomplete"
		message := strings.TrimSpace(observation.UnknownReason)
		if message == "" {
			message = "network observation did not prove complete source coverage"
		}
		return blocked(reason, message, observation)
	}
	interfaces := make(map[string]InterfaceObservation, len(observation.Interfaces))
	for _, item := range observation.Interfaces {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			return blocked("EvidenceInvalid", "network observation contains an unnamed interface", observation)
		}
		if _, exists := interfaces[name]; exists {
			return blocked("EvidenceAmbiguous", fmt.Sprintf("interface %q appears more than once", name), observation)
		}
		interfaces[name] = item
	}
	for _, name := range sortedUnique(policy.RequiredInterfaces) {
		item, ok := interfaces[name]
		if !ok {
			return blocked("InterfaceEvidenceMissing", fmt.Sprintf("required interface %q is absent", name), observation)
		}
		if policy.RequireInterfacesUp && !item.OperUp {
			return blocked("InterfaceDown", fmt.Sprintf("required interface %q is not operationally up", name), observation)
		}
		if policy.MinimumHeadroomPercent != nil {
			if item.HeadroomPct == nil {
				return blocked("HeadroomUnknown", fmt.Sprintf("headroom for required interface %q is unknown", name), observation)
			}
			if *item.HeadroomPct < *policy.MinimumHeadroomPercent {
				return blocked("TransferHeadroomInsufficient", fmt.Sprintf("interface %q has %.2f%% headroom; %.2f%% is required", name, *item.HeadroomPct, *policy.MinimumHeadroomPercent), observation)
			}
		}
	}
	neighbors := make(map[string]NeighborObservation, len(observation.Neighbors))
	for _, item := range observation.Neighbors {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			return blocked("EvidenceInvalid", "network observation contains an unnamed neighbor", observation)
		}
		if _, exists := neighbors[id]; exists {
			return blocked("EvidenceAmbiguous", fmt.Sprintf("neighbor %q appears more than once", id), observation)
		}
		neighbors[id] = item
	}
	for _, id := range sortedUnique(policy.RequiredNeighbors) {
		item, ok := neighbors[id]
		if !ok {
			return blocked("AlternatePathUnavailable", fmt.Sprintf("required neighbor %q is absent", id), observation)
		}
		if policy.RequireNeighborsFull && !strings.EqualFold(strings.TrimSpace(item.State), "full") {
			return blocked("NeighborUnhealthy", fmt.Sprintf("required neighbor %q is in state %q", id, item.State), observation)
		}
	}
	return Decision{Allowed: true, Reason: "NetworkHealthReady", Message: "network health gates passed", ObservedAt: observation.ObservedAt, EvidenceHash: hash(observation)}
}

func blocked(reason, message string, observation Observation) Decision {
	return Decision{Reason: reason, Message: message, ObservedAt: observation.ObservedAt, EvidenceHash: hash(observation)}
}

func sortedUnique(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func hash(observation Observation) string {
	interfaces := append([]InterfaceObservation(nil), observation.Interfaces...)
	neighbors := append([]NeighborObservation(nil), observation.Neighbors...)
	sort.Slice(interfaces, func(i, j int) bool { return interfaces[i].Name < interfaces[j].Name })
	sort.Slice(neighbors, func(i, j int) bool { return neighbors[i].ID < neighbors[j].ID })
	canonical := struct {
		ObservedAt         time.Time              `json:"observedAt"`
		Complete           bool                   `json:"complete"`
		UnknownReason      string                 `json:"unknownReason,omitempty"`
		Interfaces         []InterfaceObservation `json:"interfaces"`
		Neighbors          []NeighborObservation  `json:"neighbors"`
		ProducerRevision   string                 `json:"producerRevision,omitempty"`
		DeviceIdentityHash string                 `json:"deviceIdentityHash,omitempty"`
	}{observation.ObservedAt.UTC(), observation.Complete, observation.UnknownReason, interfaces, neighbors, observation.ProducerRevision, observation.DeviceIdentityHash}
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}
