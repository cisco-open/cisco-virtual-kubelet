// Copyright 2026 Cisco Systems Inc.
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
	"strings"
	"sync/atomic"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type sdaTransitIntent struct {
	name, kind    string
	controlPlanes []string
	multicast     *bool
	references    map[string]sdaTransitControlPlaneReference
}

type sdaTransitControlPlaneReference struct {
	selector               configurationDeviceReference
	site, role, roleSource string
}

const sdaTransitCreateFamily = "sda-transit-create:"

// Preserve the NetAsCode names until native inventory resolution. This staged
// parser does not make the ordinary, unreserved fabric compiler accept SDA.
// Optional wired inventory is validated dependency context, never inventory
// mutation authority; selected CPs must explicitly declare PROVISION.
func parseSDATransitIntents(raw []byte) ([]sdaTransitIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) < 1 || len(root) > 2 {
		return nil, errInvalidResponse
	}
	for key := range root {
		if key != "fabric" && key != "inventory" {
			return nil, errInvalidResponse
		}
	}
	var inventory inventoryIntent
	var states map[string]string
	byName := map[string]inventoryDeviceIntent{}
	if value, present := root["inventory"]; present {
		section, ok := value.(map[string]any)
		if !ok || len(section) != 1 || section["devices"] == nil {
			return nil, errInvalidResponse
		}
		projection, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": section}})
		if err != nil {
			return nil, err
		}
		inventory, states, err = parseWiredInventoryIntent(projection)
		if err != nil {
			return nil, err
		}
		for _, device := range inventory.devices {
			byName[device.reference.Name] = device
		}
	}
	fabric, ok := root["fabric"].(map[string]any)
	if !ok || len(fabric) != 1 {
		return nil, errInvalidResponse
	}
	rows, ok := fabric["transits"].([]any)
	if !ok || len(rows) == 0 || len(rows) > 1000 {
		return nil, errInvalidResponse
	}
	seen := map[string]bool{}
	out := []sdaTransitIntent{}
	for _, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			return nil, errInvalidResponse
		}
		for k, v := range m {
			switch k {
			case "name", "type", "control_plane_devices", "multicast_over_sda_transit":
			case "create_per_site":
				if flag, ok := v.(bool); !ok || flag {
					return nil, errConfigurationTransition
				}
			default:
				return nil, errInvalidResponse
			}
		}
		name, nok := m["name"].(string)
		kind, tok := m["type"].(string)
		if !nok || !validProfileName(name) || seen[name] || !tok || kind != "SDA_LISP_PUB_SUB_TRANSIT" && kind != "SDA_LISP_BGP_TRANSIT" {
			return nil, errInvalidResponse
		}
		seen[name] = true
		want := sdaTransitIntent{name: name, kind: kind}
		if v, present := m["multicast_over_sda_transit"]; present {
			flag, ok := v.(bool)
			if !ok {
				return nil, errInvalidResponse
			}
			// The pinned module sends this setting only for PUB_SUB transits.
			if kind == "SDA_LISP_PUB_SUB_TRANSIT" {
				want.multicast = &flag
			}
		}
		names, ok := m["control_plane_devices"].([]any)
		if !ok || len(names) == 0 || len(names) > 1000 {
			return nil, errInvalidResponse
		}
		members := map[string]bool{}
		for _, v := range names {
			name, ok := v.(string)
			if !ok || !validProfileName(name) || members[name] {
				return nil, errInvalidResponse
			}
			members[name] = true
			want.controlPlanes = append(want.controlPlanes, name)
		}
		sort.Strings(want.controlPlanes)
		if states != nil {
			want.references = map[string]sdaTransitControlPlaneReference{}
			for _, name := range want.controlPlanes {
				if states[name] != "PROVISION" {
					return nil, errConfigurationTransition
				}
				device, exists := byName[name]
				if !exists {
					return nil, errInvalidResponse
				}
				want.references[name] = sdaTransitControlPlaneReference{selector: device.reference, site: device.site, role: device.role, roleSource: device.roleSource}
			}
		}
		out = append(out, want)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func freezeSDATransitIntent(want sdaTransitIntent) sdaTransitIntent {
	want.controlPlanes = append([]string(nil), want.controlPlanes...)
	if want.multicast != nil {
		flag := *want.multicast
		want.multicast = &flag
	}
	if want.references != nil {
		copy := make(map[string]sdaTransitControlPlaneReference, len(want.references))
		for key, value := range want.references {
			copy[key] = value
		}
		want.references = copy
	}
	return want
}

