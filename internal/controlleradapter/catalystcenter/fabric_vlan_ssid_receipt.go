// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Completion evidence only. The coordinator must atomically persist this using
// the existing group receipt contract before completing or releasing claims.
func (c *client) verifiedFabricVLANSSIDReceipt(ctx context.Context, path string, want []fabricVLANSSID, owned map[string]fabricVLANSSIDOwned, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	return verifyFabricVLANSSIDReceipt(ctx, path, want, op, now, c.configurationTaskTreeComplete, func(ctx context.Context) (*fabricVLANSSIDDependencies, error) {
		return c.vlanSSIDDependencies(ctx, path, want, owned)
	})
}

func verifyFabricVLANSSIDReceipt(ctx context.Context, path string, want []fabricVLANSSID, op *cvk.NetworkControllerConfigurationOperation, now time.Time, task func(context.Context, string, time.Time, time.Time) (bool, error), collect func(context.Context) (*fabricVLANSSIDDependencies, error)) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	if task == nil || collect == nil || op == nil || op.Section != "fabric" || op.Phase != "Verifying" || op.Action != "Update" || op.CancellationRequestedAt != nil || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || now.IsZero() || op.SubmittedAt.After(now) || !validAPIID(op.TaskID) || contract.ValidateGroupJournalIdentity(op) != nil {
		return nil, errConfigurationTransition
	}
	parts, err := parseFabricVLANSSIDResource(op.Resource)
	if err != nil {
		return nil, err
	}
	declared, err := canonicalFabricVLANSSIDs(want)
	if err != nil {
		return nil, err
	}
	done, err := task(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, errConfigurationBusy
	}
	var previous *fabricVLANSSIDDependencies
	for i := 0; i < 2; i++ {
		deps, err := collect(ctx)
		if err != nil {
			return nil, err
		}
		if deps == nil || len(deps.digest) != 64 || deps.siteID != op.ParentID || deps.fabricID != op.TargetID {
			return nil, errConfigurationStale
		}
		observed, err := canonicalFabricVLANSSIDs(deps.before)
		if err != nil {
			return nil, err
		}
		resource, err := fabricVLANSSIDResource(path, &fabricVLANSSIDDependencies{siteID: deps.siteID, fabricID: deps.fabricID, before: observed, payload: observed})
		if err != nil {
			return nil, err
		}
		actual := strings.Split(resource, ":")
		if actual[1] != parts[1] {
			return nil, errConfigurationStale
		}
		if actual[3] != parts[3] {
			return nil, errConfigurationBusy
		}
		claimed := false
		for _, row := range observed {
			for _, ssid := range row.SSIDs {
				if fmt.Sprintf("%x", sha256.Sum256([]byte(ssid.Name))) != parts[4] {
					continue
				}
				binding := fabricVLANSSID{VLAN: row.VLAN, SSIDs: []fabricSSIDBinding{ssid}}
				raw, _ := json.Marshal(binding)
				if fmt.Sprintf("%x", sha256.Sum256(raw)) != parts[5] {
					return nil, errConfigurationStale
				}
				for _, desired := range declared {
					for _, entry := range desired.SSIDs {
						if desired.VLAN == row.VLAN && entry.Name == ssid.Name && entry.SecurityGroup == nil {
							claimed = true
						}
					}
				}
			}
		}
		if !claimed {
			return nil, errConfigurationStale
		}
		if len(deps.physical) != len(op.PhysicalGroup.Targets) {
			return nil, errConfigurationStale
		}
		identities := map[string]string{}
		for _, device := range deps.physical {
			if _, duplicate := identities[device.ID]; duplicate {
				return nil, errConfigurationStale
			}
			identities[device.ID] = device.Serial
		}
		for _, target := range op.PhysicalGroup.Targets {
			if identities[target.InventoryID] != target.PhysicalIdentity {
				return nil, errConfigurationStale
			}
		}
		if previous != nil && (previous.digest != deps.digest || !reflect.DeepEqual(previous.physical, deps.physical)) {
			return nil, errConfigurationStale
		}
		previous = deps
	}
	key, err := fabricVLANSSIDApplicationKey(op.Resource)
	if err != nil {
		return nil, err
	}
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	at := metav1.NewTime(now.UTC())
	return &cvk.NetworkControllerConfigurationActionApplication{Section: "fabric", Resource: key, Operation: *verified, VerifiedAt: &at}, nil
}
