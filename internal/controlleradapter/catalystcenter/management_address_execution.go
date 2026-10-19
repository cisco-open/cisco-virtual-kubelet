// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

const managementAddressPrefix = "management-address:"

// Guard receives native identity and the requested address so the future
// runtime bridge can require managed admission and independent endpoint proof.
// No public compiler enables this primitive until that bridge is qualified.
type managementAddressGuard func(context.Context, Device, string) error

func managementAddressSelection(ref configurationDeviceReference, id, serial, address string) string {
	raw, _ := json.Marshal([]string{ref.Name, ref.FQDN, ref.Serial, id, serial, address})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func managementAddressResource(ref configurationDeviceReference, change *managementAddressChange, serial string) string {
	return fmt.Sprintf("%s%s:%x", managementAddressPrefix, managementAddressSelection(ref, change.device.ID, serial, change.after), sha256.Sum256([]byte(change.before)))
}

func managementAddressBaselineMatches(before, latest *managementAddressChange, serial string) bool {
	if before == nil || latest == nil || latest.device.ID != before.device.ID || latest.before != before.before || latest.after != before.after {
		return false
	}
	current, err := topologyidentity.CanonicalPhysicalIdentity(latest.device.Serial)
	return err == nil && current == serial
}

func (c *client) planGuardedManagementAddressChange(ctx context.Context, ref configurationDeviceReference, guard managementAddressGuard) (*configurationWrite, error) {
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	before, err := c.managementAddressCandidate(ctx, ref)
	if err != nil || before == nil {
		return nil, err
	}
	serial, err := topologyidentity.CanonicalPhysicalIdentity(before.device.Serial)
	if err != nil {
		return nil, errConfigurationDeviceAdmission
	}
	body, err := json.Marshal(map[string]string{"newIP": before.after})
	if err != nil {
		return nil, err
	}
	var used atomic.Bool
	return &configurationWrite{resource: managementAddressResource(ref, before, serial), action: "Update", identity: configurationResourceIdentity{target: before.device.ID, serial: serial}, submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		latest, err := c.managementAddressCandidate(ctx, ref)
		if err != nil {
			return Task{}, err
		}
		if !managementAddressBaselineMatches(before, latest, serial) {
			return Task{}, errConfigurationStale
		}
		if err := guard(ctx, latest.device, latest.after); err != nil {
			return Task{}, err
		}
		// Endpoint proof may take time. Recheck native identity after the
		// guard so that its latency cannot conceal an intervening replacement.
		latest, err = c.managementAddressCandidate(ctx, ref)
		if err != nil {
			return Task{}, err
		}
		if !managementAddressBaselineMatches(before, latest, serial) {
			return Task{}, errConfigurationStale
		}
		raw, err := c.mutate(ctx, http.MethodPut, devicesPath+"/"+before.device.ID+"/management-address", body)
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

// Native inventory convergence alone cannot release a managed device fence.
// Require the caller's separately qualified endpoint/handoff check after the
// UUID, physical serial and requested address match the retained selection.
func (c *client) verifyManagementAddressChange(ctx context.Context, ref configurationDeviceReference, op *cvk.NetworkControllerConfigurationOperation, guard managementAddressGuard) (bool, error) {
	if guard == nil || op == nil || op.Section != "inventory" || op.Action != "Update" || op.Phase != "Verifying" || !validAPIID(op.TargetID) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.CancellationRequestedAt != nil || op.ParentID != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || len(op.CredentialVersions) != 0 || op.CreatedResourceID != "" {
		return false, errConfigurationTransition
	}
	serial, err := topologyidentity.CanonicalPhysicalIdentity(op.TargetSerial)
	if err != nil || serial != op.TargetSerial {
		return false, errConfigurationTransition
	}
	if !strings.HasPrefix(op.Resource, managementAddressPrefix) {
		return false, errConfigurationTransition
	}
	parts := strings.Split(strings.TrimPrefix(op.Resource, managementAddressPrefix), ":")
	if len(parts) != 2 {
		return false, errConfigurationTransition
	}
	for _, part := range parts {
		digest, err := hex.DecodeString(part)
		if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != part {
			return false, errConfigurationTransition
		}
	}
	wanted, err := netip.ParseAddr(ref.IP)
	if err != nil || wanted.Zone() != "" || wanted.Is4In6() || !wanted.IsGlobalUnicast() || parts[0] != managementAddressSelection(ref, op.TargetID, serial, wanted.String()) {
		return false, errConfigurationStale
	}
	target, err := c.managementAddressVerificationTarget(ctx, ref, op.TargetID, serial, wanted)
	if err != nil || target == nil {
		return false, err
	}
	if err := guard(ctx, *target, wanted.String()); err != nil {
		return false, err
	}
	target, err = c.managementAddressVerificationTarget(ctx, ref, op.TargetID, serial, wanted)
	return err == nil && target != nil, err
}

func (c *client) managementAddressVerificationTarget(ctx context.Context, ref configurationDeviceReference, id, serial string, wanted netip.Addr) (*Device, error) {
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	var target *Device
	for i := range inventory {
		device := &inventory[i]
		if device.ID == id {
			if target != nil {
				return nil, errInvalidResponse
			}
			target = device
		} else if sameManagementIP(device.ManagementIP, wanted.String()) {
			return nil, errConfigurationDeviceAdmission
		}
	}
	if target == nil {
		return nil, errConfigurationStale
	}
	currentSerial, err := topologyidentity.CanonicalPhysicalIdentity(target.Serial)
	if err != nil || currentSerial != serial {
		return nil, errConfigurationStale
	}
	if !sameManagementIP(target.ManagementIP, wanted.String()) {
		return nil, nil
	}
	resolved, err := resolveConfigurationDevice(ref, inventory)
	if err != nil || resolved.ID != target.ID {
		return nil, errConfigurationStale
	}
	if change, err := planManagementAddressChange(configurationDeviceReference{Name: target.Hostname, IP: wanted.String(), Serial: target.Serial}, inventory); err != nil || change != nil {
		return nil, errConfigurationDeviceAdmission
	}
	return target, nil
}
