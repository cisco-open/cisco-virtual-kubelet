// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"net/url"
	"time"
)

const wiredProvisionPath = "/dna/intent/api/v1/sda/provisionDevices"

type wiredProvisionedDevice struct {
	ID       string `json:"id,omitempty"`
	SiteID   string `json:"siteId"`
	DeviceID string `json:"networkDeviceId"`
}

// Existence binds native identity; it does not prove completion of a new task.
func (c *client) wiredProvisionedDevice(ctx context.Context, deviceID string) (wiredProvisionedDevice, bool, error) {
	if !validAPIID(deviceID) {
		return wiredProvisionedDevice{}, false, errInvalidResponse
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		return c.get(ctx, path, q, http.StatusOK)
	}
	rows, err := listInventoryQueryWithGet(ctx, get, wiredProvisionPath, url.Values{"networkDeviceId": {deviceID}}, func(d wiredProvisionedDevice) string { return d.ID })
	if err != nil {
		return wiredProvisionedDevice{}, false, err
	}
	if len(rows) == 0 {
		return wiredProvisionedDevice{}, false, nil
	}
	if len(rows) != 1 || !validAPIID(rows[0].ID) || !validAPIID(rows[0].SiteID) || rows[0].DeviceID != deviceID {
		return wiredProvisionedDevice{}, false, errInvalidResponse
	}
	return rows[0], true, nil
}

type wiredProvisionSnapshot struct {
	target Device
	site   Site
	before wiredProvisionedDevice
	exists bool
	digest string
}

func (c *client) wiredProvisionSnapshot(ctx context.Context, want inventoryDeviceIntent) (*wiredProvisionSnapshot, error) {
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	target, err := resolveWiredProvisionTarget(want, inventory)
	if err != nil {
		return nil, err
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	return c.wiredProvisionSnapshotWithTarget(ctx, want, target, sites)
}

func resolveWiredProvisionTarget(want inventoryDeviceIntent, inventory []Device) (Device, error) {
	target, err := resolveConfigurationDevice(want.reference, inventory)
	if err != nil {
		return Device{}, err
	}
	bound, err := bindMulticastRPDevices([]string{target.ID}, inventory)
	if err != nil {
		return Device{}, err
	}
	target = bound[0]
	if target.Reachability != "Reachable" || (target.Family != "Switches and Hubs" && target.Family != "Routers") {
		return Device{}, errConfigurationTransition
	}
	if want.role != "" && (want.role != target.Role || want.roleSource != target.RoleSource) {
		return Device{}, errConfigurationBusy
	}
	return target, nil
}

func (c *client) wiredProvisionSnapshotWithTarget(ctx context.Context, want inventoryDeviceIntent, target Device, sites []Site) (*wiredProvisionSnapshot, error) {
	var site Site
	for _, s := range sites {
		if s.NameHierarchy == want.site {
			site = s
		}
	}
	if !validAPIID(site.ID) || site.Type == "global" {
		return nil, errConfigurationBusy
	}
	assigned, err := c.assignedDevices(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	if !assigned[target.ID] {
		return nil, errConfigurationBusy
	}
	before, exists, err := c.wiredProvisionedDevice(ctx, target.ID)
	if err != nil {
		return nil, err
	}
	if exists && before.SiteID != site.ID {
		return nil, errConfigurationTransition
	}
	raw, err := json.Marshal(struct {
		Target Device
		Site   Site
	}{target, site})
	if err != nil {
		return nil, err
	}
	return &wiredProvisionSnapshot{target: target, site: site, before: before, exists: exists, digest: fmt.Sprintf("%x", sha256.Sum256(raw))}, nil
}
func wiredProvisionResource(state string, snapshot *wiredProvisionSnapshot) string {
	nativeID := ""
	if state == "REPROVISION" {
		nativeID = snapshot.before.ID
	}
	raw, _ := json.Marshal([]string{state, snapshot.digest, nativeID})
	return fmt.Sprintf("wired-provision:%x", sha256.Sum256(raw))
}

// Native single-device step. Runtime still requires the existing per-device
// mutation admission, journal and retained application receipt for REPROVISION.
func (c *client) planWiredDeviceProvision(ctx context.Context, want inventoryDeviceIntent, state string) (*configurationWrite, error) {
	if state != "PROVISION" && state != "REPROVISION" || want.wireless != nil {
		return nil, errInvalidResponse
	}
	snapshot, err := c.wiredProvisionSnapshot(ctx, want)
	if err != nil {
		return nil, err
	}
	action, method := "Create", http.MethodPost
	payload := wiredProvisionedDevice{SiteID: snapshot.site.ID, DeviceID: snapshot.target.ID}
	if state == "PROVISION" && snapshot.exists {
		return nil, nil
	}
	if state == "REPROVISION" {
		if !snapshot.exists {
			return nil, errConfigurationBusy
		}
		action, method = "Update", http.MethodPut
		payload.ID = snapshot.before.ID
	}
	body, err := json.Marshal([]wiredProvisionedDevice{payload})
	if err != nil {
		return nil, err
	}
	return &configurationWrite{action: action, resource: wiredProvisionResource(state, snapshot), identity: configurationResourceIdentity{target: snapshot.target.ID, parent: snapshot.site.ID, serial: snapshot.target.Serial}, submit: func(ctx context.Context) (Task, error) {
		fresh, err := c.wiredProvisionSnapshot(ctx, want)
		if err != nil {
			return Task{}, err
		}
		if fresh.digest != snapshot.digest || fresh.exists != snapshot.exists || fresh.before != snapshot.before {
			return Task{}, errConfigurationStale
		}
		raw, err := c.mutate(ctx, method, wiredProvisionPath, body)
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
func (c *client) verifyWiredDeviceProvision(ctx context.Context, want inventoryDeviceIntent, state string, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	action := "Create"
	if state == "REPROVISION" {
		action = "Update"
	}
	if want.wireless != nil || state != "PROVISION" && state != "REPROVISION" || op == nil || op.Phase != "Verifying" || op.Action != action || op.SubmittedAt == nil || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	done, err := c.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || !done {
		return false, err
	}
	snapshot, err := c.wiredProvisionSnapshot(ctx, want)
	if err != nil {
		return false, err
	}
	if !snapshot.exists {
		return false, nil
	}
	if op.Resource != wiredProvisionResource(state, snapshot) || op.TargetID != snapshot.target.ID || op.TargetSerial != snapshot.target.Serial || op.ParentID != snapshot.site.ID {
		return false, errConfigurationStale
	}
	return true, nil
}
