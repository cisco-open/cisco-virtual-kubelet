// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"strings"
	"sync/atomic"
)

func multicastVNResource(path, name, dependencies string) string {
	return fmt.Sprintf("fabric-multicast-vn:%x:%s", sha256.Sum256([]byte(path+"\x00"+name)), dependencies)
}

// Only the selected VN may change while its task is outstanding. Other declared
// VNs remain dependencies just like unmanaged VNs; omission from this binding
// would let a peer change go unnoticed during recovery.
func multicastVNOperationResource(path, name string, snapshot *fabricMulticastSnapshot) string {
	action := "Create"
	if _, exists := snapshot.before[name]; exists {
		action = "Update"
	}
	return multicastVNResultResource(path, name, snapshot, action) + ":" + snapshot.digest
}

func multicastVNResultResource(path, name string, snapshot *fabricMulticastSnapshot, action string) string {
	peers := map[string]map[string]any{}
	for peer, row := range snapshot.before {
		if peer != name {
			peers[peer] = row.fields
		}
	}
	nativeID := ""
	if action == "Update" {
		nativeID = snapshot.before[name].ID
	}
	raw, _ := json.Marshal([]any{snapshot.dependencyDigest, peers, nativeID})
	return multicastVNResource(path, name, fmt.Sprintf("%x", sha256.Sum256(raw)))
}

// Private native primitive. Runtime callers still require physical mutation
// admission and the shared configuration journal; this does not acquire leases.
func (c *client) planGuardedFabricMulticastVN(ctx context.Context, path string, wants []fabricMulticastVNIntent, name string, guard func(context.Context) error) (*configurationWrite, error) {
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	snapshot, err := c.multicastSnapshot(ctx, path, wants)
	if err != nil {
		return nil, err
	}
	var want *fabricMulticastVNIntent
	for i := range wants {
		if wants[i].name == name {
			want = &wants[i]
			break
		}
	}
	if want == nil {
		return nil, errInvalidResponse
	}
	// Both actions retain the existing L3 VN anchor so create-to-update receipt
	// replacement preserves the shared logical target. Native update UUIDs are
	// bound in result evidence; create UUIDs are recovered after completion.
	action, method, target := "Create", http.MethodPost, snapshot.networkIDs[name]
	if !validAPIID(target) {
		return nil, errConfigurationStale
	}
	if before, exists := snapshot.before[name]; exists {
		same, err := fabricMulticastMatches(*want, snapshot.fabricID, snapshot.nameIDs, before)
		if err != nil {
			return nil, err
		}
		if same {
			return nil, nil
		}
		action, method = "Update", http.MethodPut
	}
	body, err := json.Marshal([]map[string]any{snapshot.bodies[name]})
	if err != nil {
		return nil, err
	}
	var used atomic.Bool
	return &configurationWrite{resource: multicastVNOperationResource(path, name, snapshot), action: action, identity: configurationResourceIdentity{target: target, parent: snapshot.fabricID}, submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		latest, err := c.multicastSnapshot(ctx, path, wants)
		if err != nil {
			return Task{}, err
		}
		if latest.digest != snapshot.digest {
			return Task{}, errConfigurationStale
		}
		if err := guard(ctx); err != nil {
			return Task{}, err
		}
		raw, err := c.mutate(ctx, method, fabricMulticastVNsPath, body)
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

func (c *client) verifyFabricMulticastVN(ctx context.Context, path string, wants []fabricMulticastVNIntent, name string, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Action != "Create" && op.Action != "Update" || !validAPIID(op.ParentID) || op.TargetSerial != "" || len(op.CredentialVersions) != 0 || !validAPIID(op.TargetID) {
		return false, errConfigurationTransition
	}
	snapshot, err := c.multicastSnapshot(ctx, path, wants)
	if err != nil {
		return false, err
	}
	return verifyFabricMulticastVNSnapshot(snapshot, path, wants, name, op)
}

func verifyFabricMulticastVNSnapshot(snapshot *fabricMulticastSnapshot, path string, wants []fabricMulticastVNIntent, name string, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if snapshot == nil || op == nil {
		return false, errConfigurationTransition
	}
	if op.TargetID != snapshot.networkIDs[name] {
		return false, errConfigurationStale
	}
	if !validConfigurationGroupAction("fabric", op.Resource, op.Action) {
		return false, errConfigurationTransition
	}
	// Validate the complete three-hash operation identity, then compare only
	// its result binding after the intentional target-state change.
	parts := strings.Split(op.Resource, ":")
	if snapshot.fabricID != op.ParentID || strings.Join(parts[:3], ":") != multicastVNResultResource(path, name, snapshot, op.Action) {
		return false, errConfigurationStale
	}
	observed, exists := snapshot.before[name]
	if !exists {
		return false, nil
	}
	for _, want := range wants {
		if want.name == name {
			return fabricMulticastMatches(want, snapshot.fabricID, snapshot.nameIDs, observed)
		}
	}
	return false, errConfigurationTransition
}
