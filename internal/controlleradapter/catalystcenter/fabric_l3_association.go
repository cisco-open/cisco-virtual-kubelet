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
	"slices"
	"sort"
)

// Native PUT replaces fabricIds, rather than adding one membership. Bind the
// complete post-write set and anchor into the journal identity, preserving
// memberships outside this manifest. The controller's shared journal serializes
// CVK writers; an in-memory mutex would not protect restarts or other workers.
func fabricL3AssociationResource(n fabricL3Network) string {
	ids := append([]string{}, n.Fabrics...)
	sort.Strings(ids)
	raw, _ := json.Marshal(struct {
		ID, Name, Anchor string
		Fabrics          []string
	}{n.ID, n.Name, n.Anchor, ids})
	return fmt.Sprintf("fabric-l3-association:%x", sha256.Sum256(raw))
}
func (c *client) planFabricL3Association(ctx context.Context, name, fabricID string) (*configurationWrite, error) {
	if !validProfileName(name) || !validAPIID(fabricID) {
		return nil, errInvalidResponse
	}
	networks, err := c.fabricL3Networks(ctx)
	if err != nil {
		return nil, err
	}
	before, exists := networks[name]
	if !exists {
		return nil, errConfigurationBusy
	}
	if !before.membershipsKnown {
		return nil, errInvalidResponse
	}
	if slices.Contains(before.Fabrics, fabricID) {
		return nil, nil
	}
	after := before
	after.Fabrics = append(append([]string{}, before.Fabrics...), fabricID)
	sort.Strings(after.Fabrics)
	resource := fabricL3AssociationResource(after)
	// Preserve anchoredSiteId only when present; never send an empty anchor
	// while attaching to an existing anchored VN.
	body := map[string]any{"id": after.ID, "virtualNetworkName": after.Name, "fabricIds": after.Fabrics}
	if after.Anchor != "" {
		body["anchoredSiteId"] = after.Anchor
	}
	encoded, err := json.Marshal([]any{body})
	if err != nil {
		return nil, err
	}
	return &configurationWrite{resource: resource, action: "Update", identity: configurationResourceIdentity{target: before.ID, parent: fabricID}, submit: func(ctx context.Context) (Task, error) {
		latest, err := c.fabricL3Networks(ctx)
		if err != nil {
			return Task{}, err
		}
		current, exists := latest[name]
		if !exists || !current.membershipsKnown || fabricL3AssociationResource(current) != fabricL3AssociationResource(before) {
			return Task{}, errConfigurationStale
		}
		raw, err := c.mutate(ctx, http.MethodPut, fabricL3NetworksPath, encoded)
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
func (c *client) verifyFabricL3Association(ctx context.Context, name, fabricID string, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Action != "Update" || !validAPIID(op.TargetID) || op.ParentID != fabricID || !validAPIID(fabricID) || op.TargetSerial != "" || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	networks, err := c.fabricL3Networks(ctx)
	if err != nil {
		return false, err
	}
	n, exists := networks[name]
	if !exists || !n.membershipsKnown || n.ID != op.TargetID {
		return false, errConfigurationStale
	}
	// Exact expected membership evidence detects lost unrelated attachments as
	// well as anchor changes. Merely finding the new target is insufficient.
	return slices.Contains(n.Fabrics, fabricID) && fabricL3AssociationResource(n) == op.Resource, nil
}
