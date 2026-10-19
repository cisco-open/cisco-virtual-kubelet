// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const fabricDevicesBulkLimit = 10
const fabricDevicesBulkFamily = "fabric-devices-create:"
const fabricDevicesBulkPrefix = fabricDevicesBulkFamily + "v1:"
const fabricDevicesBulkIDsPrefix = fabricDevicesBulkFamily + "v2:"

type fabricDevicesBulkSnapshot struct {
	fabric         string
	entries        []*fabricDeviceDependencies
	physical       []Device
	mask           uint16
	deferredParent bool
}

func (c *client) fabricDevicesBulkSnapshot(ctx context.Context, wants []fabricDeviceIntent) (*fabricDevicesBulkSnapshot, error) {
	return c.fabricDevicesBulkPreflightSnapshot(ctx, wants, nil)
}

func (c *client) fabricDevicesBulkPreflightSnapshot(ctx context.Context, wants []fabricDeviceIntent, proposal *fabricDevicesParentProposal) (*fabricDevicesBulkSnapshot, error) {
	if len(wants) == 0 || len(wants) > fabricDevicesBulkLimit {
		return nil, errConfigurationTransition
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, want := range wants {
		target, err := resolveConfigurationDevice(want.inventory.reference, inventory)
		if err != nil {
			return nil, err
		}
		if seen[target.ID] {
			return nil, errConfigurationTransition
		}
		seen[target.ID] = true
		ids = append(ids, target.ID)
	}
	out := &fabricDevicesBulkSnapshot{}
	for _, want := range wants {
		deps, err := c.collectFabricDeviceDependenciesPreflight(ctx, want, false, ids, proposal)
		if err != nil {
			return nil, err
		}
		if out.fabric == "" {
			out.fabric = deps.native.FabricID
			out.physical = deps.physical
		}
		if deps.native.FabricID != out.fabric || !reflect.DeepEqual(out.physical, deps.physical) {
			return nil, errConfigurationStale
		}
		if got, exists := deps.before[deps.target.ID]; exists && !fabricDeviceConfigurationMatches(deps.native, got) {
			return nil, errConfigurationTransition
		}
		out.entries = append(out.entries, deps)
		out.deferredParent = out.deferredParent || deps.deferredParent
	}
	sort.Slice(out.entries, func(i, j int) bool { return out.entries[i].target.ID < out.entries[j].target.ID })
	for i, deps := range out.entries {
		if _, exists := deps.before[deps.target.ID]; !exists {
			out.mask |= 1 << uint(i)
		}
	}
	return out, nil
}

func (s *fabricDevicesBulkSnapshot) legacyResource(mask uint16) string {
	type member struct {
		Native                 fabricDeviceConfiguration
		Dependency, ExistingID string
	}
	rows := []member{}
	for i, entry := range s.entries {
		id := ""
		if mask&(1<<uint(i)) == 0 {
			id = entry.before[entry.target.ID].ID
		}
		rows = append(rows, member{entry.native, entry.recoveryDigest, id})
	}
	raw, _ := json.Marshal(rows)
	return fmt.Sprintf("%s%d:%03x:%x", fabricDevicesBulkPrefix, len(rows), mask, sha256.Sum256(raw))
}

func (s *fabricDevicesBulkSnapshot) resource(mask uint16) string {
	ids := []string{}
	for _, entry := range s.entries {
		ids = append(ids, entry.target.ID)
	}
	legacy := strings.TrimPrefix(s.legacyResource(mask), fabricDevicesBulkPrefix)
	return fabricDevicesBulkIDsPrefix + legacy + ":" + strings.Join(ids, ",")
}

// Private physical stage. The supplied guard must authorize the entire returned
// footprint; this does not create reservations, grant authority or enable public
// submission. Native task acknowledgement and original-scope readback are separate.
func (c *client) planFabricDevicesBulk(ctx context.Context, wants []fabricDeviceIntent, guard func(context.Context, []Device) error) (*configurationWrite, error) {
	before, err := c.fabricDevicesBulkSnapshot(ctx, wants)
	if err != nil {
		return nil, err
	}
	if before.mask == 0 {
		return nil, nil
	}
	if len(before.resource(before.mask)) > 512 {
		return nil, errConfigurationTransition
	}
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	// Canonical native snapshots own mutable roles/border fields. Dependencies
	// re-resolve the same selectors using those captured desired values.
	frozen := append([]fabricDeviceIntent(nil), wants...)
	for i := range frozen {
		target, err := resolveConfigurationDevice(frozen[i].inventory.reference, before.physical)
		if err != nil {
			return nil, err
		}
		for _, entry := range before.entries {
			if entry.target.ID == target.ID {
				frozen[i].roles = entry.native.Roles
				frozen[i].border = entry.native.Border
				break
			}
		}
	}
	check := func(ctx context.Context) error {
		fresh, err := c.fabricDevicesBulkSnapshot(ctx, frozen)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(before, fresh) {
			return errConfigurationStale
		}
		return nil
	}
	var used atomic.Bool
	return &configurationWrite{resource: before.resource(before.mask), action: "Create", identity: configurationResourceIdentity{target: before.fabric, parent: before.fabric}, submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		if err := check(ctx); err != nil {
			return Task{}, err
		}
		if err := guard(ctx, append([]Device(nil), before.physical...)); err != nil {
			return Task{}, err
		}
		if err := check(ctx); err != nil {
			return Task{}, err
		}
		body := []fabricDeviceConfiguration{}
		for i, entry := range before.entries {
			if before.mask&(1<<uint(i)) != 0 {
				body = append(body, entry.native)
			}
		}
		return c.submitTask(ctx, fabricDevicesPath, body)
	}}, nil
}

