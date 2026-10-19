// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"time"
)

// Execution evidence deliberately has no configuration-complete or release
// flag. Native task completion and stable keyed readback must still be compared
// with every selected setting by an independently qualified verifier.
type accessPointConfigurationExecutionObservation struct {
	taskCompletedAt time.Time
	members         []accessPointObservation
}

// Reconstruct observation after restart from declared intent, its retained
// digest and accepted task, without retaining/reusing a submission closure.
// Guard must check the exact accepted journal/source/owner and member bindings;
// it is a read authorization check, not a new physical mutation permit.
// This never submits a request, writes a receipt or changes operation phase.
func (c *client) observeAccessPointConfigurationExecution(ctx context.Context, wants []accessPointDeviceIntent, digest, taskID string, submitted, now time.Time, guard func(context.Context, string, []accessPointConfigurationMember) error) (*accessPointConfigurationExecutionObservation, error) {
	if guard == nil || !validAPIID(taskID) || submitted.IsZero() || now.Before(submitted) {
		return nil, errConfigurationTransition
	}
	frozen, err := freezeAccessPointConfiguration(wants)
	if err != nil {
		return nil, err
	}
	snapshot, err := c.accessPointConfigurationSnapshot(ctx, frozen)
	if err != nil {
		return nil, err
	}
	if snapshot.digest != digest {
		return nil, errConfigurationStale
	}
	authorize := func() error {
		return guard(ctx, digest, append([]accessPointConfigurationMember(nil), snapshot.members...))
	}
	if err := authorize(); err != nil {
		return nil, err
	}
	completed, err := c.configurationTaskTreeCompletion(ctx, taskID, submitted, now)
	if err != nil || completed.IsZero() {
		return nil, err
	}
	if completed.After(now) {
		return nil, errConfigurationStale
	}
	read := func() ([]accessPointObservation, error) {
		rows := make([]accessPointObservation, 0, len(snapshot.members))
		for _, member := range snapshot.members {
			row, err := c.accessPointConfiguration(ctx, member.EthernetMAC)
			if err != nil {
				return nil, err
			}
			rows = append(rows, row)
		}
		return rows, nil
	}
	rows, err := read()
	if err != nil {
		return nil, err
	}
	fresh, err := c.accessPointConfigurationSnapshot(ctx, frozen)
	if err != nil {
		return nil, err
	}
	if fresh.digest != digest {
		return nil, errConfigurationStale
	}
	if err := authorize(); err != nil {
		return nil, err
	}
	latest, err := read()
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(rows, latest) {
		return nil, errConfigurationStale
	}
	// Authorization and native identities are checked again after the keyed
	// observations; the final complete task-tree read also catches child failures
	// introduced while authorization or identity reads were in progress.
	if err := authorize(); err != nil {
		return nil, err
	}
	fresh, err = c.accessPointConfigurationSnapshot(ctx, frozen)
	if err != nil {
		return nil, err
	}
	if fresh.digest != digest {
		return nil, errConfigurationStale
	}
	ended, err := c.configurationTaskTreeCompletion(ctx, taskID, submitted, now)
	if err != nil || ended.IsZero() {
		return nil, err
	}
	if !ended.Equal(completed) {
		return nil, errConfigurationStale
	}
	return &accessPointConfigurationExecutionObservation{taskCompletedAt: completed, members: latest}, nil
}
