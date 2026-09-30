package topologyhealth

import (
	"strings"
	"testing"
	"time"
)

func TestEvaluateRequiresCompleteFreshEvidence(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	identity := "sha256:" + strings.Repeat("a", 64)
	policy := Policy{MaxAge: 5 * time.Minute, RequireCompleteEvidence: true, ExpectedDeviceIdentityHash: identity}
	for name, observation := range map[string]Observation{
		"missing":    {},
		"stale":      {ObservedAt: now.Add(-6 * time.Minute), Complete: true, DeviceIdentityHash: identity},
		"incomplete": {ObservedAt: now, Complete: false, UnknownReason: "OSPF read failed", DeviceIdentityHash: identity},
		"future":     {ObservedAt: now.Add(time.Minute), Complete: true, DeviceIdentityHash: identity},
	} {
		t.Run(name, func(t *testing.T) {
			decision := Evaluate(now, observation, policy)
			if decision.Allowed || decision.EvidenceHash == "" {
				t.Fatalf("expected blocked decision with evidence hash: %+v", decision)
			}
			want := map[string]string{
				"missing": "EvidenceMissing", "stale": "EvidenceStale",
				"incomplete": "EvidenceIncomplete", "future": "EvidenceClockSkew",
			}[name]
			if decision.Reason != want {
				t.Fatalf("reason=%q, want %q", decision.Reason, want)
			}
		})
	}
}

func TestEvaluateRejectsAmbiguousPeerIdentity(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	decision := Evaluate(now, Observation{
		ObservedAt: now, Complete: true, DeviceIdentityHash: "sha256:" + strings.Repeat("a", 64),
		Neighbors: []NeighborObservation{
			{Identity: "cdp|core-a|Gi1|", ID: "core-a", State: "discovered"},
			{Identity: "ospf|core-a|Gi2|0", ID: "core-a", State: "Full"},
		},
	}, Policy{RequiredNeighbors: []string{"core-a"}})
	if decision.Reason != "EvidenceAmbiguous" {
		t.Fatalf("decision=%#v, want EvidenceAmbiguous", decision)
	}
}

func TestEvaluateChecksPathsAndHeadroom(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	minimum := 30.0
	policy := Policy{
		MaxAge: 5 * time.Minute, RequireCompleteEvidence: true,
		RequiredInterfaces: []string{"Gi1/0/1"}, RequiredNeighbors: []string{"core-a"},
		RequireInterfacesUp: true, RequireNeighborsFull: true, MinimumHeadroomPercent: &minimum,
	}
	observation := Observation{
		ObservedAt: now.Add(-time.Minute), Complete: true,
		DeviceIdentityHash: "sha256:" + strings.Repeat("a", 64),
		Interfaces:         []InterfaceObservation{{Name: "Gi1/0/1", OperUp: true, HeadroomPct: &minimum}},
		Neighbors:          []NeighborObservation{{ID: "core-a", State: "Full"}},
	}
	if decision := Evaluate(now, observation, policy); !decision.Allowed {
		t.Fatalf("expected healthy redundant path to pass: %+v", decision)
	}
	observation.Neighbors[0].State = "Down"
	decision := Evaluate(now, observation, policy)
	if decision.Allowed || decision.Reason != "NeighborUnhealthy" {
		t.Fatalf("expected neighbor gate, got %+v", decision)
	}
	if !strings.HasPrefix(decision.EvidenceHash, "sha256:") {
		t.Fatalf("unexpected evidence hash %q", decision.EvidenceHash)
	}
}

func TestEvaluateRejectsAmbiguousEvidence(t *testing.T) {
	now := time.Now().UTC()
	decision := Evaluate(now, Observation{ObservedAt: now, Complete: true, DeviceIdentityHash: "sha256:" + strings.Repeat("b", 64), Interfaces: []InterfaceObservation{{Name: "Gi1"}, {Name: "Gi1"}}}, Policy{RequireCompleteEvidence: true})
	if decision.Allowed || decision.Reason != "EvidenceAmbiguous" {
		t.Fatalf("expected ambiguous evidence rejection, got %+v", decision)
	}
}

func TestEvaluateRejectsIdentityMismatch(t *testing.T) {
	now := time.Now().UTC()
	decision := Evaluate(now, Observation{
		ObservedAt: now, Complete: true, DeviceIdentityHash: "sha256:" + strings.Repeat("a", 64),
	}, Policy{ExpectedDeviceIdentityHash: "sha256:" + strings.Repeat("b", 64)})
	if decision.Allowed || decision.Reason != "DeviceIdentityMismatch" {
		t.Fatalf("expected identity mismatch rejection, got %+v", decision)
	}
}