type sdaTransitSnapshot struct {
	native      fabricTransit
	physical    []Device
	provisioned []wiredProvisionedDevice
	sites       []Site
	peers       map[string]fabricTransit
	actual      *fabricTransit
	digest      string
}

func sdaTransitMatches(want, got fabricTransit) bool {
	if want.Name != got.Name || want.Type != got.Type || got.IP != nil || want.SDA == nil || got.SDA == nil || len(want.SDA.ControlPlanes) != len(got.SDA.ControlPlanes) {
		return false
	}
	ids := append([]string(nil), got.SDA.ControlPlanes...)
	sort.Strings(ids)
	for i, id := range ids {
		if !validAPIID(id) || i > 0 && ids[i-1] == id || id != want.SDA.ControlPlanes[i] {
			return false
		}
	}
	return want.SDA.Multicast == nil || got.SDA.Multicast != nil && *want.SDA.Multicast == *got.SDA.Multicast
}

// CP provisioning, assigned site, reachability and physical identity are native
// prerequisites. They do not prove a new transit's device convergence.
func (c *client) sdaTransitSnapshot(ctx context.Context, want sdaTransitIntent) (*sdaTransitSnapshot, error) {
	return c.sdaTransitSnapshotWithInventory(ctx, want, nil)
}

// A view belongs to one complete observation only; it is never retained across
// source checks, submission guards or task recovery boundaries.
type sdaTransitNativeInventory struct {
	devices     []Device
	sites       []Site
	transits    map[string]fabricTransit
	assignments map[string]map[string]bool
}

func (c *client) sdaTransitNativeInventory(ctx context.Context) (*sdaTransitNativeInventory, error) {
	devices, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	return &sdaTransitNativeInventory{devices: devices, sites: sites, assignments: map[string]map[string]bool{}}, nil
}

