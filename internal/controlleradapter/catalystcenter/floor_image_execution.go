// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Image writes use the shared journal and its dedicated completion receipt.
type floorImageExecutor struct {
	client                *client
	desired               []floorImageIntent
	configName, configUID string
	receipts              []cvk.NetworkControllerConfigurationActionApplication
	synchronous           bool
	declaration           *cvk.NetworkControllerConfigurationOperation
}

func (e *floorImageExecutor) count() int { return len(e.desired) }
func (e *floorImageExecutor) floors(ctx context.Context) (map[string]string, error) {
	sites, err := e.client.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	floors := map[string]string{}
	for _, site := range sites {
		if site.Type == "floor" {
			floors[site.NameHierarchy] = site.ID
		}
	}
	return floors, nil
}
func (e *floorImageExecutor) next(ctx context.Context) (*configurationWrite, error) {
	floors, err := e.floors(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var first *configurationWrite
	for _, want := range e.desired {
		if seen[want.floor] {
			return nil, errFloorImage
		}
		seen[want.floor] = true
		if _, err := validateFloorImage(want.filename, want.content); err != nil {
			return nil, err
		}
		if fmt.Sprintf("%x", sha256.Sum256(want.content)) != want.digest {
			return nil, errConfigurationStale
		}
		floorID := floors[want.floor]
		applied, err := floorImageApplied(want, floorID, e.configName, e.configUID, e.receipts, time.Now())
		if err != nil {
			return nil, err
		}
		if err := floorImageNewDeclaration(want, e.receipts, e.declaration, time.Now()); err != nil {
			return nil, err
		}
		if applied || first != nil {
			continue
		}
		checkFloor := func(ctx context.Context) error {
			fresh, err := e.floors(ctx)
			if err != nil {
				return err
			}
			if fresh[want.floor] != floorID {
				return errConfigurationStale
			}
			return nil
		}
		first = &configurationWrite{resource: want.operationResource(), action: "Update", identity: configurationResourceIdentity{target: floorID}, submit: func(ctx context.Context) (Task, error) {
			if err := checkFloor(ctx); err != nil {
				return Task{}, err
			}
			return e.client.uploadFloorImage(ctx, floorID, want.filename, want.content)
		}}
		if e.synchronous {
			first.resource = want.synchronousOperationResource()
			first.submit = nil
			first.submitCompleted = func(ctx context.Context) error {
				if err := checkFloor(ctx); err != nil {
					return err
				}
				return e.client.uploadFloorImageCompleted(ctx, floorID, want.floor, want.filename, want.content)
			}
		}
	}
	return first, nil
}

// This proves a successful legacy task or a validated synchronous-response
// boundary and floor identity, not remote byte equality. Both modes use the
// dedicated retained receipt rather than generic attribute verification.
func (e *floorImageExecutor) uploadCompleted(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Phase != "Verifying" || op.Section != "sites" || op.Action != "Update" || op.ConfigUID != e.configUID || op.ConfigName != e.configName || !configurationOperationID.MatchString(op.ID) || !validAPIID(op.TargetID) || !floorImageCompletionKindMatches(op) || op.TargetSerial != "" || op.ParentID != "" || len(op.CredentialVersions) != 0 || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) || op.CreatedResourceID != "" || op.DependencyVersions != nil || op.PhysicalGroup != nil || op.CancellationRequestedAt != nil {
		return false, errConfigurationTransition
	}
	var intent *floorImageIntent
	for i := range e.desired {
		if e.desired[i].matchesOperation(op.Resource) {
			if intent != nil {
				return false, errConfigurationTransition
			}
			intent = &e.desired[i]
		}
	}
	if intent == nil {
		return false, errConfigurationStale
	}
	if !strings.HasPrefix(op.Resource, "floor-image-sync:") {
		complete, err := e.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
		if err != nil || !complete {
			return false, err
		}
	}
	floors, err := e.floors(ctx)
	if err != nil {
		return false, err
	}
	if floors[intent.floor] != op.TargetID {
		return false, errConfigurationStale
	}
	return true, nil
}
