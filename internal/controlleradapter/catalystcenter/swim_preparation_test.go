// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

type testModernSWIMAPI struct {
	testSWIMAPI
	store                          *memorySWIMStore
	readiness                      int
	missingWorkflow, partialChecks bool
	readinessErr                   error
}

func (a *testModernSWIMAPI) StartReadinessCheck(context.Context, string) (Task, error) {
	a.readiness++
	return Task{ID: fmt.Sprintf("readiness-%d", a.readiness)}, a.readinessErr
}
func (a *testModernSWIMAPI) ListReadinessResults(context.Context, string) ([]ReadinessResult, error) {
	r := a.store.record
	items := []ReadinessResult{}
	for idx, name := range requiredXEReadinessChecks {
		items = append(items, ReadinessResult{ID: fmt.Sprintf("check-%d", idx), ParentID: r.ReadinessTask, DeviceID: r.Intent.ControllerDeviceID, Operation: "READINESS_CHECK", Type: "PRE_VALIDATION", Name: name, Status: "SUCCESS", StartTime: r.ReadinessNotBefore.UnixMilli(), EndTime: time.Now().UnixMilli()})
	}
	if a.partialChecks {
		items = items[:1]
	}
	return items, nil
}
func (a *testModernSWIMAPI) DistributeImages(ctx context.Context, device, image string) (Task, error) {
	return a.testSWIMAPI.Distribute(ctx, device, image)
}
func (a *testModernSWIMAPI) UpdateImages(ctx context.Context, device, image string) (Task, error) {
	return a.testSWIMAPI.Activate(ctx, device, image)
}
func (a *testModernSWIMAPI) ListImageUpdates(_ context.Context, device, task string) ([]ImageUpdate, error) {
	if a.missingWorkflow {
		return nil, nil
	}
	r := a.store.record
	kind, start := "DISTRIBUTE", r.DistributionNotBefore
	if task == "activation-task" {
		kind, start = "ACTIVATE", r.ActivationNotBefore
	}
	return []ImageUpdate{{ID: "workflow-" + task, ParentID: task, DeviceID: device, Version: r.Intent.ControllerImageVersion, Type: kind, Status: "SUCCESS", StartTime: start.UnixMilli(), EndTime: time.Now().UnixMilli()}}, nil
}

func TestModernSWIMRechecksBeforeActivationAndVerifies(t *testing.T) {
	e, i, s, a, _, v := executionFixture(t)
	api := &testModernSWIMAPI{store: s}
	e.api = api
	i.APIContract = swimModernContract
	i.ControllerImageVersion = "17.18.03.0.5496"
	for n := 0; n < 5; n++ {
		step(t, e, i)
	}
	if s.record.Phase != swimDistributed || api.readiness != 1 || api.distributions != 1 || api.activations != 0 {
		t.Fatalf("bad distribution transition: %+v", s.record)
	}
	for n := 0; n < 4; n++ {
		step(t, e, i)
	}
	if s.record.Phase != swimVerifying || api.readiness != 2 || api.activations != 1 || a.releases != 0 {
		t.Fatalf("bad activation transition: %+v", s.record)
	}
	step(t, e, i)
	step(t, e, i)
	if s.record.Phase != swimSucceeded || v.calls != 1 || a.releases != 1 {
		t.Fatal("did not verify and release")
	}
}

func TestModernSWIMDoesNotTrustParentTaskOrPartialReadiness(t *testing.T) {
	e, i, s, _, _, _ := executionFixture(t)
	api := &testModernSWIMAPI{store: s}
	e.api = api
	i.APIContract = swimModernContract
	i.ControllerImageVersion = "17.18.03"
	step(t, e, i)
	step(t, e, i)
	api.partialChecks = true
	if e.Step(context.Background(), i) == nil || api.distributions != 0 {
		t.Fatal("partial readiness authorized distribution")
	}
	api.partialChecks = false
	step(t, e, i)
	step(t, e, i)
	api.missingWorkflow = true
	step(t, e, i)
	if s.record.Phase != swimDistributing || api.activations != 0 {
		t.Fatal("parent task success treated as workflow success")
	}
}

func TestModernSWIMReadinessReceiptLossNeverReplays(t *testing.T) {
	for _, failure := range []string{"response", "persistence"} {
		t.Run(failure, func(t *testing.T) {
			e, i, s, a, _, _ := executionFixture(t)
			api := &testModernSWIMAPI{store: s}
			e.api = api
			i.APIContract = swimModernContract
			i.ControllerImageVersion = "17.18.03"
			step(t, e, i)
			if failure == "response" {
				api.readinessErr = errors.New("connection lost")
			} else {
				s.failWrite = 2
			}
			if e.Step(context.Background(), i) == nil {
				t.Fatal("expected uncertain submission")
			}
			step(t, e, i)
			step(t, e, i)
			if api.readiness != 1 || api.distributions != 0 || a.releases != 0 || s.record.Phase != swimOutcomeUnknown {
				t.Fatal("replayed readiness or released uncertain operation")
			}
		})
	}
}

