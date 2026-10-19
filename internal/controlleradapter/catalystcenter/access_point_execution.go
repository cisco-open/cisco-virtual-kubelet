// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync/atomic"

	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

const accessPointConfigurePath = "/dna/intent/api/v1/wireless/accesspoint-configuration"

// Native staging only: the caller must supply independent provisioning and
// all-member mutation authority. Neither inventory nor this plan grants it.
// Settings are common to the complete request; only Ethernet MAC and rename
// belong to each AP, matching the pinned module's configuration cohorts.
type accessPointConfigurationPlan struct {
	digest  string
	members []accessPointConfigurationMember
	submit  func(context.Context) (Task, error)
}

type accessPointConfigurationMember struct {
	DeviceID, Serial, EthernetMAC, SiteID, RFID, Dependencies string
}

type accessPointConfigurationSnapshot struct {
	body    []byte
	members []accessPointConfigurationMember
	digest  string
}

func freezeAccessPointConfiguration(wants []accessPointDeviceIntent) ([]accessPointDeviceIntent, error) {
	if len(wants) == 0 || len(wants) > 1000 {
		return nil, errAccessPointModel
	}
	frozen := append([]accessPointDeviceIntent(nil), wants...)
	var settings []byte
	serials, canonical := map[string]bool{}, map[string]string{}
	for i := range frozen {
		want := &frozen[i]
		physical, err := topologyidentity.CanonicalPhysicalIdentity(want.inventory.reference.Serial)
		if err != nil || serials[physical] {
			return nil, errAccessPointIdentity
		}
		serials[physical], canonical[want.inventory.reference.Serial] = true, physical
		if want.state != "PROVISION" && want.state != "REPROVISION" || want.profile == nil && !want.manageHostname {
			return nil, errAccessPointModel
		}
		payload := map[string]any{}
		if want.profile != nil {
			payload = want.profile.payload
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		if i > 0 && !bytes.Equal(settings, raw) {
			// Heterogeneous cohorts must be planned separately, never silently
			// configured with the first member's settings.
			return nil, errAccessPointModel
		}
		settings = raw
		var copy map[string]any
		if json.Unmarshal(raw, &copy) != nil || copy == nil {
			return nil, errAccessPointModel
		}
		want.profile = &accessPointProfile{payload: copy}
	}
	sort.Slice(frozen, func(i, j int) bool {
		return canonical[frozen[i].inventory.reference.Serial] < canonical[frozen[j].inventory.reference.Serial]
	})
	return frozen, nil
}

func (c *client) accessPointConfigurationSnapshot(ctx context.Context, wants []accessPointDeviceIntent) (*accessPointConfigurationSnapshot, error) {
	catalog, err := c.accessPointDependencyCatalog(ctx)
	if err != nil {
		return nil, err
	}
	return c.accessPointConfigurationSnapshotWithCatalog(ctx, wants, catalog, map[string]map[string]bool{})
}

// Catalogs may be shared across cohorts inside one complete observation only.
func (c *client) accessPointConfigurationSnapshotWithCatalog(ctx context.Context, wants []accessPointDeviceIntent, catalog *accessPointDependencyCatalog, assignments map[string]map[string]bool) (*accessPointConfigurationSnapshot, error) {
	if len(wants) == 0 || wants[0].profile == nil || catalog == nil || assignments == nil {
		return nil, errAccessPointModel
	}
	var payload map[string]any
	encoded, err := json.Marshal(wants[0].profile.payload)
	if err != nil || json.Unmarshal(encoded, &payload) != nil {
		return nil, errAccessPointModel
	}
	// The published configure schema requires the radio list even when only
	// hostname or top-level settings are selected.
	if _, exists := payload["radioConfigurations"]; !exists {
		payload["radioConfigurations"] = []any{}
	}
	apList := []any{}
	members := []accessPointConfigurationMember{}
	serials, devices, macs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, want := range wants {
		deps, err := catalog.resolve(want)
		if err != nil {
			return nil, err
		}
		assigned, exists := assignments[deps.siteID]
		if !exists {
			assigned, err = c.assignedDevices(ctx, deps.siteID)
			if err != nil {
				return nil, err
			}
			assignments[deps.siteID] = assigned
		}
		if !assigned[deps.target.device.ID] {
			return nil, errConfigurationBusy
		}
		target := deps.target
		if serials[target.device.Serial] || devices[target.device.ID] || macs[target.ethernetMAC] {
			return nil, errAccessPointIdentity
		}
		serials[target.device.Serial], devices[target.device.ID], macs[target.ethernetMAC] = true, true, true
		member, err := target.member(want.inventory.reference.Name, want.manageHostname)
		if err != nil {
			return nil, err
		}
		apList = append(apList, member)
		members = append(members, accessPointConfigurationMember{target.device.ID, target.device.Serial, target.ethernetMAC, deps.siteID, deps.rfID, deps.digest})
	}
	payload["apList"] = apList
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	digest, err := wirelessTagDigest(map[string]any{"body": json.RawMessage(body), "members": members})
	if err != nil {
		return nil, err
	}
	return &accessPointConfigurationSnapshot{body: body, members: members, digest: digest}, nil
}

// The guard must corroborate the exact whole-cohort digest and identities with
// durable source/journal, provisioning completion and physical reservation
// evidence. A refusal or uncertain result consumes the plan. No retry, receipt
// publication, completion inference or lease release occurs here.
func (c *client) planAccessPointConfiguration(ctx context.Context, wants []accessPointDeviceIntent, guard func(context.Context, string, []accessPointConfigurationMember) error) (*accessPointConfigurationPlan, error) {
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	frozen, err := freezeAccessPointConfiguration(wants)
	if err != nil {
		return nil, err
	}
	before, err := c.accessPointConfigurationSnapshot(ctx, frozen)
	if err != nil {
		return nil, err
	}
	check := func(ctx context.Context) error {
		fresh, err := c.accessPointConfigurationSnapshot(ctx, frozen)
		if err != nil {
			return err
		}
		if fresh.digest != before.digest {
			return errConfigurationStale
		}
		return nil
	}
	if err := check(ctx); err != nil {
		return nil, err
	}
	var used atomic.Bool
	return &accessPointConfigurationPlan{digest: before.digest, members: append([]accessPointConfigurationMember(nil), before.members...), submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		if err := check(ctx); err != nil {
			return Task{}, err
		}
		if err := guard(ctx, before.digest, append([]accessPointConfigurationMember(nil), before.members...)); err != nil {
			return Task{}, err
		}
		if err := check(ctx); err != nil {
			return Task{}, err
		}
		raw, err := c.mutateQueryStatus(ctx, http.MethodPost, accessPointConfigurePath, nil, before.body, http.StatusOK)
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
