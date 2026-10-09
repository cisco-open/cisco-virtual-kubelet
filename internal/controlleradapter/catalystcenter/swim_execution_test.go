// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
)

type memorySWIMStore struct {
	record                      swimRecord
	exists                      bool
	revision, writes, failWrite int
}

func (s *memorySWIMStore) Load(context.Context, string) (swimRecord, error) {
	if !s.exists {
		return swimRecord{}, errSWIMRecordNotFound
	}
	return s.record, nil
}
func (s *memorySWIMStore) Create(_ context.Context, r swimRecord) error {
	if s.exists {
		return errors.New("already exists")
	}
	s.exists = true
	s.revision++
	r.Revision = strconv.Itoa(s.revision)
	s.record = r
	return nil
}
func (s *memorySWIMStore) Replace(_ context.Context, before, after swimRecord) error {
	s.writes++
	if s.writes == s.failWrite {
		return errors.New("injected persistence failure")
	}
	if before.Revision != s.record.Revision || before.Intent != s.record.Intent || after.Intent != before.Intent {
		return errors.New("CAS conflict")
	}
	s.revision++
	after.Revision = strconv.Itoa(s.revision)
	s.record = after
	return nil
}

type testSWIMAuthority struct {
	claims, holds, releases int
	denyClaim, denyCheck    bool
	claimRevision           string
}

func (a *testSWIMAuthority) Claim(_ context.Context, _ swimIntent, phase swimPhase) (string, error) {
	a.claims++
	if a.denyClaim {
		return "", errors.New("no admission")
	}
	return string(phase) + "-claim" + a.claimRevision, nil
}
func (a *testSWIMAuthority) Check(context.Context, swimIntent, swimPhase, string) error {
	if a.denyCheck {
		return errors.New("admission revoked")
	}
	return nil
}
func (a *testSWIMAuthority) Hold(context.Context, swimIntent) error    { a.holds++; return nil }
func (a *testSWIMAuthority) Release(context.Context, swimIntent) error { a.releases++; return nil }

type testSWIMVerifier struct {
	bad   bool
	calls int
}

func (v *testSWIMVerifier) Verify(_ context.Context, intent swimIntent) (swimVerification, error) {
	v.calls++
	version := intent.TargetVersion
	if v.bad {
		version = "17.12.1"
	}
	return swimVerification{DeviceUID: intent.DeviceUID, Serial: intent.Serial, RunningVersion: version, ObservedAt: time.Now()}, nil
}

type testSWIMAPI struct {
	distributions, activations, polls int
	submitErr, pollErr                error
	failed                            bool
}

func (a *testSWIMAPI) Distribute(context.Context, string, string) (Task, error) {
	a.distributions++
	return Task{ID: "distribution-task"}, a.submitErr
}
func (a *testSWIMAPI) Activate(context.Context, string, string) (Task, error) {
	a.activations++
	return Task{ID: "activation-task"}, a.submitErr
}
func (a *testSWIMAPI) GetTask(_ context.Context, id string) ([]byte, error) {
	a.polls++
	return []byte(fmt.Sprintf(`{"response":{"id":%q,"isError":%t,"endTime":1}}`, id, a.failed)), a.pollErr
}

func executionFixture(t *testing.T) (*swimExecutor, swimIntent, *memorySWIMStore, *testSWIMAuthority, *testSWIMAPI, *testSWIMVerifier) {
	t.Helper()
	binding := softwarelifecycle.UpgradeExecution{Method: softwarelifecycle.UpgradeCatalystCenter, ControllerNamespace: "lab", ControllerName: "catc", ControllerUID: "catc-uid"}
	intent := swimIntent{OperationUID: "op-uid", Execution: binding, DeviceNamespace: "devices", DeviceName: "switch", DeviceUID: "device-uid", Serial: "SERIAL", ControllerDeviceID: "catc-device", ImageID: "image-uuid", TargetVersion: "17.18.3"}
	store, authority, api, verifier := &memorySWIMStore{}, &testSWIMAuthority{}, &testSWIMAPI{}, &testSWIMVerifier{}
	executor, err := newSWIMExecutor(binding, store, authority, verifier, api)
	if err != nil {
		t.Fatal(err)
	}
	return executor, intent, store, authority, api, verifier
}
func step(t *testing.T, e *swimExecutor, i swimIntent) {
	t.Helper()
	if err := e.Step(context.Background(), i); err != nil {
		t.Fatal(err)
	}
}