func (c *client) verifyFabricDevicesBulk(ctx context.Context, wants []fabricDeviceIntent, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Phase != "Verifying" || !validAPIID(op.TaskID) || op.Action != "Create" || op.TargetID != op.ParentID || op.TargetSerial != "" || !validAPIID(op.ParentID) || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	count, mask, ids, err := parseFabricDevicesBulkResourceIDs(op.Resource, op.Action)
	if err != nil || count != len(wants) {
		return false, errConfigurationTransition
	}
	fresh, err := c.fabricDevicesBulkSnapshot(ctx, wants)
	if err != nil {
		return false, err
	}
	resource := fresh.legacyResource(mask)
	if ids != nil {
		resource = fresh.resource(mask)
	}
	if fresh.fabric != op.ParentID || resource != op.Resource {
		return false, errConfigurationStale
	}
	for _, entry := range fresh.entries {
		if _, exists := entry.before[entry.target.ID]; !exists {
			return false, nil
		}
	}
	return true, nil
}

func parseFabricDevicesBulkResource(resource, action string) (int, uint16, error) {
	count, mask, _, err := parseFabricDevicesBulkResourceIDs(resource, action)
	return count, mask, err
}

func parseFabricDevicesBulkResourceIDs(resource, action string) (int, uint16, []string, error) {
	prefix := fabricDevicesBulkPrefix
	fields := 3
	if strings.HasPrefix(resource, fabricDevicesBulkIDsPrefix) {
		prefix = fabricDevicesBulkIDsPrefix
		fields = 4
	}
	parts := strings.Split(strings.TrimPrefix(resource, prefix), ":")
	if action != "Create" || len(resource) > 512 || !strings.HasPrefix(resource, prefix) || len(parts) != fields {
		return 0, 0, nil, errConfigurationTransition
	}
	count, err := strconv.Atoi(parts[0])
	if err != nil || count < 1 || count > fabricDevicesBulkLimit || strconv.Itoa(count) != parts[0] {
		return 0, 0, nil, errConfigurationTransition
	}
	mask, err := strconv.ParseUint(parts[1], 16, 16)
	if err != nil || mask == 0 || mask>>uint(count) != 0 || fmt.Sprintf("%03x", mask) != parts[1] {
		return 0, 0, nil, errConfigurationTransition
	}
	digest, err := hex.DecodeString(parts[2])
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != parts[2] {
		return 0, 0, nil, errConfigurationTransition
	}
	var ids []string
	if fields == 4 {
		ids = strings.Split(parts[3], ",")
		if len(ids) != count {
			return 0, 0, nil, errConfigurationTransition
		}
		for i, id := range ids {
			if !validAPIID(id) || i > 0 && ids[i-1] >= id {
				return 0, 0, nil, errConfigurationTransition
			}
		}
	}
	return count, uint16(mask), ids, nil
}
