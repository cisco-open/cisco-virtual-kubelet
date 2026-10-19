// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"sync/atomic"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const anycastBulkPrefix = "anycast-gateway-bulk:v1:"

type anycastBulkEntry struct {
	want fabricAnycastIntent
	deps anycastGatewayDependencies
	body map[string]any
}
type anycastBulkSnapshot struct {
	fabric  string
	entries []anycastBulkEntry
	mask    []byte
}

func (c *client) observeAnycastBulk(ctx context.Context, desired []fabricAnycastIntent) (*anycastBulkSnapshot, error) {
	if len(desired) == 0 || len(desired) > 1000 {
		return nil, errConfigurationTransition
	}
	wants := canonicalAnycastMembers(desired)
	for i, want := range wants {
		if !validSitePath(want.path) || i > 0 && want.path == wants[i-1].path && want.pool == wants[i-1].pool {
			return nil, errConfigurationTransition
		}
		if want.payload["ipPoolName"] != want.pool || want.payload["virtualNetworkName"] != want.network {
			return nil, errConfigurationTransition
		}
		if _, err := json.Marshal(want.payload); err != nil {
			return nil, errInvalidResponse
		}
	}
	inv, err := c.loadAnycastGatewayInventory(ctx, wants)
	if err != nil {
		return nil, err
	}
	// Request-local allocation reservations let the existing VLAN checker also
	// see earlier pending members, without treating them as native readback.
	inv.gateways = maps.Clone(inv.gateways)
	out := &anycastBulkSnapshot{mask: make([]byte, (len(wants)+7)/8)}
	for i, want := range wants {
		deps, err := inv.resolve(want)
		if err != nil {
			return nil, err
		}
		if out.fabric == "" {
			out.fabric = deps.fabricID
		}
		if out.fabric != deps.fabricID {
			return nil, errConfigurationTransition
		}
		if _, err := c.anycastGatewayWrite(want, deps); err != nil {
			return nil, err
		}
		body, err := anycastResolvedPayload(want, deps)
		if err != nil {
			return nil, err
		}
		out.entries = append(out.entries, anycastBulkEntry{want, deps, body})
		if !deps.exists {
			out.mask[i/8] |= 1 << uint(i%8)
			id, name, err := resolvedAnycastVLANAllocation(want, deps)
			if err != nil {
				return nil, err
			}
			inv.gateways[anycastGatewayKey{deps.fabricID, want.pool}] = anycastGateway{FabricID: deps.fabricID, Pool: want.pool, VLAN: id, VLANName: name}
		}
	}
	return out, nil
}

func (s *anycastBulkSnapshot) resource(mask []byte) string {
	type binding struct {
		Path, Parent, Pool, Dependency, ExistingID string
		Body                                       map[string]any
	}
	bindings := []binding{}
	for i, e := range s.entries {
		id := ""
		if mask[i/8]&(1<<uint(i%8)) == 0 {
			id = e.deps.actual.ID
		}
		bindings = append(bindings, binding{e.want.path, e.want.parent, e.want.pool, e.deps.digest, id, e.body})
	}
	raw, _ := json.Marshal(bindings)
	return fmt.Sprintf("%s%d:%s:%x", anycastBulkPrefix, len(bindings), hex.EncodeToString(mask), sha256.Sum256(raw))
}

// One immutable native batch and one external authority check, bracketed by
// fresh full dependency observations. This is not public runtime admission.
func (c *client) planAnycastGatewayBulk(ctx context.Context, desired []fabricAnycastIntent, guard func(context.Context) error) (*configurationWrite, error) {
	if len(desired) > anycastBulkRequestLimit {
		return nil, errConfigurationTransition
	}
	// Freeze the closed parser's payload types, including ordered secondary pools.
	wants := append([]fabricAnycastIntent(nil), desired...)
	for i := range wants {
		wants[i].payload = maps.Clone(wants[i].payload)
		if pools, ok := wants[i].payload["additionalIpPools"].([]map[string]any); ok {
			copy := make([]map[string]any, len(pools))
			for j, pool := range pools {
				copy[j] = maps.Clone(pool)
			}
			wants[i].payload["additionalIpPools"] = copy
		}
	}
	before, err := c.observeAnycastBulk(ctx, wants)
	if err != nil {
		return nil, err
	}
	selected := false
	for _, b := range before.mask {
		selected = selected || b != 0
	}
	if !selected {
		return nil, nil
	}
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	check := func(ctx context.Context) error {
		fresh, err := c.observeAnycastBulk(ctx, wants)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(before, fresh) {
			return errConfigurationStale
		}
		return nil
	}
	var used atomic.Bool
	return &configurationWrite{resource: before.resource(before.mask), action: "Create", identity: configurationResourceIdentity{parent: before.fabric}, submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		if err := check(ctx); err != nil {
			return Task{}, err
		}
		if err := guard(ctx); err != nil {
			return Task{}, err
		}
		if err := check(ctx); err != nil {
			return Task{}, err
		}
		body := []map[string]any{}
		for i, e := range before.entries {
			if before.mask[i/8]&(1<<uint(i%8)) != 0 {
				body = append(body, e.body)
			}
		}
		return c.submitTask(ctx, anycastGatewaysPath, body)
	}}, nil
}

func (c *client) verifyAnycastGatewayBulk(ctx context.Context, desired []fabricAnycastIntent, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op != nil && strings.HasPrefix(op.Resource, anycastBulkChunkPrefix) {
		return c.verifyAnycastGatewayChunk(ctx, desired, op)
	}
	if op == nil || op.Action != "Create" || op.TargetID != "" || op.TargetSerial != "" || !validAPIID(op.ParentID) || op.PhysicalGroup != nil || len(op.CredentialVersions) != 0 || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil {
		return false, errConfigurationTransition
	}
	receipt, err := decodeAnycastBulkReceipt(op.Resource)
	if err != nil || receipt.version != 1 || receipt.count != len(desired) {
		return false, errConfigurationTransition
	}
	mask := receipt.mask

	fresh, err := c.observeAnycastBulk(ctx, desired)
	if err != nil {
		return false, err
	}
	if fresh.fabric != op.ParentID || fresh.resource(mask) != op.Resource {
		return false, errConfigurationStale
	}
	for _, e := range fresh.entries {
		if !e.deps.exists {
			return false, nil
		}
	}
	return true, nil
}
