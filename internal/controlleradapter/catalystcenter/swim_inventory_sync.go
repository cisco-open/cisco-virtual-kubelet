// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"net/netip"
	"time"
)

type swimPreparationAuthority interface {
	PreparationReceipt(context.Context, swimIntent, swimPhase) (string, error)
}
type swimInventoryAPI interface {
	StartInventorySync(context.Context, string) (Task, error)
	ObserveInventorySync(context.Context, string, string, time.Time, time.Time) (bool, error)
}

func (c *client) StartInventorySync(ctx context.Context, deviceID string) (Task, error) {
	if !validAPIID(deviceID) {
		return Task{}, errInvalidResponse
	}
	body, _ := json.Marshal([]string{deviceID})
	raw, err := c.mutate(ctx, http.MethodPut, devicesPath+"/sync", body)
	if err != nil {
		return Task{}, err
	}
	var env struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &env) != nil || !validAPIID(env.Response.ID) {
		return Task{}, errInvalidResponse
	}
	return env.Response, nil
}

func (c *client) ObserveInventorySync(ctx context.Context, deviceID, taskID string, submitted, now time.Time) (bool, error) {
	if !validAPIID(deviceID) || !validAPIID(taskID) {
		return false, errInvalidResponse
	}
	devices, err := c.ListDevices(ctx)
	if err != nil {
		return false, err
	}
	ip := ""
	count := 0
	for _, d := range devices {
		if d.ID == deviceID {
			ip = d.ManagementIP
			count++
		}
	}
	if _, err := netip.ParseAddr(ip); err != nil || count != 1 {
		return false, errInvalidResponse
	}
	raw, err := c.Get(ctx, taskPath+taskID+"/tree", nil)
	if err != nil {
		return false, err
	}
	return inventorySyncComplete(raw, taskID, ip, submitted, now)
}

// Parent success alone is insufficient: require the exact device child. NCIM
// audit entries may remain open after Inventory service has completed.
func inventorySyncComplete(raw []byte, taskID, ip string, submitted, now time.Time) (bool, error) {
	var env struct {
		Response []struct {
			ID       string `json:"id"`
			Parent   string `json:"parentId"`
			Root     string `json:"rootId"`
			Service  string `json:"serviceType"`
			Progress string `json:"progress"`
			Error    *bool  `json:"isError"`
			Start    int64  `json:"startTime"`
			End      int64  `json:"endTime"`
		} `json:"response"`
	}
	if submitted.IsZero() || submitted.After(now) || json.Unmarshal(raw, &env) != nil || env.Response == nil {
		return false, errInvalidResponse
	}
	parents, children := 0, 0
	complete := true
	seen := map[string]bool{}
	for _, t := range env.Response {
		if !validAPIID(t.ID) || seen[t.ID] || t.Root != taskID || t.Error == nil || *t.Error {
			return false, errInvalidResponse
		}
		seen[t.ID] = true
		if t.Service != "Inventory service" {
			continue
		}
		if t.Start < submitted.UnixMilli() || t.Start > now.UnixMilli() {
			return false, errInvalidResponse
		}
		if t.End == 0 {
			complete = false
		} else if t.End < t.Start || t.End > now.UnixMilli() {
			return false, errInvalidResponse
		}
		if t.ID == taskID && t.Parent == "" {
			parents++
		} else if t.Parent == taskID && t.Progress == "Synced device: "+ip+" Status: SUCCESS" {
			children++
		} else {
			return false, errInvalidResponse
		}
	}
	if parents > 1 || children > 1 {
		return false, errInvalidResponse
	}
	return complete && parents == 1 && children == 1, nil
}

