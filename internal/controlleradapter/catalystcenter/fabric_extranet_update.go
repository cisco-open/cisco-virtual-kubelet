// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func extranetExpansion(want, got fabricExtranetPolicy) bool {
	if want.Name != got.Name || want.Provider != got.Provider || len(want.Fabrics) == 0 || len(got.Fabrics) == 0 {
		return false
	}
	for _, s := range got.Subscribers {
		if !slices.Contains(want.Subscribers, s) {
			return false
		}
	}
	for _, f := range got.Fabrics {
		if !slices.Contains(want.Fabrics, f) {
			return false
		}
	}
	return true
}
func (s *fabricExtranetSnapshot) updateResource(id string) string {
	return "extranet-update:" + id + ":" + s.digest
}

// Only additive updates have a recoverable footprint derivable entirely from
// the desired model: it contains every old and new VN/fabric. Removals require
// separately retained old-scope evidence and are not silently treated as updates.
// ownership is rechecked before PUT, but is not a physical dispatch permit.
func (c *client) planExtranetExpansion(ctx context.Context, want fabricExtranetIntent, ownership func(context.Context, string) error) (*configurationWrite, error) {
	if ownership == nil {
		return nil, errConfigurationOwned
	}
	before, err := c.extranetSnapshot(ctx, want)
	if err != nil {
		return nil, err
	}
	got, exists := before.policies[before.desired.Name]
	if !exists {
		return nil, errConfigurationStale
	}
	if err := ownership(ctx, got.ID); err != nil {
		return nil, err
	}
	if !extranetExpansion(before.desired, got) {
		return nil, errConfigurationTransition
	}
	if extranetPolicyMatches(before.desired, got) {
		return nil, nil
	}
	desired := before.desired
	desired.ID = got.ID
	body, err := json.Marshal([]fabricExtranetPolicy{desired})
	if err != nil {
		return nil, err
	}
	want.policy.Subscribers = append([]string{}, want.policy.Subscribers...)
	want.paths = append([]string{}, want.paths...)
	return &configurationWrite{resource: before.updateResource(got.ID), action: "Update", identity: configurationResourceIdentity{target: got.ID}, submit: func(ctx context.Context) (Task, error) {
		fresh, err := c.extranetSnapshot(ctx, want)
		if err != nil {
			return Task{}, err
		}
		if fresh.digest != before.digest || !reflect.DeepEqual(before.policies, fresh.policies) {
			return Task{}, errConfigurationStale
		}
		if err := ownership(ctx, got.ID); err != nil {
			return Task{}, err
		}
		raw, err := c.mutate(ctx, http.MethodPut, fabricExtranetPath, body)
		if err != nil {
			return Task{}, err
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			return Task{}, errInvalidResponse
		}
		return Task{ID: receipt.Response.ID}, nil
	}}, nil
}

// Bind the native primitive to retained controller ownership and source revision.
// Public dispatch remains gated on the complete group reservation coordinator.
func (a *adapter) planOwnedExtranetExpansion(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want fabricExtranetIntent) (*configurationWrite, error) {
	if config == nil || !supportedConfigurationApply(config) || !slices.Contains(config.Spec.ManagedSections, "fabric") {
		return nil, errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	uid, generation := nc.UID, nc.Generation
	ownership := func(ctx context.Context, id string) error {
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if current.UID != uid || current.Generation != generation {
			return errConfigurationStale
		}
		owned, err := ownedExtranetPolicy(current.Status.Configuration, config.Name, string(config.UID), want.policy.Name, id, time.Now())
		if err != nil {
			return err
		}
		if !owned {
			return errConfigurationOwned
		}
		return nil
	}
	return a.client.planExtranetExpansion(ctx, want, ownership)
}
func (c *client) verifiedExtranetExpansionSnapshot(ctx context.Context, want fabricExtranetIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (*fabricExtranetSnapshot, error) {
	if op == nil || op.Phase != "Verifying" || op.Action != "Update" || !validAPIID(op.TargetID) || op.ParentID != "" || op.TargetSerial != "" || op.SubmittedAt == nil || len(op.CredentialVersions) != 0 {
		return nil, errConfigurationTransition
	}
	complete, err := c.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || !complete {
		return nil, err
	}
	snapshot, err := c.extranetSnapshot(ctx, want)
	if err != nil {
		return nil, err
	}
	nativeResource, err := extranetUpdateNativeResource(op.Resource)
	if err != nil || nativeResource != snapshot.updateResource(op.TargetID) {
		return nil, errConfigurationStale
	}
	got, exists := snapshot.policies[snapshot.desired.Name]
	if !exists || got.ID != op.TargetID || !extranetPolicyMatches(snapshot.desired, got) {
		return nil, nil
	}
	return snapshot, nil
}

func (c *client) verifyExtranetExpansion(ctx context.Context, want fabricExtranetIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	snapshot, err := c.verifiedExtranetExpansionSnapshot(ctx, want, op, now)
	return snapshot != nil, err
}
