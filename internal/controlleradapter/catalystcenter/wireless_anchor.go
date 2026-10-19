// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const wirelessAnchorPath = "/dna/intent/api/v1/wirelessSettings/anchorGroups"

type wirelessAnchorPeer struct {
	DeviceName string  `json:"deviceName"`
	IP         string  `json:"ipAddress"`
	Priority   string  `json:"anchorPriority"`
	Managed    *bool   `json:"managedAnchorWlc"`
	Type       *string `json:"peerDeviceType,omitempty"`
	MAC        *string `json:"macAddress,omitempty"`
	Group      *string `json:"mobilityGroupName,omitempty"`
	PrivateIP  *string `json:"privateIp,omitempty"`
}
type wirelessAnchorGroup struct {
	ID    string               `json:"id,omitempty"`
	Name  string               `json:"anchorGroupName"`
	Peers []wirelessAnchorPeer `json:"mobilityAnchors"`
}

func normalizeWirelessAnchor(g *wirelessAnchorGroup) error {
	if !validProfileName(g.Name) || len(g.Name) > 32 || len(g.Peers) == 0 || len(g.Peers) > 24 {
		return errInvalidResponse
	}
	names, ips := map[string]bool{}, map[string]bool{}
	for i := range g.Peers {
		p := &g.Peers[i]
		if !validProfileName(p.DeviceName) || p.Managed == nil || !slices.Contains([]string{"PRIMARY", "SECONDARY", "TERTIARY"}, p.Priority) || names[p.DeviceName] {
			return errInvalidResponse
		}
		ip, err := netip.ParseAddr(p.IP)
		if err != nil || ip.Zone() != "" {
			return errInvalidResponse
		}
		p.IP = ip.String()
		if ips[p.IP] {
			return errInvalidResponse
		}
		ips[p.IP] = true
		names[p.DeviceName] = true
		if p.Type != nil && !slices.Contains([]string{"IOS-XE", "AIREOS"}, *p.Type) {
			return errInvalidResponse
		}
		if p.PrivateIP != nil {
			ip, err := netip.ParseAddr(*p.PrivateIP)
			if err != nil || ip.Zone() != "" {
				return errInvalidResponse
			}
			v := ip.String()
			p.PrivateIP = &v
		}
		if p.MAC != nil {
			raw := strings.NewReplacer(":", "", ".", "", "-", "").Replace(*p.MAC)
			if len(raw) != 12 {
				return errInvalidResponse
			}
			formatted := ""
			for j := 0; j < 12; j += 2 {
				if j > 0 {
					formatted += ":"
				}
				formatted += raw[j : j+2]
			}
			mac, err := net.ParseMAC(formatted)
			if err != nil {
				return errInvalidResponse
			}
			v := mac.String()
			p.MAC = &v
		}
		if p.Group != nil && (len(*p.Group) > 31 || !validProfileName(*p.Group) || strings.ContainsAny(*p.Group, "!< ?/'")) {
			return errInvalidResponse
		}
	}
	sort.Slice(g.Peers, func(i, j int) bool { return g.Peers[i].DeviceName < g.Peers[j].DeviceName })
	return nil
}
func parseWirelessAnchors(raw []byte) ([]wirelessAnchorGroup, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errInvalidResponse
	}
	w, ok := root["wireless"].(map[string]any)
	if !ok || len(w) != 1 {
		return nil, errInvalidResponse
	}
	list, ok := w["anchor_groups"].([]any)
	if !ok || len(list) == 0 || len(list) > 1000 {
		return nil, errInvalidResponse
	}
	out := []wirelessAnchorGroup{}
	seen := map[string]bool{}
	fields := map[string]string{"device_name": "deviceName", "ip_address": "ipAddress", "anchor_priority": "anchorPriority", "managed_anchor_wlc": "managedAnchorWlc", "peer_device_type": "peerDeviceType", "mac_address": "macAddress", "mobility_group_name": "mobilityGroupName", "private_ip": "privateIp"}
	for _, v := range list {
		m, ok := v.(map[string]any)
		if !ok || len(m) != 2 {
			return nil, errInvalidResponse
		}
		peers, ok := m["mobility_anchors"].([]any)
		if !ok {
			return nil, errInvalidResponse
		}
		converted := []any{}
		for _, v := range peers {
			p, ok := v.(map[string]any)
			if !ok {
				return nil, errInvalidResponse
			}
			row := map[string]any{}
			for key, value := range p {
				dest, ok := fields[key]
				if !ok || value == nil {
					return nil, errors.New("unsupported or null anchor peer field")
				}
				row[dest] = value
			}
			converted = append(converted, row)
		}
		encoded, _ := json.Marshal(map[string]any{"anchorGroupName": m["name"], "mobilityAnchors": converted})
		var g wirelessAnchorGroup
		if json.Unmarshal(encoded, &g) != nil || normalizeWirelessAnchor(&g) != nil || seen[g.Name] {
			return nil, errors.New("invalid or duplicate anchor group")
		}
		seen[g.Name] = true
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (c *client) wirelessAnchorGroups(ctx context.Context) (map[string]wirelessAnchorGroup, error) {
	// This endpoint exposes the complete collection, with no paging parameters.
	raw, err := c.get(ctx, wirelessAnchorPath, nil, 200)
	if err != nil {
		return nil, err
	}
	rows, err := decodeList[wirelessAnchorGroup](raw)
	if err != nil {
		return nil, err
	}
	out := map[string]wirelessAnchorGroup{}
	ids := map[string]bool{}
	for _, g := range rows {
		if !validAPIID(g.ID) || normalizeWirelessAnchor(&g) != nil || ids[g.ID] {
			return nil, errInvalidResponse
		}
		if _, ok := out[g.Name]; ok {
			return nil, errInvalidResponse
		}
		ids[g.ID] = true
		out[g.Name] = g
	}
	return out, nil
}
func wirelessAnchorMatches(want, got wirelessAnchorGroup) bool {
	if want.Name != got.Name || len(want.Peers) != len(got.Peers) {
		return false
	}
	matches := func(a, b *string) bool { return a == nil || b != nil && *a == *b }
	for i, a := range want.Peers {
		b := got.Peers[i]
		if a.DeviceName != b.DeviceName || a.IP != b.IP || a.Priority != b.Priority || a.Managed == nil || b.Managed == nil || *a.Managed != *b.Managed || !matches(a.Type, b.Type) || !matches(a.MAC, b.MAC) || !matches(a.Group, b.Group) || !matches(a.PrivateIP, b.PrivateIP) {
			return false
		}
	}
	return true
}
func preserveWirelessAnchorOptions(want *wirelessAnchorGroup, got wirelessAnchorGroup) {
	for i := range want.Peers {
		a := &want.Peers[i]
		for _, b := range got.Peers {
			// An IP or management-mode change is a different peer identity.
			if a.DeviceName != b.DeviceName || a.IP != b.IP || *a.Managed != *b.Managed {
				continue
			}
			if a.Type == nil {
				a.Type = b.Type
			}
			if a.MAC == nil {
				a.MAC = b.MAC
			}
			if a.Group == nil {
				a.Group = b.Group
			}
			if a.PrivateIP == nil {
				a.PrivateIP = b.PrivateIP
			}
		}
	}
}

type wirelessAnchorExecutor struct {
	client  *client
	desired []wirelessAnchorGroup
}

// Keep inventory identities separate from the API payload. Hostname equality does
// not authorize dispatch after a managed controller has been replaced.
type wirelessAnchorDeviceKey struct{ Group, Peer string }

type wirelessAnchorDeviceIdentity struct {
	ID, Hostname, ManagementIP, Serial, PlatformID, Family string
}

func (e *wirelessAnchorExecutor) resolved(ctx context.Context) ([]wirelessAnchorGroup, error) {
	groups, _, err := e.resolveObservation(ctx)
	return groups, err
}

func (e *wirelessAnchorExecutor) resolveObservation(ctx context.Context) ([]wirelessAnchorGroup, map[wirelessAnchorDeviceKey]wirelessAnchorDeviceIdentity, error) {
	devices, err := e.client.ListDevices(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := make([]wirelessAnchorGroup, len(e.desired))
	identities := map[wirelessAnchorDeviceKey]wirelessAnchorDeviceIdentity{}
	for i, g := range e.desired {
		out[i] = g
		out[i].Peers = slices.Clone(g.Peers)
		for j := range out[i].Peers {
			p := &out[i].Peers[j]
			matches := []Device{}
			for _, d := range devices {
				if d.Hostname == p.DeviceName || !strings.Contains(p.DeviceName, ".") && strings.SplitN(d.Hostname, ".", 2)[0] == p.DeviceName {
					matches = append(matches, d)
				}
			}
			if len(matches) > 1 {
				return nil, nil, errors.New("ambiguous anchor device hostname")
			}
			if len(matches) == 1 {
				d := matches[0]
				identities[wirelessAnchorDeviceKey{g.Name, p.DeviceName}] = wirelessAnchorDeviceIdentity{
					d.ID, d.Hostname, d.ManagementIP, d.Serial, d.PlatformID, d.Family,
				}
				p.DeviceName = d.Hostname
			} else if *p.Managed {
				return nil, nil, errors.New("managed anchor device not found")
			}
		}
		if err := normalizeWirelessAnchor(&out[i]); err != nil {
			return nil, nil, err
		}
	}
	return out, identities, nil
}
func (e *wirelessAnchorExecutor) count() int { return len(e.desired) }
func (e *wirelessAnchorExecutor) next(ctx context.Context) (*configurationWrite, error) {
	actual, err := e.client.wirelessAnchorGroups(ctx)
	if err != nil {
		return nil, err
	}
	desired, identities, err := e.resolveObservation(ctx)
	if err != nil {
		return nil, err
	}
	for _, want := range desired {
		got, exists := actual[want.Name]
		if exists && wirelessAnchorMatches(want, got) {
			continue
		}
		method, path, action := http.MethodPost, wirelessAnchorPath, "Create"
		identity := configurationResourceIdentity{}
		if exists {
			method, path, action = http.MethodPut, wirelessAnchorPath+"/"+url.PathEscape(got.ID), "Update"
			identity.target = got.ID
			want.Peers = slices.Clone(want.Peers)
			preserveWirelessAnchorOptions(&want, got)
		}
		body, _ := json.Marshal(want)
		write := &configurationWrite{resource: "anchor-group:" + want.Name, action: action, identity: identity, submit: func(ctx context.Context) (Task, error) {
			raw, err := e.client.mutate(ctx, method, path, body)
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
		}}
		// Capture immutable values, including omitted-but-preserved peer options.
		// Reads are selected by name; unrelated groups do not invalidate a plan.
		plannedDesired, _ := json.Marshal(desired)
		plannedNative, _ := json.Marshal(got)
		selectedName := want.Name
		guardWirelessDefinitionPreflight(write, func(ctx context.Context) error {
			resolved, currentIdentities, err := e.resolveObservation(ctx)
			if err != nil {
				return err
			}
			encoded, _ := json.Marshal(resolved)
			if string(encoded) != string(plannedDesired) || !reflect.DeepEqual(identities, currentIdentities) {
				return errConfigurationStale
			}
			// Read the target last so inventory resolution cannot hide a peer
			// edit made while the dependency request was in flight.
			fresh, err := e.client.wirelessAnchorGroups(ctx)
			if err != nil {
				return err
			}
			current, present := fresh[selectedName]
			native, _ := json.Marshal(current)
			if present != exists || string(native) != string(plannedNative) {
				return errConfigurationStale
			}
			return nil
		})
		return write, nil
	}
	return nil, nil
}
func (e *wirelessAnchorExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op.ParentID != "" || op.TargetSerial != "" || (op.Action != "Create" && op.Action != "Update") || (op.Action == "Create" && op.TargetID != "") || (op.Action == "Update" && !validAPIID(op.TargetID)) {
		return false, errConfigurationTransition
	}
	actual, err := e.client.wirelessAnchorGroups(ctx)
	if err != nil {
		return false, err
	}
	desired, err := e.resolved(ctx)
	if err != nil {
		return false, err
	}
	for _, want := range desired {
		if op.Resource == "anchor-group:"+want.Name {
			got, ok := actual[want.Name]
			return ok && wirelessAnchorMatches(want, got) && (op.TargetID == "" || op.TargetID == got.ID), nil
		}
	}
	return false, errConfigurationTransition
}
func (e *wirelessAnchorExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	actual, err := e.client.wirelessAnchorGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	desired, err := e.resolved(ctx)
	if err != nil {
		return nil, err
	}
	for _, want := range desired {
		got, ok := actual[want.Name]
		if ok && wirelessAnchorMatches(want, got) {
			continue
		}
		change := configapi.NetworkControllerDriftChangeAdd
		if ok {
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "wireless", Resource: "anchor-group:" + want.Name, Path: "/anchor_groups", Change: change})
	}
	return out, nil
}
