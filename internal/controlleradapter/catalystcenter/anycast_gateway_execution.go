// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func (w fabricAnycastIntent) resource(deps anycastGatewayDependencies) string {
	return w.resourcePrefix() + deps.digest
}
func (w fabricAnycastIntent) resourcePrefix() string {
	return fmt.Sprintf("anycast-gateway:%x:", sha256.Sum256([]byte(w.path+"\x00"+w.pool)))
}
func anycastCreatePayload(want fabricAnycastIntent, deps anycastGatewayDependencies) (map[string]any, error) {
	if err := validateAnycastCreateIntent(want); err != nil {
		return nil, err
	}
	return anycastResolvedPayload(want, deps)
}

func anycastResolvedPayload(want fabricAnycastIntent, deps anycastGatewayDependencies) (map[string]any, error) {
	if !validAPIID(deps.fabricID) || deps.digest == "" || deps.pendingParent {
		return nil, errInvalidResponse
	}
	body := map[string]any{}
	for k, v := range want.payload {
		body[k] = v
	}
	body["fabricId"] = deps.fabricID
	if deps.parent != nil {
		if id, present := body["vlanId"]; present && id != deps.parent.VLAN {
			return nil, errConfigurationStale
		}
		body["vlanId"] = deps.parent.VLAN
		if body["autoGenerateVlanName"] == false {
			body["vlanName"] = deps.parent.VLANName
		}
	}
	return body, nil
}
func validateAnycastCreateIntent(want fabricAnycastIntent) error {
	// Resolved ordinary-pool manifests must supply native required booleans.
	// INFRA_VN omits those fields, as the pinned provider does.
	if want.network != "INFRA_VN" {
		for _, key := range []string{"isCriticalPool", "isLayer2FloodingEnabled", "isWirelessPool", "isIpDirectedBroadcast", "isIntraSubnetRoutingEnabled", "isMultipleIpToMacAddresses"} {
			if _, ok := want.payload[key].(bool); !ok {
				return errInvalidResponse
			}
		}
	}
	return nil
}
func (c *client) planAnycastGateway(ctx context.Context, want fabricAnycastIntent) (*configurationWrite, error) {
	deps, err := c.anycastGatewayDependencies(ctx, want)
	if err != nil {
		return nil, err
	}
	return c.anycastGatewayWrite(want, deps)
}
func (c *client) anycastGatewayWrite(want fabricAnycastIntent, deps anycastGatewayDependencies) (*configurationWrite, error) {
	if deps.exists {
		matches, err := anycastGatewayMatches(want.anycastGatewayIntent, deps.actual, deps.pools, deps.parent)
		if err != nil {
			return nil, err
		}
		if !matches {
			return nil, errConfigurationTransition
		}
		return nil, nil
	}
	body, err := anycastCreatePayload(want, deps)
	if err != nil {
		return nil, err
	}
	return &configurationWrite{resource: want.resource(deps), action: "Create", identity: configurationResourceIdentity{parent: deps.fabricID}, submit: func(ctx context.Context) (Task, error) {
		latest, err := c.anycastGatewayDependencies(ctx, want)
		if err != nil {
			return Task{}, err
		}
		if latest.digest != deps.digest || latest.exists {
			return Task{}, errConfigurationStale
		}
		return c.submitTask(ctx, anycastGatewaysPath, []map[string]any{body})
	}}, nil
}
func (c *client) verifyAnycastGateway(ctx context.Context, want fabricAnycastIntent, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Action != "Create" || op.TargetID != "" || !validAPIID(op.ParentID) || op.TargetSerial != "" || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	deps, err := c.anycastGatewayDependencies(ctx, want)
	if err != nil {
		return false, err
	}
	if op.ParentID != deps.fabricID || op.Resource != want.resource(deps) {
		return false, errConfigurationStale
	}
	if !deps.exists {
		return false, nil
	}
	return anycastGatewayMatches(want.anycastGatewayIntent, deps.actual, deps.pools, deps.parent)
}
