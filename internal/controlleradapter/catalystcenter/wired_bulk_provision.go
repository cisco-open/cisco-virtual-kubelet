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
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const wiredBulkProvisionPrefix = "wired-bulk-provision:"
const wiredBulkProvisionLimit = 10 // pinned provider 0.6.1 root-list shard size

type wiredBulkMember struct {
	desired inventoryDeviceIntent
	state   string
}
type wiredBulkSnapshot struct {
	anchor           string
	entries          []*wiredProvisionSnapshot
	state, digest    string
	mask             uint16
	dependencyDigest string
}

type wiredBulkGuard func(context.Context, []Device) error

func (c *client) wiredBulkSnapshot(ctx context.Context, path string, members []wiredBulkMember, dependencies ...wiredBulkDependencyObserver) (*wiredBulkSnapshot, error) {
	if len(dependencies) > 1 || len(dependencies) == 1 && dependencies[0] == nil {
		return nil, errConfigurationTransition
	}
	if !validSitePath(path) || len(members) == 0 || len(members) > wiredBulkProvisionLimit {
		return nil, errConfigurationTransition
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	anchor := ""
	for _, site := range sites {
		if site.NameHierarchy == path {
			if anchor != "" || !validAPIID(site.ID) {
				return nil, errInvalidResponse
			}
			anchor = site.ID
		}
	}
	if anchor == "" {
		return nil, errConfigurationBusy
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	// One fresh catalog per observation, never a cache across observations or
	// submissions. Shared single-device identity and native readback rules remain.
	snapshot := &wiredBulkSnapshot{anchor: anchor}
	ids, serials := map[string]bool{}, map[string]bool{}
	for _, member := range members {
		if (member.state != "PROVISION" && member.state != "REPROVISION") || member.desired.wireless != nil || member.desired.site != path && !strings.HasPrefix(member.desired.site, path+"/") {
			return nil, errConfigurationTransition
		}
		if snapshot.state != "" && snapshot.state != member.state {
			return nil, errConfigurationTransition
		}
		snapshot.state = member.state
		target, err := resolveWiredProvisionTarget(member.desired, inventory)
		if err != nil {
			return nil, err
		}
		entry, err := c.wiredProvisionSnapshotWithTarget(ctx, member.desired, target, sites)
		if err != nil {
			return nil, err
		}
		if ids[entry.target.ID] || serials[entry.target.Serial] {
			return nil, errConfigurationOwned
		}
		ids[entry.target.ID], serials[entry.target.Serial] = true, true
		if member.state == "REPROVISION" && !entry.exists {
			return nil, errConfigurationBusy
		}
		snapshot.entries = append(snapshot.entries, entry)
	}
	sort.Slice(snapshot.entries, func(i, j int) bool { return snapshot.entries[i].target.ID < snapshot.entries[j].target.ID })
	for i, entry := range snapshot.entries {
		if snapshot.state == "REPROVISION" || !entry.exists {
			snapshot.mask |= 1 << i
		}
	}
	if len(dependencies) == 1 {
		digest, err := dependencies[0](ctx)
		if err != nil {
			return nil, err
		}
		if _, err := hex.DecodeString(digest); err != nil || len(digest) != 64 || strings.ToLower(digest) != digest {
			return nil, errInvalidResponse
		}
		snapshot.dependencyDigest = digest
	}
	snapshot.digest = snapshot.bindingDigest(snapshot.mask)
	return snapshot, nil
}
func (s *wiredBulkSnapshot) physical() []Device {
	targets := make([]Device, 0, len(s.entries))
	for _, entry := range s.entries {
		targets = append(targets, entry.target)
	}
	return targets
}
func (s *wiredBulkSnapshot) bindingDigest(mask uint16) string {
	identity := []string{s.anchor, s.state}
	if s.dependencyDigest != "" {
		identity = append(identity, "bulk-dependencies-v1", s.dependencyDigest)
	}
	for i, entry := range s.entries {
		identity = append(identity, entry.digest)
		// Creation receipts have no pre-existing UUID. Keep UUIDs for initially
		// converged members and all PUT entries, using the original retained mask.
		if s.state == "REPROVISION" || mask&(1<<i) == 0 {
			identity = append(identity, entry.before.ID)
		}
	}
	raw, _ := json.Marshal(identity)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func (s *wiredBulkSnapshot) resource(mask uint16) string {
	action := "create"
	if s.state == "REPROVISION" {
		action = "update"
	}
	version := "v1"
	if s.dependencyDigest != "" {
		version = "v2"
	}
	return fmt.Sprintf("%s%s:%s:%d:%03x:%s", wiredBulkProvisionPrefix, version, action, len(s.entries), mask, s.bindingDigest(mask))
}
func parseWiredBulkResource(resource, action string) (int, uint16, string, error) {
	value, ok := strings.CutPrefix(resource, wiredBulkProvisionPrefix)
	parts := strings.Split(value, ":")
	if !ok || len(parts) != 5 || parts[0] != "v1" && parts[0] != "v2" || action != "Create" && action != "Update" || parts[1] != strings.ToLower(action) {
		return 0, 0, "", errConfigurationTransition
	}
	count, err := strconv.Atoi(parts[2])
	if err != nil || count < 1 || count > wiredBulkProvisionLimit || strconv.Itoa(count) != parts[2] {
		return 0, 0, "", errConfigurationTransition
	}
	mask, err := strconv.ParseUint(parts[3], 16, 16)
	if err != nil || mask == 0 || mask >= 1<<count || fmt.Sprintf("%03x", mask) != parts[3] {
		return 0, 0, "", errConfigurationTransition
	}
	if action == "Update" && mask != (1<<count)-1 {
		return 0, 0, "", errConfigurationTransition
	}
	digest := parts[4]
	if _, err := hex.DecodeString(digest); err != nil || len(digest) != 64 || strings.ToLower(digest) != digest {
		return 0, 0, "", errConfigurationTransition
	}
	return count, uint16(mask), digest, nil
}

// The caller's guard must supply exact held all-member authority; native
// snapshots themselves grant no dispatch. POST and PUT are separate batches.
// This private primitive never creates a journal/claim or infers API success
// from a duplicate-device error. The retained selection mask survives partial
// native creation without authorizing a smaller replacement request.
func (c *client) planWiredBulkProvision(ctx context.Context, path string, members []wiredBulkMember, guard wiredBulkGuard, dependencies ...wiredBulkDependencyObserver) (*configurationWrite, error) {
	before, err := c.wiredBulkSnapshot(ctx, path, members, dependencies...)
	if err != nil {
		return nil, err
	}
	if before.mask == 0 {
		return nil, nil
	}
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	payload := []wiredProvisionedDevice{}
	for i, entry := range before.entries {
		if before.mask&(1<<i) != 0 {
			row := wiredProvisionedDevice{SiteID: entry.site.ID, DeviceID: entry.target.ID}
			if before.state == "REPROVISION" {
				row.ID = entry.before.ID
			}
			payload = append(payload, row)
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	method, action := http.MethodPost, "Create"
	if before.state == "REPROVISION" {
		method, action = http.MethodPut, "Update"
	}
	var consumed atomic.Bool
	write := &configurationWrite{resource: before.resource(before.mask), action: action, identity: configurationResourceIdentity{target: before.anchor, parent: before.anchor}}
	write.submit = func(ctx context.Context) (Task, error) {
		if !consumed.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		check := func() error {
			fresh, err := c.wiredBulkSnapshot(ctx, path, members, dependencies...)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(before, fresh) {
				return errConfigurationStale
			}
			return nil
		}
		if err := check(); err != nil {
			return Task{}, err
		}
		if err := guard(ctx, before.physical()); err != nil {
			return Task{}, err
		}
		if err := check(); err != nil {
			return Task{}, err
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
	}
	return write, nil
}

// A successful task alone is insufficient: every selected and initially
// converged member must retain its native identity and expected site. Recover
// with the original mask instead of re-planning the remaining subset.
func (c *client) verifyWiredBulkProvision(ctx context.Context, path string, members []wiredBulkMember, op *cvk.NetworkControllerConfigurationOperation, now time.Time, dependencies ...wiredBulkDependencyObserver) (bool, error) {
	completed, err := c.observeWiredBulkProvisionCompletion(ctx, path, members, op, now, dependencies...)
	return !completed.IsZero(), err
}

func (c *client) observeWiredBulkProvisionCompletion(ctx context.Context, path string, members []wiredBulkMember, op *cvk.NetworkControllerConfigurationOperation, now time.Time, dependencies ...wiredBulkDependencyObserver) (time.Time, error) {
	if op == nil || op.Phase != "Verifying" || op.Section != "inventory" || op.TargetSerial != "" || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 || op.SubmittedAt == nil || op.SubmittedAt.IsZero() {
		return time.Time{}, errConfigurationTransition
	}
	count, mask, digest, err := parseWiredBulkResource(op.Resource, op.Action)
	if err != nil {
		return time.Time{}, err
	}
	completed, err := c.configurationTaskTreeCompletion(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || completed.IsZero() {
		return time.Time{}, err
	}
	fresh, err := c.wiredBulkSnapshot(ctx, path, members, dependencies...)
	if err != nil {
		return time.Time{}, err
	}
	if len(fresh.entries) != count || fresh.bindingDigest(mask) != digest || fresh.resource(mask) != op.Resource || fresh.anchor != op.TargetID || fresh.anchor != op.ParentID {
		return time.Time{}, errConfigurationStale
	}
	for _, entry := range fresh.entries {
		if !entry.exists {
			return time.Time{}, nil
		}
	}
	return completed, nil
}