func (c *client) sdaTransitSnapshotWithInventory(ctx context.Context, want sdaTransitIntent, view *sdaTransitNativeInventory) (*sdaTransitSnapshot, error) {
	// Revalidate private callers as well as parsed manifests, and freeze slices.
	row := map[string]any{"name": want.name, "type": want.kind, "control_plane_devices": want.controlPlanes}
	if want.multicast != nil {
		row["multicast_over_sda_transit"] = *want.multicast
	}
	root := map[string]any{"fabric": map[string]any{"transits": []any{row}}}
	if want.references != nil {
		if len(want.references) != len(want.controlPlanes) {
			return nil, errInvalidResponse
		}
		devices := []any{}
		for _, name := range want.controlPlanes {
			ref, exists := want.references[name]
			if !exists || ref.selector.Name != name {
				return nil, errInvalidResponse
			}
			device := map[string]any{"name": name, "site": ref.site, "state": "PROVISION"}
			for key, value := range map[string]string{"fqdn_name": ref.selector.FQDN, "device_ip": ref.selector.IP, "serial_number": ref.selector.Serial, "device_role": ref.role, "role_source": ref.roleSource} {
				if value != "" {
					device[key] = value
				}
			}
			devices = append(devices, device)
		}
		root["inventory"] = map[string]any{"devices": devices}
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": root})
	parsed, err := parseSDATransitIntents(raw)
	if err != nil || len(parsed) != 1 {
		return nil, errInvalidResponse
	}
	want = parsed[0]
	if view == nil {
		view, err = c.sdaTransitNativeInventory(ctx)
		if err != nil {
			return nil, err
		}
	}
	byID := map[string]Site{}
	for _, site := range view.sites {
		byID[site.ID] = site
	}
	s := &sdaTransitSnapshot{native: fabricTransit{Name: want.name, Type: want.kind, SDA: &sdaTransitSettings{Multicast: want.multicast}}}
	selected := map[string]bool{}
	placements := map[string]string{}
	for _, name := range want.controlPlanes {
		desired := inventoryDeviceIntent{reference: configurationDeviceReference{Name: name}}
		if ref, exists := want.references[name]; exists {
			desired = inventoryDeviceIntent{reference: ref.selector, site: ref.site, role: ref.role, roleSource: ref.roleSource}
		}
		d, err := resolveWiredProvisionTarget(desired, view.devices)
		if err != nil {
			return nil, err
		}
		if selected[d.ID] {
			return nil, errConfigurationStale
		}
		// The pinned transit map resolves model name/FQDN, not an unrelated
		// IP/serial-only fallback. Physical selectors remain consistency checks.
		if d.Hostname != desired.reference.Name && d.Hostname != desired.reference.FQDN {
			return nil, errConfigurationStale
		}
		selected[d.ID] = true
		placements[d.ID] = desired.site
		s.physical = append(s.physical, d)
	}
	sort.Slice(s.physical, func(i, j int) bool { return s.physical[i].ID < s.physical[j].ID })
	for _, d := range s.physical {
		p, exists, err := c.wiredProvisionedDevice(ctx, d.ID)
		if err != nil {
			return nil, err
		}
		site, found := byID[p.SiteID]
		if !exists || !found || !validSitePath(site.NameHierarchy) || site.NameHierarchy == "Global" {
			return nil, errConfigurationBusy
		}
		if placements[d.ID] != "" && placements[d.ID] != site.NameHierarchy {
			return nil, errConfigurationBusy
		}
		assigned, found := view.assignments[p.SiteID]
		if !found {
			assigned, err = c.assignedDevices(ctx, p.SiteID)
			if err != nil {
				return nil, err
			}
			view.assignments[p.SiteID] = assigned
		}
		if !assigned[d.ID] {
			return nil, errConfigurationBusy
		}
		s.provisioned = append(s.provisioned, p)
		s.sites = append(s.sites, site)
		s.native.SDA.ControlPlanes = append(s.native.SDA.ControlPlanes, d.ID)
	}
	if view.transits == nil {
		view.transits, err = c.fabricTransits(ctx)
		if err != nil {
			return nil, err
		}
	}
	// Removing this result must not remove it from another declaration's view.
	s.peers = make(map[string]fabricTransit, len(view.transits))
	for name, transit := range view.transits {
		s.peers[name] = transit
	}
	if actual, found := s.peers[want.name]; found {
		s.actual = &actual
		delete(s.peers, want.name)
	}
	// An existing transit's CP cannot be repurposed with only the incoming
	// CP footprint reserved: its connected consumers are outside this stage.
	for _, peer := range s.peers {
		if peer.Type == "IP_BASED_TRANSIT" {
			continue
		}
		if peer.SDA == nil || len(peer.SDA.ControlPlanes) == 0 {
			return nil, errInvalidResponse
		}
		for _, id := range peer.SDA.ControlPlanes {
			if !validAPIID(id) {
				return nil, errInvalidResponse
			}
			if selected[id] {
				return nil, errConfigurationBusy
			}
		}
	}
	// Exclude only the selected result from the recovery dependency digest. No
	// desired IDs, physical bindings, provisioning, site or peers are discarded.
	raw, _ = json.Marshal([]any{s.native, s.physical, s.provisioned, s.sites, s.peers})
	s.digest = fmt.Sprintf("%x", sha256.Sum256(raw))
	return s, nil
}

func sdaTransitResource(s *sdaTransitSnapshot) string {
	return sdaTransitCreateFamily + "v1:" + s.digest
}

// A new global definition has no native UUID. Its logical target is bound to
// the exact dependency fingerprint, not a fabricated controller resource ID.
func sdaTransitOperationTarget(resource, action string) (string, error) {
	const version = sdaTransitCreateFamily + "v1:"
	if action != "Create" || !strings.HasPrefix(resource, version) {
		return "", errConfigurationTransition
	}
	id := strings.TrimPrefix(resource, version)
	if len(id) != 64 || strings.ToLower(id) != id {
		return "", errConfigurationTransition
	}
	if _, err := hex.DecodeString(id); err != nil {
		return "", errConfigurationTransition
	}
	return id, nil
}

// Private execution stage. A public caller still needs the shared journal and
// an all-member GroupSubmissionPermit. This callback cannot create that permit.
func (c *client) planSDATransitCreate(ctx context.Context, want sdaTransitIntent, guard func(context.Context, []Device) error) (*configurationWrite, error) {
	before, err := c.sdaTransitSnapshot(ctx, want)
	if err != nil {
		return nil, err
	}
	if before.actual != nil {
		if sdaTransitMatches(before.native, *before.actual) {
			return nil, nil
		}
		return nil, errConfigurationTransition
	}
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	want = freezeSDATransitIntent(want)
	check := func(ctx context.Context) error {
		fresh, err := c.sdaTransitSnapshot(ctx, want)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(before, fresh) {
			return errConfigurationStale
		}
		return nil
	}
	var used atomic.Bool
	return &configurationWrite{resource: sdaTransitResource(before), action: "Create", identity: configurationResourceIdentity{target: before.digest}, submit: func(ctx context.Context) (Task, error) {
		if used.Swap(true) {
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
		return c.submitTask(ctx, fabricTransitsPath, []fabricTransit{before.native})
	}}, nil
}

func (c *client) sdaTransitTargets(ctx context.Context, reader ctrlclient.Reader, namespace string, want sdaTransitIntent) (*sdaTransitSnapshot, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*sdaTransitSnapshot], error) {
		s, err := c.sdaTransitSnapshot(ctx, want)
		if err != nil {
			return configurationPhysicalObservation[*sdaTransitSnapshot]{}, err
		}
		// Unlike recovery's predecessor digest, this observation also binds
		// whether a same-name native object already exists and its exact state.
		raw, _ := json.Marshal([]any{s.digest, s.actual})
		return configurationPhysicalObservation[*sdaTransitSnapshot]{state: s, physical: s.physical, digest: fmt.Sprintf("%x", sha256.Sum256(raw))}, nil
	})
}