func TestTransferFallbackIsNarrowAndOptIn(t *testing.T) {
	now := time.Now()
	start := now.Add(-time.Minute)
	var warning ReadinessResult
	wire := `{"name":"File Transfer Check","resultDetails":[{"key":"STATUS","value":"warning"},{"key":"DESCRIPTION","value":"HTTPS/SCP is reachable: 192.0.2.254/ Netconf transfer failed"},{"key":"EXPECTED","value":"HTTPS/SCP is reachable: 192.0.2.254. The Netconf transfer failed, likely because the device is unable to ping 192.0.2.254 through the default VRF."}]}`
	if err := json.Unmarshal([]byte(wire), &warning); err != nil {
		t.Fatal(err)
	}
	if warning.TransferFallbackAddress != "192.0.2.254" {
		t.Fatal("known fallback not recognized")
	}
	items := []ReadinessResult{}
	for idx, name := range requiredXEReadinessChecks {
		i := ReadinessResult{ID: fmt.Sprintf("check-%d", idx), ParentID: "task", DeviceID: "device", Name: name, Operation: "READINESS_CHECK", Type: "PRE_VALIDATION", Status: "SUCCESS", StartTime: start.UnixMilli(), EndTime: now.UnixMilli()}
		if name == warning.Name {
			i.Status = warning.Status
			i.TransferFallbackAddress = warning.TransferFallbackAddress
		}
		items = append(items, i)
	}
	if validateXEReadiness(items, "device", "task", "", start, now) == nil {
		t.Fatal("fallback enabled implicitly")
	}
	if validateXEReadiness(items, "device", "task", "192.0.2.253", start, now) == nil {
		t.Fatal("wrong appliance fallback accepted")
	}
	if err := validateXEReadiness(items, "device", "task", "192.0.2.254", start, now); err != nil {
		t.Fatal(err)
	}
	items[0].Status = "WARNING"
	if validateXEReadiness(items, "device", "task", "192.0.2.254", start, now) == nil {
		t.Fatal("unrelated warning suppressed")
	}
}

// Reauthorization is explicit: failed checks alone never submit more work.
func TestModernSWIMReadinessReplacementBoundedAndJournaled(t *testing.T) {
	e, i, s, a, _, _ := executionFixture(t)
	api := &testModernSWIMAPI{store: s, partialChecks: true}
	e.api = api
	i.APIContract = swimModernContract
	i.ControllerImageVersion = "17.18.03"
	step(t, e, i)
	step(t, e, i)
	original := s.record.ReadinessTask
	if e.Step(context.Background(), i) == nil || api.readiness != 1 {
		t.Fatal("repeated check without new grant")
	}
	for n := 1; n <= 2; n++ {
		a.claimRevision = fmt.Sprint(n)
		s.record.ReadinessNotBefore = time.Now().Add(-2 * time.Minute)
		step(t, e, i)
		if api.readiness != n+1 || len(s.record.ReadinessHistory) != n || s.record.ReadinessHistory[0].Task != original {
			t.Fatal("missing bounded durable history")
		}
	}
	a.claimRevision = "3"
	s.record.ReadinessNotBefore = time.Now().Add(-2 * time.Minute)
	if e.Step(context.Background(), i) == nil || api.readiness != 3 {
		t.Fatal("exceeded per-stage retry bound")
	}
	if api.activations != 0 || api.distributions != 0 {
		t.Fatal("failed checks dispatched mutation")
	}
}

func TestModernSWIMReadinessReplacementRetainsUncertainMarker(t *testing.T) {
	for _, failure := range []string{"response", "persistence", "revoked"} {
		t.Run(failure, func(t *testing.T) {
			e, i, s, a, _, _ := executionFixture(t)
			api := &testModernSWIMAPI{store: s, partialChecks: true}
			e.api = api
			i.APIContract = swimModernContract
			i.ControllerImageVersion = "17.18.03"
			step(t, e, i)
			step(t, e, i)
			a.claimRevision = "new"
			s.record.ReadinessNotBefore = time.Now().Add(-2 * time.Minute)
			switch failure {
			case "response":
				api.readinessErr = errors.New("lost")
			case "persistence":
				s.failWrite = s.writes + 2
			case "revoked":
				a.denyCheck = true
			}
			if e.Step(context.Background(), i) == nil {
				t.Fatal("expected failure")
			}
			step(t, e, i)
			step(t, e, i)
			want := 2
			if failure == "revoked" {
				want = 1
			}
			if api.readiness != want || s.record.Phase != swimOutcomeUnknown || len(s.record.ReadinessHistory) != 1 || a.releases != 0 {
				t.Fatal("uncertain retry replayed or lost history")
			}
		})
	}
}

func TestModernSWIMReadinessReplacementCooldownAndTaskReuse(t *testing.T) {
	e, i, s, a, _, _ := executionFixture(t)
	api := &testModernSWIMAPI{store: s, partialChecks: true}
	e.api = api
	i.APIContract = swimModernContract
	i.ControllerImageVersion = "17.18.03"
	step(t, e, i)
	step(t, e, i)
	a.claimRevision = "new"
	if e.Step(context.Background(), i) == nil || api.readiness != 1 {
		t.Fatal("ignored cooldown")
	}
	s.record.ReadinessNotBefore = time.Now().Add(-2 * time.Minute)
	api.readiness = 0 // Simulate the appliance returning the same task identifier.
	if e.Step(context.Background(), i) == nil || s.record.Phase != swimOutcomeUnknown {
		t.Fatal("accepted reused task ID")
	}
	step(t, e, i)
	if api.readiness != 1 || api.distributions != 0 {
		t.Fatal("replayed reused receipt")
	}
}
