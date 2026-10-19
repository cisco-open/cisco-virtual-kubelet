// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"sync/atomic"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

// Shared frozen, one-use native sender. Product preflight and the caller's
// non-reconstructible group permit guard remain separate from HTTP transport.
// Authority is checked last, with no native read between it and mutation.
func (c *client) planLANLinkMutation(want lanLinkIntent, resource string, identity configurationResourceIdentity, check, guard func(context.Context) error) (*configurationWrite, error) {
	if check == nil || guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	path, payload, err := lanLinkPayload(want)
	if err != nil {
		return nil, err
	}
	route, err := url.Parse(path)
	if err != nil {
		return nil, err
	}
	var used atomic.Bool
	return &configurationWrite{resource: resource, action: "Update", identity: identity, submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		if err := check(ctx); err != nil {
			return Task{}, err
		}
		if err := guard(ctx); err != nil {
			return Task{}, err
		}
		raw, err := c.mutateQuery(ctx, http.MethodPut, route.Path, route.Query(), payload)
		if err != nil {
			return Task{}, err
		}
		var response struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &response) != nil || !validAPIID(response.Response.ID) {
			return Task{}, errInvalidResponse
		}
		return Task{ID: response.Response.ID}, nil
	}}, nil
}

// Planning creates no operation or permit. The shared private lifecycle obtains
// all-member authority; native absence never establishes owned completion.
func (a *adapter) planManagedLANLinkDelete(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want lanLinkIntent, guard func(context.Context) error) (*configurationWrite, error) {
	if a == nil || guard == nil || !a.configurationGroupAdmissionReady() {
		return nil, errConfigurationDeviceAdmission
	}
	before, targets, err := a.observeLANLinkDelete(ctx, config, source, want, time.Now())
	if err != nil {
		return nil, err
	}
	resource := lanLinkDeleteResource(before)
	check := func(ctx context.Context) error {
		fresh, current, err := a.observeLANLinkDelete(ctx, config, source, want, time.Now())
		if err != nil {
			return err
		}
		if fresh.observationDigest() != before.observationDigest() || !reflect.DeepEqual(targets, current) {
			return errConfigurationStale
		}
		return nil
	}
	endpoints := before.adjacency.endpoints
	return a.client.planLANLinkMutation(want, resource, configurationResourceIdentity{target: endpoints.primary.device.ID, parent: endpoints.secondary.device.ID, groupTargets: targets}, check, guard)
}