// Use the same complete-footprint observer as the other physical fabric stages.
// This is not public dispatch: only the journal coordinator can supply a real
// all-member permit to the eventual submission bridge.
func (c *client) planManagedSDATransitCreate(ctx context.Context, reader ctrlclient.Reader, namespace string, want sdaTransitIntent, guard func(context.Context, []Device) error) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	want = freezeSDATransitIntent(want)
	before, targets, err := c.sdaTransitTargets(ctx, reader, namespace, want)
	if err != nil {
		return nil, nil, err
	}
	if before.actual != nil {
		if sdaTransitMatches(before.native, *before.actual) {
			return nil, targets, nil
		}
		return nil, nil, errConfigurationTransition
	}
	if guard == nil {
		return nil, nil, errConfigurationDeviceAdmission
	}
	check := func(ctx context.Context) error {
		fresh, current, err := c.sdaTransitTargets(ctx, reader, namespace, want)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(before, fresh) || !reflect.DeepEqual(targets, current) {
			return errConfigurationStale
		}
		return nil
	}
	write, err := c.planSDATransitCreate(ctx, want, func(ctx context.Context, physical []Device) error {
		if !reflect.DeepEqual(physical, before.physical) {
			return errConfigurationStale
		}
		if err := check(ctx); err != nil {
			return err
		}
		if err := guard(ctx, physical); err != nil {
			return err
		}
		return check(ctx)
	})
	if err != nil {
		return nil, nil, err
	}
	if write == nil || write.resource != sdaTransitResource(before) {
		return nil, nil, errConfigurationStale
	}
	if err := check(ctx); err != nil {
		return nil, nil, err
	}
	write.identity.groupTargets = targets
	return write, targets, nil
}

// Task/readback observation is deliberately separate from creator ownership,
// device convergence, completion receipts and reservation release.
func (c *client) observeSDATransitCreate(ctx context.Context, want sdaTransitIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	if op == nil || (op.Phase != "Accepted" && op.Phase != "Unknown" && op.Phase != "Verifying") || op.Action != "Create" || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.ParentID != "" || op.TargetSerial != "" || op.CreatedResourceID != "" || len(op.CredentialVersions) != 0 || op.CancellationRequestedAt != nil {
		return false, errConfigurationTransition
	}
	target, err := sdaTransitOperationTarget(op.Resource, op.Action)
	if err != nil || target != op.TargetID {
		return false, errConfigurationTransition
	}
	before, err := c.sdaTransitSnapshot(ctx, want)
	if err != nil {
		return false, err
	}
	if op.Resource != sdaTransitResource(before) {
		return false, errConfigurationStale
	}
	done, err := c.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || !done {
		return false, err
	}
	done, err = c.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || !done {
		return false, err
	}
	after, err := c.sdaTransitSnapshot(ctx, want)
	if err != nil {
		return false, err
	}
	if !reflect.DeepEqual(before, after) {
		return false, errConfigurationStale
	}
	return after.actual != nil && sdaTransitMatches(after.native, *after.actual), nil
}
