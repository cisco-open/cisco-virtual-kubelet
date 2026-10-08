// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
)

// swimIntent is an immutable, resolved execution plan. It contains references
// and identities only; controller credentials stay in the adapter worker.
type swimIntent struct {
	OperationUID                                   string
	Execution                                      softwarelifecycle.UpgradeExecution
	DeviceNamespace, DeviceName, DeviceUID, Serial string
	ControllerDeviceID, ImageID, TargetVersion     string
}

type swimPhase string

const (
	swimPending             swimPhase = "Pending"
	swimDistributionClaimed swimPhase = "DistributionClaimed"
	swimDistributing        swimPhase = "Distributing"
	swimDistributed         swimPhase = "Distributed"
	swimActivationClaimed   swimPhase = "ActivationClaimed"
	swimActivating          swimPhase = "Activating"
	swimVerifying           swimPhase = "Verifying"
	swimSucceeded           swimPhase = "Succeeded"
	swimOutcomeUnknown      swimPhase = "OutcomeUnknown"
)

// A claimed phase is a write-ahead dispatch marker. Finding it after a restart
// is ambiguous, not permission to submit the same external request again.
type swimRecord struct {
	Revision                            string
	Intent                              swimIntent
	Phase                               swimPhase
	DistributionClaim, DistributionTask string
	ActivationClaim, ActivationTask     string
	ActivationNotBefore                 time.Time
}

var errSWIMRecordNotFound = errors.New("SWIM operation record not found")

// swimStore must use durable storage. Create is create-only; Replace is an
// atomic compare-and-swap using before.Revision and the immutable operation
// identity. Conflicts MUST return an error; an in-memory store is tests only.
// Records with claims cannot be deleted until the coordination authority has
// independently proved that the remote mutation is settled.
type swimStore interface {
	Load(context.Context, string) (swimRecord, error)
	Create(context.Context, swimRecord) error
	Replace(context.Context, swimRecord, swimRecord) error
}

// swimAuthority is the required bridge to CVK's canonical device coordination.
// Claim must durably bind the action to the operation, controller and device
// UIDs, image, maintenance window and rollout reservation before returning.
// Check repeats live admission, identity and image compatibility checks just
// before dispatch. Hold must preserve/renew the canonical mutation fence even
// after cancellation, deletion, timeout or worker restart. Release is permitted
// only after verified success. A plain expiring Lease is not an implementation.
// The existing controller adapter does not install a production bridge yet.
type swimAuthority interface {
	Claim(context.Context, swimIntent, swimPhase) (string, error)
	Check(context.Context, swimIntent, swimPhase, string) error
	Hold(context.Context, swimIntent) error
	Release(context.Context, swimIntent) error
}

type swimVerification struct {
	DeviceUID, Serial, RunningVersion string
	ObservedAt                        time.Time
}

// Verification comes from the authenticated device observation path. Successful
// Catalyst Center task completion or its cached inventory is not this evidence.
type swimVerifier interface {
	Verify(context.Context, swimIntent) (swimVerification, error)
}

type swimAPI interface {
	Distribute(context.Context, string, string) (Task, error)
	Activate(context.Context, string, string) (Task, error)
	GetTask(context.Context, string) ([]byte, error)
}

// swimExecutor implements controller task execution only. Direct upgrades
// continue to use the existing gNOI state machine. There is no backend fallback.
// It remains unregistered until durable storage and canonical admission wiring
// are available; constructing it without those dependencies fails closed.
type swimExecutor struct {
	binding   softwarelifecycle.UpgradeExecution
	store     swimStore
	authority swimAuthority
	verifier  swimVerifier
	api       swimAPI
	now       func() time.Time
}

func newSWIMExecutor(binding softwarelifecycle.UpgradeExecution, store swimStore, authority swimAuthority, verifier swimVerifier, api swimAPI) (*swimExecutor, error) {
	resolved, err := binding.Resolve()
	if err != nil {
		return nil, err
	}
	if resolved.Method != softwarelifecycle.UpgradeCatalystCenter || store == nil || authority == nil || verifier == nil || api == nil {
		return nil, errors.New("SWIM requires an explicit CatalystCenter binding, durable store, mutation authority, verifier and API")
	}
	return &swimExecutor{binding: resolved, store: store, authority: authority, verifier: verifier, api: api, now: time.Now}, nil
}

