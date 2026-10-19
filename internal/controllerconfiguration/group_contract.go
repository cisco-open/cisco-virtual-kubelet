// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"k8s.io/apimachinery/pkg/util/validation"
	"sort"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

// GroupIdentityVersion is an identity format, not an enabled admission protocol.
// Persistence, manager/worker coordination and dispatch remain separate gates.
const GroupIdentityVersion = "controller-configuration-group-identity-v1"

type GroupTargetIdentity = cvk.NetworkControllerConfigurationGroupTarget

// CanonicalGroupTargets validates the complete immutable physical footprint.
// Empty groups cannot authorize a device mutation. Input order is not identity.
func CanonicalGroupTargets(targets []GroupTargetIdentity) ([]GroupTargetIdentity, error) {
	reject := errors.New("incomplete or ambiguous configuration group targets")
	if len(targets) == 0 || len(targets) > 1000 {
		return nil, reject
	}
	out := append([]GroupTargetIdentity(nil), targets...)
	physical, inventory, devices, nodes := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	deviceNames, nodeNames := map[string]bool{}, map[string]bool{}
	for i := range out {
		t := &out[i]
		canonical, err := topologyidentity.CanonicalPhysicalIdentity(t.PhysicalIdentity)
		if err != nil || !validGroupIdentityToken(t.InventoryID) || len(validation.IsDNS1123Subdomain(t.Device.Name)) > 0 || !validGroupIdentityToken(t.Device.UID) || len(validation.IsDNS1123Subdomain(t.Node.Name)) > 0 || !validGroupIdentityToken(t.Node.UID) {
			return nil, reject
		}
		if physical[canonical] || inventory[t.InventoryID] || devices[t.Device.UID] || nodes[t.Node.UID] || deviceNames[t.Device.Name] || nodeNames[t.Node.Name] {
			return nil, reject
		}
		physical[canonical] = true
		inventory[t.InventoryID] = true
		devices[t.Device.UID] = true
		nodes[t.Node.UID] = true
		deviceNames[t.Device.Name] = true
		nodeNames[t.Node.Name] = true
		t.PhysicalIdentity = canonical
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PhysicalIdentity < out[j].PhysicalIdentity })
	return out, nil
}

// GroupOperationDigest retains the logical resource TargetID/ParentID while
// binding physical devices independently. It must never be passed to v1's
// single-device reservation validation as a substitute for that protocol.
func GroupOperationDigest(op *cvk.NetworkControllerConfigurationOperation, targets []GroupTargetIdentity) (string, error) {
	if op == nil || op.CreatedResourceID != "" || op.DependencyVersions != nil || !operationIDPattern.MatchString(op.ID) || op.ConfigUID == "" || op.ConfigName == "" || op.ConfigGeneration < 1 || op.ControllerGeneration < 1 || op.Resource == "" || op.Section == "" || op.TargetSerial != "" || (op.Action != "Create" && op.Action != "Update" && op.Action != "Delete") || len(op.CredentialVersions) > 0 {
		return "", errors.New("invalid group configuration operation")
	}
	canonical, err := CanonicalGroupTargets(targets)
	if err != nil {
		return "", err
	}
	copy := op.DeepCopy()
	copy.PhysicalGroup = nil // Exclude the self-referential persisted digest.
	copy.Phase = ""
	copy.TaskID = ""
	copy.SubmittedAt = nil
	copy.CancellationRequestedAt = nil
	raw, err := json.Marshal(struct {
		Version   string                                       `json:"version"`
		Operation *cvk.NetworkControllerConfigurationOperation `json:"operation"`
		Targets   []GroupTargetIdentity                        `json:"targets"`
	}{GroupIdentityVersion, copy, canonical})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func validGroupIdentityToken(value string) bool {
	return value != "" && len(value) <= 128 && !strings.ContainsAny(value, " \t\r\n\x00")
}