func TestSWIMExecutionRequiresVerificationBeforeRelease(t *testing.T) {
	e, i, s, a, api, v := executionFixture(t)
	for n := 0; n < 5; n++ {
		step(t, e, i)
	}
	if s.record.Phase != swimVerifying || a.releases != 0 || api.distributions != 1 || api.activations != 1 {
		t.Fatalf("premature completion: %+v releases=%d", s.record, a.releases)
	}
	v.bad = true
	if err := e.Step(context.Background(), i); err == nil {
		t.Fatal("wrong version verified")
	}
	if a.releases != 0 || s.record.Phase != swimVerifying {
		t.Fatal("failed verification released fence")
	}
	v.bad = false
	step(t, e, i)
	if s.record.Phase != swimSucceeded || a.releases != 0 {
		t.Fatal("success must be persisted before release")
	}
	step(t, e, i)
	if a.releases != 1 || a.claims != 2 {
		t.Fatalf("releases=%d claims=%d", a.releases, a.claims)
	}
}

func TestSWIMLostTaskReceiptNeverReplaysAfterRestart(t *testing.T) {
	e, i, s, a, api, _ := executionFixture(t)
	step(t, e, i)
	s.failWrite = 2 // persist claim, submit POST, lose task receipt persistence
	if err := e.Step(context.Background(), i); err == nil {
		t.Fatal("expected persistence error")
	}
	if s.record.Phase != swimDistributionClaimed || api.distributions != 1 {
		t.Fatalf("record=%+v calls=%d", s.record, api.distributions)
	}
	restarted, err := newSWIMExecutor(e.binding, s, a, e.verifier, api)
	if err != nil {
		t.Fatal(err)
	}
	step(t, restarted, i)
	step(t, restarted, i)
	if s.record.Phase != swimOutcomeUnknown || api.distributions != 1 || api.activations != 0 || a.releases != 0 || a.holds == 0 {
		t.Fatal("ambiguous mutation replayed or fence released")
	}
}

func TestSWIMRecordedTaskSurvivesObservationOutageAndRestart(t *testing.T) {
	for _, phase := range []swimPhase{swimDistributing, swimActivating} {
		t.Run(string(phase), func(t *testing.T) {
			e, i, s, a, api, v := executionFixture(t)
			for n := 0; n < 5 && s.record.Phase != phase; n++ {
				step(t, e, i)
			}
			if s.record.Phase != phase {
				t.Fatalf("did not reach %s", phase)
			}
			before := s.record
			api.pollErr = context.DeadlineExceeded
			for n := 0; n < 2; n++ {
				// Reconstruct only from persisted state, including the task receipt.
				restarted, err := newSWIMExecutor(e.binding, s, a, v, api)
				if err != nil {
					t.Fatal(err)
				}
				if err := restarted.Step(context.Background(), i); err == nil {
					t.Fatal("observation outage was not reported")
				}
				if s.record.Phase != before.Phase || s.record.Revision != before.Revision || a.releases != 0 {
					t.Fatal("observation outage changed durable task or released fence")
				}
				e = restarted
			}
			api.pollErr = nil
			for n := 0; n < 8 && a.releases == 0; n++ {
				step(t, e, i)
			}
			if s.record.Phase != swimSucceeded || api.distributions != 1 || api.activations != 1 || a.releases != 1 || v.calls != 1 {
				t.Fatalf("did not resume exactly once: phase=%s distribution=%d activation=%d release=%d verification=%d", s.record.Phase, api.distributions, api.activations, a.releases, v.calls)
			}
		})
	}
}

func TestSWIMLostSubmissionResponseDoesNotFallback(t *testing.T) {
	e, i, s, a, api, _ := executionFixture(t)
	step(t, e, i)
	api.submitErr = context.DeadlineExceeded
	if err := e.Step(context.Background(), i); err == nil {
		t.Fatal("expected unknown outcome")
	}
	step(t, e, i)
	direct := i
	direct.Execution = softwarelifecycle.UpgradeExecution{}
	if err := e.Step(context.Background(), direct); err == nil {
		t.Fatal("implicit direct fallback allowed")
	}
	if s.record.Phase != swimOutcomeUnknown || api.distributions != 1 || api.activations != 0 || a.releases != 0 {
		t.Fatal("unsafe failure recovery")
	}
}