// Step performs at most one durable transition or one external submission.
// Callers retry observations and storage errors; claimed submissions are never
// retried, even if no task ID was returned or persisted.
func (e *swimExecutor) Step(ctx context.Context, intent swimIntent) error {
	if err := e.validateIntent(intent); err != nil {
		return err
	}
	record, err := e.store.Load(ctx, intent.OperationUID)
	if errors.Is(err, errSWIMRecordNotFound) {
		return e.store.Create(ctx, swimRecord{Intent: intent, Phase: swimPending})
	}
	if err != nil {
		return err
	}
	if record.Intent != intent || record.Revision == "" {
		return errors.New("SWIM operation identity or storage revision mismatch")
	}
	if record.Phase != swimPending && record.Phase != swimSucceeded {
		if err := e.authority.Hold(ctx, intent); err != nil {
			return err
		}
	}
	if err := validateSWIMRecord(record); err != nil {
		return err
	}
	switch record.Phase {
	case swimPending:
		return e.submit(ctx, record, swimDistributionClaimed)
	case swimDistributionClaimed, swimActivationClaimed:
		// The process that won the CAS might have sent the request and died before
		// recording its task ID. Recovery is observation/operator reconciliation.
		return e.transition(ctx, record, swimOutcomeUnknown)
	case swimDistributing:
		return e.observeTask(ctx, record, record.DistributionTask, swimDistributed)
	case swimDistributed:
		return e.submit(ctx, record, swimActivationClaimed)
	case swimActivating:
		return e.observeTask(ctx, record, record.ActivationTask, swimVerifying)
	case swimVerifying:
		observed, err := e.verifier.Verify(ctx, intent)
		if err != nil {
			return err
		}
		if observed.DeviceUID != intent.DeviceUID || observed.Serial != intent.Serial || observed.RunningVersion != intent.TargetVersion || !observed.ObservedAt.After(record.ActivationNotBefore) || observed.ObservedAt.After(e.now()) {
			return errors.New("fresh device evidence does not verify the pinned SWIM target")
		}
		return e.transition(ctx, record, swimSucceeded)
	case swimSucceeded:
		// Release happens only after the verified success is durable. It must be
		// idempotent so a restart between status persistence and release is safe.
		return e.authority.Release(ctx, intent)
	case swimOutcomeUnknown:
		return nil // Hold above maintains the fence; never replay or fall back.
	default:
		return errors.New("unsupported SWIM operation phase")
	}
}

func validateSWIMRecord(record swimRecord) error {
	switch record.Phase {
	case swimDistributionClaimed, swimDistributing, swimDistributed, swimActivationClaimed, swimActivating, swimVerifying, swimSucceeded:
		if record.DistributionClaim == "" {
			return errors.New("SWIM distribution claim is missing")
		}
	}
	switch record.Phase {
	case swimDistributing, swimDistributed, swimActivationClaimed, swimActivating, swimVerifying, swimSucceeded:
		if !validAPIID(record.DistributionTask) {
			return errors.New("SWIM distribution receipt is missing")
		}
	}
	switch record.Phase {
	case swimActivationClaimed, swimActivating, swimVerifying, swimSucceeded:
		if record.ActivationClaim == "" || record.ActivationNotBefore.IsZero() {
			return errors.New("SWIM activation claim is missing")
		}
	}
	switch record.Phase {
	case swimActivating, swimVerifying, swimSucceeded:
		if !validAPIID(record.ActivationTask) || record.ActivationTask == record.DistributionTask {
			return errors.New("SWIM activation receipt is missing or ambiguous")
		}
	}
	return nil
}

func (e *swimExecutor) validateIntent(intent swimIntent) error {
	if err := intent.Execution.ValidatePinned(e.binding); err != nil {
		return err
	}
	if intent.OperationUID == "" || intent.DeviceUID == "" || intent.DeviceNamespace == "" || intent.DeviceName == "" || intent.Serial == "" || !validAPIID(intent.ControllerDeviceID) || !validAPIID(intent.ImageID) {
		return errors.New("SWIM execution requires resolved immutable operation, device and image identities")
	}
	return softwarelifecycle.ValidateTargetVersion(intent.TargetVersion)
}