func (e *swimExecutor) requestPreparation(ctx context.Context, before swimRecord, stage swimPhase) error {
	if _, ok := e.authority.(swimPreparationAuthority); !ok {
		return errors.New("device preparation authority unavailable")
	}
	if _, ok := e.api.(swimInventoryAPI); !ok {
		return errors.New("inventory synchronization API unavailable")
	}
	after := before
	after.ReadinessHistory = append([]ops.SWIMReadinessReceipt(nil), before.ReadinessHistory...)
	if before.ReadinessTask != "" {
		if len(after.ReadinessHistory) >= 5 {
			return errors.New("readiness history exhausted")
		}
		after.ReadinessHistory = append(after.ReadinessHistory, ops.SWIMReadinessReceipt{Task: before.ReadinessTask, Claim: before.ReadinessClaim, Stage: string(before.ReadinessFor), SubmittedAt: metav1.NewTime(before.ReadinessNotBefore)})
	}
	after.Phase, after.ReadinessFor = swimPreparing, stage
	after.ReadinessTask, after.ReadinessClaim, after.ReadinessNotBefore = "", "", time.Time{}
	return e.store.Replace(ctx, before, after)
}
func (e *swimExecutor) startInventorySync(ctx context.Context, before swimRecord) error {
	a, ok := e.authority.(swimPreparationAuthority)
	if !ok {
		return errors.New("preparation authority unavailable")
	}
	api, ok := e.api.(swimInventoryAPI)
	if !ok {
		return errors.New("inventory API unavailable")
	}
	id, err := a.PreparationReceipt(ctx, before.Intent, before.ReadinessFor)
	if err != nil {
		return err
	}
	if id == "" {
		return errors.New("device preparation pending")
	}
	if len(before.InventorySyncs) >= 2 {
		return errors.New("inventory synchronization budget exhausted")
	}
	for _, s := range before.InventorySyncs {
		if s.Stage == string(before.ReadinessFor) {
			return errors.New("inventory synchronization already claimed")
		}
	}
	claim, err := e.authority.Claim(ctx, before.Intent, swimInventorySyncClaimed)
	if err != nil {
		return err
	}
	if claim == "" {
		return errors.New("empty inventory claim")
	}
	marked := before
	marked.Phase = swimInventorySyncClaimed
	marked.InventorySyncs = append(append([]ops.SWIMInventorySync(nil), before.InventorySyncs...), ops.SWIMInventorySync{Stage: string(before.ReadinessFor), PreparationID: id, Claim: claim, SubmittedAt: metav1.NewTime(e.now())})
	if err = e.store.Replace(ctx, before, marked); err != nil {
		return err
	}
	marked, err = e.store.Load(ctx, before.Intent.OperationUID)
	if err != nil {
		return err
	}
	if marked.Phase != swimInventorySyncClaimed || marked.Intent != before.Intent || len(marked.InventorySyncs) != len(before.InventorySyncs)+1 {
		return errors.New("inventory dispatch marker changed")
	}
	last := marked.InventorySyncs[len(marked.InventorySyncs)-1]
	if last.PreparationID != id || last.Claim != claim || last.Stage != string(before.ReadinessFor) {
		return errors.New("inventory receipt changed")
	}
	if err = e.authority.Check(ctx, before.Intent, swimInventorySyncClaimed, claim); err != nil {
		return err
	}
	fresh, err := a.PreparationReceipt(ctx, before.Intent, before.ReadinessFor)
	if err != nil || fresh != id {
		return errors.New("device preparation changed before synchronization")
	}
	task, err := api.StartInventorySync(ctx, before.Intent.ControllerDeviceID)
	reused := task.ID == before.DistributionTask || task.ID == before.ActivationTask || task.ID == before.ReadinessTask
	for _, s := range before.InventorySyncs {
		reused = reused || s.Task == task.ID
	}
	for _, s := range before.ReadinessHistory {
		reused = reused || s.Task == task.ID
	}
	if err != nil || !validAPIID(task.ID) || reused {
		return e.transition(ctx, marked, swimOutcomeUnknown)
	}
	after := marked
	after.InventorySyncs = append([]ops.SWIMInventorySync(nil), marked.InventorySyncs...)
	after.InventorySyncs[len(after.InventorySyncs)-1].Task = task.ID
	after.Phase = swimSyncingInventory
	return e.store.Replace(ctx, marked, after)
}
func (e *swimExecutor) observeInventorySync(ctx context.Context, before swimRecord) error {
	api, ok := e.api.(swimInventoryAPI)
	if !ok || len(before.InventorySyncs) == 0 {
		return errors.New("inventory receipt missing")
	}
	last := before.InventorySyncs[len(before.InventorySyncs)-1]
	if last.Stage != string(before.ReadinessFor) || last.PreparationID == "" || last.Claim == "" || !validAPIID(last.Task) {
		return errors.New("inventory receipt invalid")
	}
	if last.CompletedAt != nil {
		return e.startReadiness(ctx, before, before.ReadinessFor)
	}
	done, err := api.ObserveInventorySync(ctx, before.Intent.ControllerDeviceID, last.Task, last.SubmittedAt.Time, e.now())
	if err != nil || !done {
		return err
	}
	after := before
	after.InventorySyncs = append([]ops.SWIMInventorySync(nil), before.InventorySyncs...)
	now := metav1.NewTime(e.now())
	after.InventorySyncs[len(after.InventorySyncs)-1].CompletedAt = &now
	return e.store.Replace(ctx, before, after)
}

func (b *handoffBridge) PreparationReceipt(ctx context.Context, i swimIntent, stage swimPhase) (string, error) {
	if stage != swimReadyToDistribute && stage != swimReadyToActivate {
		return "", errors.New("invalid preparation stage")
	}
	if err := b.Hold(ctx, i); err != nil {
		return "", err
	}
	up, _, err := b.bound(ctx, i)
	if err != nil {
		return "", err
	}
	for _, p := range up.Status.ControllerHandoff.Preparation {
		if p.Stage == string(stage) && p.Phase == "Complete" && p.CompletedAt != nil && !p.CompletedAt.After(time.Now()) && time.Since(p.CompletedAt.Time) <= 15*time.Minute && p.PolicySHA256 == i.PreparationPolicySHA256 && p.ID != "" {
			return p.ID, nil
		}
	}
	return "", nil
}
