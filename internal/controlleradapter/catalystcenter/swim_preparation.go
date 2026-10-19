// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"net/netip"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// These are the qualified IOS XE validation names observed on Catalyst Center
// 3.2.3. A changed/missing check is a qualification failure, not success.
var requiredXEReadinessChecks = []string{"Startup config check", "Config register check", "Flash check", "File Transfer Check", "Image Version Support", "XFSU Compatibility Check"}

func validateXEReadiness(items []ReadinessResult, deviceID, taskID, fallbackAddress string, submitted, now time.Time) error {
	if fallbackAddress != "" {
		if _, err := netip.ParseAddr(fallbackAddress); err != nil {
			return errors.New("fallback address must be a literal appliance IP")
		}
	}
	seen := map[string]bool{}
	copyItems := append([]ReadinessResult(nil), items...)
	for idx, item := range copyItems {
		if item.ParentID != taskID {
			continue
		}
		if seen[item.Name] {
			return errors.New("duplicate readiness check name")
		}
		seen[item.Name] = true
		// Only a known positive alternative-transport result can qualify this
		// particular warning. Missing, contradictory or other warnings stay blocked.
		if item.Name == "File Transfer Check" && item.Status == "WARNING" && fallbackAddress != "" && item.TransferFallbackAddress == fallbackAddress {
			copyItems[idx].Status = "SUCCESS"
		}
	}
	for _, name := range requiredXEReadinessChecks {
		if !seen[name] {
			return errors.New("required XE readiness check missing")
		}
	}
	return validateReadinessResults(copyItems, deviceID, taskID, submitted, now, 15*time.Minute)
}

func (e *swimExecutor) startReadiness(ctx context.Context, before swimRecord, next swimPhase) error {
	// Admission precedes even preflight so a competing upgrade cannot invalidate
	// the observations while the controller is producing them.
	claim, err := e.authority.Claim(ctx, before.Intent, swimReadinessClaimed)
	if err != nil {
		return err
	}
	if claim == "" {
		return errors.New("missing readiness admission")
	}
	if before.ReadinessTask != "" && before.ReadinessFor == next {
		attempts := 1
		for _, receipt := range before.ReadinessHistory {
			if receipt.Stage == string(next) {
				attempts++
			}
		}
		if attempts >= 3 || claim == before.ReadinessClaim || e.now().Sub(before.ReadinessNotBefore) < time.Minute {
			return errors.New("readiness replacement requires a new parent grant, cooldown and remaining attempt budget")
		}
	}
	marked := before
	marked.ReadinessHistory = append([]ops.SWIMReadinessReceipt(nil), before.ReadinessHistory...)
	if before.ReadinessTask != "" {
		if len(marked.ReadinessHistory) >= 5 {
			return errors.New("readiness history capacity exhausted")
		}
		marked.ReadinessHistory = append(marked.ReadinessHistory, ops.SWIMReadinessReceipt{Task: before.ReadinessTask, Claim: before.ReadinessClaim, Stage: string(before.ReadinessFor), SubmittedAt: metav1.NewTime(before.ReadinessNotBefore)})
	}
	marked.Phase, marked.ReadinessFor, marked.ReadinessTask, marked.ReadinessNotBefore = swimReadinessClaimed, next, "", e.now()
	marked.ReadinessClaim = claim
	if err := e.store.Replace(ctx, before, marked); err != nil {
		return err
	}
	marked, err = e.store.Load(ctx, before.Intent.OperationUID)
	if err != nil {
		return err
	}
	if marked.Intent != before.Intent || marked.Phase != swimReadinessClaimed || marked.ReadinessFor != next || marked.ReadinessNotBefore.IsZero() || marked.ReadinessClaim != claim {
		return errors.New("readiness dispatch marker changed")
	}
	if err := e.authority.Check(ctx, before.Intent, swimReadinessClaimed, claim); err != nil {
		return err
	}
	task, err := e.api.(modernSWIMAPI).StartReadinessCheck(ctx, before.Intent.ControllerDeviceID)
	reused := task.ID == before.DistributionTask || task.ID == before.ActivationTask
	for _, receipt := range marked.ReadinessHistory {
		if receipt.Task == task.ID {
			reused = true
		}
	}
	if err != nil || !validAPIID(task.ID) || reused {
		if persistErr := e.transition(ctx, marked, swimOutcomeUnknown); persistErr != nil {
			return persistErr
		}
		return errors.New("readiness submission outcome unknown; no automatic replay")
	}
	after := marked
	after.Phase, after.ReadinessTask = swimCheckingReadiness, task.ID
	return e.store.Replace(ctx, marked, after)
}

func (e *swimExecutor) checkReadiness(ctx context.Context, record swimRecord) error {
	if record.Intent.APIContract != swimModernContract || record.ReadinessClaim == "" || !validAPIID(record.ReadinessTask) || (record.ReadinessFor != swimReadyToDistribute && record.ReadinessFor != swimReadyToActivate) {
		return errors.New("invalid readiness receipt")
	}
	if record.Phase == swimReadyToDistribute || record.Phase == swimReadyToActivate {
		if record.Phase != record.ReadinessFor {
			return errors.New("readiness receipt is for another stage")
		}
	}
	items, err := e.api.(modernSWIMAPI).ListReadinessResults(ctx, record.Intent.ControllerDeviceID)
	if err != nil {
		return err
	}
	if record.Intent.StandardReloadProfile != "" {
		api, ok := e.api.(standardReloadAPI)
		if !ok {
			return errors.New("standard reload API unavailable")
		}
		if err := api.CheckStandardReloadProfile(ctx, record.Intent.ControllerDeviceID); err != nil {
			return err
		}
		return validateStandardReloadReadiness(items, record.Intent, record.ReadinessTask, record.ReadinessNotBefore, e.now())
	}
	return validateXEReadiness(items, record.Intent.ControllerDeviceID, record.ReadinessTask, record.Intent.TransferFallbackAddress, record.ReadinessNotBefore, e.now())
}

func (e *swimExecutor) observeReadiness(ctx context.Context, record swimRecord) error {
	if record.Intent.APIContract != swimModernContract {
		return errors.New("readiness requires modern SWIM contract")
	}
	body, err := e.api.GetTask(ctx, record.ReadinessTask)
	if err != nil {
		return err
	}
	state, err := parseSWIMTask(body, record.ReadinessTask)
	if err != nil {
		return err
	}
	if state.failed {
		return e.transition(ctx, record, swimOutcomeUnknown)
	}
	if !state.complete {
		return nil
	}
	if err := e.checkReadiness(ctx, record); err != nil {
		// Only a positively completed task can be replaced, and only after the
		// device owner issues a new grant (for example, explicit pause/resume).
		// An ambiguous POST or an in-flight check never enters this path.
		claim, claimErr := e.authority.Claim(ctx, record.Intent, swimReadinessClaimed)
		if claimErr != nil || claim == "" || claim == record.ReadinessClaim {
			return err
		}
		return e.startReadiness(ctx, record, record.ReadinessFor)
	}
	return e.transition(ctx, record, record.ReadinessFor)
}