func TestSWIMAdmissionAndPersistenceFailuresPreventPOST(t *testing.T) {
	for _, kind := range []string{"claim denied", "claim not persisted", "admission revoked"} {
		t.Run(kind, func(t *testing.T) {
			e, i, s, a, api, _ := executionFixture(t)
			step(t, e, i)
			switch kind {
			case "claim denied":
				a.denyClaim = true
			case "claim not persisted":
				s.failWrite = 1
			case "admission revoked":
				a.denyCheck = true
			}
			if err := e.Step(context.Background(), i); err == nil {
				t.Fatal("expected failure")
			}
			if api.distributions != 0 || api.activations != 0 {
				t.Fatal("POST issued without durable live authorization")
			}
		})
	}
}

func TestSWIMTaskDisappearanceOrFailureRetainsFence(t *testing.T) {
	for _, kind := range []string{"missing", "failed"} {
		t.Run(kind, func(t *testing.T) {
			e, i, s, a, api, _ := executionFixture(t)
			step(t, e, i)
			step(t, e, i)
			if kind == "missing" {
				api.pollErr = errors.New("task not found")
			} else {
				api.failed = true
			}
			_ = e.Step(context.Background(), i)
			if a.releases != 0 || api.distributions != 1 || api.activations != 0 {
				t.Fatal("task failure allowed further mutation")
			}
			if kind == "missing" && s.record.Phase != swimDistributing {
				t.Fatal("missing task treated as successful")
			}
			if kind == "failed" && s.record.Phase != swimOutcomeUnknown {
				t.Fatal("failed task did not retain quarantine")
			}
		})
	}
}

func TestSWIMExecutionIdentityCannotChange(t *testing.T) {
	e, i, _, _, api, _ := executionFixture(t)
	step(t, e, i)
	for _, change := range []func(*swimIntent){func(i *swimIntent) { i.Execution.ControllerUID = "replacement" }, func(i *swimIntent) { i.DeviceUID = "replacement" }, func(i *swimIntent) { i.ImageID = "another-image" }, func(i *swimIntent) { i.TargetVersion = "17.19.1" }} {
		altered := i
		change(&altered)
		if err := e.Step(context.Background(), altered); err == nil {
			t.Fatal("changed execution identity accepted")
		}
	}
	if api.distributions != 0 {
		t.Fatal("identity drift reached API")
	}
}

func TestParseSWIMTaskRequiresExplicitCorrelatedCompletion(t *testing.T) {
	for _, body := range []string{`{}`, `{"response":null}`, `{"response":{"id":"wrong","isError":false,"endTime":1}}`, `{"response":{"id":"task","endTime":1}}`, `{"response":{"id":"task","isError":false,"endTime":-1}}`} {
		if _, err := parseSWIMTask([]byte(body), "task"); err == nil {
			t.Fatalf("accepted invalid task: %s", body)
		}
	}
	state, err := parseSWIMTask([]byte(`{"response":{"id":"task","isError":false}}`), "task")
	if err != nil || state.complete {
		t.Fatal("unfinished task completed")
	}
}

func TestSWIMActivationReceiptLossCannotRebootTwice(t *testing.T) {
	e, i, s, a, api, _ := executionFixture(t)
	for n := 0; n < 3; n++ {
		step(t, e, i)
	}
	s.failWrite = s.writes + 2
	if err := e.Step(context.Background(), i); err == nil {
		t.Fatal("expected activation receipt persistence failure")
	}
	if api.activations != 1 || s.record.Phase != swimActivationClaimed {
		t.Fatal("test did not lose activation receipt")
	}
	step(t, e, i)
	step(t, e, i)
	if api.activations != 1 || a.releases != 0 || s.record.Phase != swimOutcomeUnknown {
		t.Fatal("activation replayed after restart")
	}
}

func TestSWIMIncompleteRecordCannotAdvanceOrRelease(t *testing.T) {
	for _, phase := range []swimPhase{swimDistributed, swimVerifying, swimSucceeded} {
		t.Run(string(phase), func(t *testing.T) {
			e, i, s, a, api, _ := executionFixture(t)
			step(t, e, i)
			s.record.Phase = phase
			if err := e.Step(context.Background(), i); err == nil {
				t.Fatal("incomplete operation record accepted")
			}
			if api.activations != 0 || a.releases != 0 {
				t.Fatal("incomplete record allowed mutation or release")
			}
		})
	}
}