func (e *swimExecutor) submit(ctx context.Context, before swimRecord, phase swimPhase) error {
	claim, err := e.authority.Claim(ctx, before.Intent, phase)
	if err != nil {
		return err
	}
	if claim == "" {
		return errors.New("mutation authority returned an empty SWIM claim")
	}
	claimed := before
	claimed.Phase = phase
	if phase == swimDistributionClaimed {
		claimed.DistributionClaim = claim
	} else {
		claimed.ActivationClaim = claim
		claimed.ActivationNotBefore = e.now()
	}
	if err := e.store.Replace(ctx, before, claimed); err != nil {
		return err
	}
	// Re-read to obtain the storage revision and detect concurrent transitions.
	claimed, err = e.store.Load(ctx, before.Intent.OperationUID)
	if err != nil {
		return err
	}
	if claimed.Intent != before.Intent || claimed.Phase != phase || claimed.Revision == "" {
		return errors.New("SWIM dispatch marker changed before submission")
	}
	storedClaim := claimed.DistributionClaim
	if phase == swimActivationClaimed {
		storedClaim = claimed.ActivationClaim
	}
	if storedClaim != claim {
		return errors.New("SWIM dispatch claim changed before submission")
	}
	if err := e.authority.Check(ctx, before.Intent, phase, claim); err != nil {
		return err
	}
	var task Task
	if phase == swimDistributionClaimed {
		task, err = e.api.Distribute(ctx, before.Intent.ControllerDeviceID, before.Intent.ImageID)
	} else {
		task, err = e.api.Activate(ctx, before.Intent.ControllerDeviceID, before.Intent.ImageID)
	}
	if err != nil || !validAPIID(task.ID) || (phase == swimActivationClaimed && task.ID == claimed.DistributionTask) {
		// Persist the ambiguity when possible. If storage fails, the earlier
		// claimed marker still prevents a repeat POST on the next reconciliation.
		persistErr := e.transition(ctx, claimed, swimOutcomeUnknown)
		if persistErr != nil {
			return persistErr
		}
		return errors.New("SWIM submission outcome is unknown; mutation fence retained")
	}
	accepted := claimed
	if phase == swimDistributionClaimed {
		accepted.Phase, accepted.DistributionTask = swimDistributing, task.ID
	} else {
		accepted.Phase, accepted.ActivationTask = swimActivating, task.ID
	}
	return e.store.Replace(ctx, claimed, accepted)
}

func (e *swimExecutor) observeTask(ctx context.Context, record swimRecord, taskID string, next swimPhase) error {
	if !validAPIID(taskID) {
		return errors.New("SWIM operation has no valid persisted task ID")
	}
	body, err := e.api.GetTask(ctx, taskID)
	if err != nil {
		return err
	} // 404/timeout is not evidence that no work happened.
	state, err := parseSWIMTask(body, taskID)
	if err != nil {
		return err
	}
	if state.failed {
		return e.transition(ctx, record, swimOutcomeUnknown)
	}
	if !state.complete {
		return nil
	}
	return e.transition(ctx, record, next)
}

func (e *swimExecutor) transition(ctx context.Context, record swimRecord, phase swimPhase) error {
	next := record
	next.Phase = phase
	return e.store.Replace(ctx, record, next)
}

type swimTaskState struct{ complete, failed bool }

func parseSWIMTask(body []byte, id string) (swimTaskState, error) {
	var envelope struct {
		Response *struct {
			ID      string `json:"id"`
			IsError *bool  `json:"isError"`
			EndTime *int64 `json:"endTime"`
		} `json:"response"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Response == nil || envelope.Response.ID != id || envelope.Response.IsError == nil {
		return swimTaskState{}, fmt.Errorf("%w: task identity or state missing", errInvalidResponse)
	}
	task := envelope.Response
	if task.EndTime != nil && *task.EndTime < 0 {
		return swimTaskState{}, errInvalidResponse
	}
	return swimTaskState{failed: *task.IsError, complete: task.EndTime != nil && *task.EndTime > 0 && !*task.IsError}, nil
}
