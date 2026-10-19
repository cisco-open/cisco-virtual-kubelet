// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

type fabricL2Executor struct {
	client  *client
	desired []fabricL2Intent
}
type fabricL2State struct {
	want                 fabricL2Intent
	fabricID, dependency string
	actual               fabricL2Network
	exists, ready        bool
	vlan                 int
}

func (e *fabricL2Executor) count() int { return len(e.desired) }
func (w fabricL2Intent) resource() string {
	return fmt.Sprintf("fabric-l2:%x", sha256.Sum256([]byte(w.path+"\x00"+w.name)))
}
func (o fabricL2State) resource() string {
	return fmt.Sprintf("%s:%x", o.want.resource(), sha256.Sum256([]byte(o.dependency)))
}
func fabricL2Matches(w fabricL2Intent, n fabricL2Network, vlan int) bool {
	return n.VLANName == w.vlanName && (vlan == 0 || n.VLAN == vlan) && n.Traffic == w.traffic && n.L3 == w.l3 && (w.wireless == nil || n.Wireless != nil && *n.Wireless == *w.wireless)
}
func (e *fabricL2Executor) observe(ctx context.Context) ([]fabricL2State, error) {
	return e.observeWithPending(ctx, nil)
}

// Pending associations are admitted only for preflight/report, never submission
// or completion verification. Keys bind an explicitly declared path and VN.
func (e *fabricL2Executor) observeWithPending(ctx context.Context, pending map[fabricL2Key]bool) ([]fabricL2State, error) {
	sites, err := e.client.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	byPath := map[string]Site{}
	for _, s := range sites {
		byPath[s.NameHierarchy] = s
	}
	fabrics, err := e.client.fabricSites(ctx)
	if err != nil {
		return nil, err
	}
	zones := map[string]fabricZone{}
	for _, w := range e.desired {
		if w.parent != "" {
			zones, err = e.client.fabricZones(ctx)
			if err != nil {
				return nil, err
			}
			break
		}
	}
	actual, err := e.client.fabricL2Networks(ctx)
	if err != nil {
		return nil, err
	}
	gateways, err := e.client.anycastGateways(ctx)
	if err != nil {
		return nil, err
	}
	l3s := map[string]fabricL3Network{}
	for _, w := range e.desired {
		if w.l3 != "" {
			l3s, err = e.client.fabricL3Networks(ctx)
			if err != nil {
				return nil, err
			}
			break
		}
	}
	out := []fabricL2State{}
	for _, w := range e.desired {
		site, ok := byPath[w.path]
		if !ok {
			return nil, errInvalidResponse
		}
		o := fabricL2State{want: w, vlan: w.vlan}
		if w.parent == "" {
			o.fabricID = fabrics[site.ID].ID
		} else {
			o.fabricID = zones[site.ID].ID
		}
		o.ready = o.fabricID != ""
		if w.parent != "" {
			parentSite, ok := byPath[w.parent]
			if !ok {
				return nil, errInvalidResponse
			}
			parentFabric := fabrics[parentSite.ID].ID
			parentVN, exists := actual[fabricL2Key{parentFabric, w.vlanName}]
			if exists {
				if !fabricL2Matches(w, parentVN, w.vlan) {
					return nil, errConfigurationTransition
				}
				o.vlan = parentVN.VLAN
				o.dependency = fmt.Sprintf("%s\x00%s\x00%d", parentFabric, parentVN.ID, parentVN.VLAN)
			} else {
				o.ready = false
			}
		}
		if w.l3 != "" {
			network, exists := l3s[w.l3]
			attached := false
			for _, id := range network.Fabrics {
				if id == o.fabricID {
					attached = true
				}
			}
			// Defining an L3 VN is not enough: the native API requires fabric
			// association. Never infer or silently create that association here.
			if !exists || !attached {
				if !pending[fabricL2Key{w.path, w.l3}] {
					return nil, errConfigurationBusy
				}
				o.ready = false
			}
			o.dependency += "\x00" + network.ID
		}
		o.actual, o.exists = actual[fabricL2Key{o.fabricID, w.vlanName}]
		if o.exists && !o.ready {
			return nil, errConfigurationStale
		}
		if !o.exists && o.fabricID != "" && o.vlan != 0 {
			for _, n := range actual {
				if n.FabricID == o.fabricID && n.VLAN == o.vlan {
					return nil, errConfigurationTransition
				}
			}
		}
		if !o.exists && o.fabricID != "" {
			for _, gateway := range gateways {
				if gateway.FabricID == o.fabricID && (gateway.VLANName == w.vlanName || o.vlan != 0 && gateway.VLAN == o.vlan) {
					return nil, errConfigurationTransition
				}
			}
		}
		out = append(out, o)
	}
	return out, nil
}
func (e *fabricL2Executor) preflight(ctx context.Context) error {
	states, err := e.observe(ctx)
	if err != nil {
		return err
	}
	for _, o := range states {
		if o.exists && !fabricL2Matches(o.want, o.actual, o.vlan) {
			return errConfigurationTransition
		}
	}
	return nil
}
func (e *fabricL2Executor) next(ctx context.Context) (*configurationWrite, error) {
	states, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	var first *configurationWrite
	for _, o := range states {
		if o.exists {
			if !fabricL2Matches(o.want, o.actual, o.vlan) {
				return nil, errConfigurationTransition
			}
			continue
		}
		if !o.ready {
			if first == nil {
				return nil, errConfigurationBusy
			}
			continue
		}
		if first != nil {
			continue
		}
		body := fabricL2Network{FabricID: o.fabricID, VLANName: o.want.vlanName, VLAN: o.vlan, Traffic: o.want.traffic, Wireless: o.want.wireless, L3: o.want.l3}
		first = &configurationWrite{resource: o.resource(), action: "Create", identity: configurationResourceIdentity{parent: o.fabricID}, submit: func(ctx context.Context) (Task, error) {
			// Re-observe dependencies and allocations immediately before writing.
			// A plan is not permission to reuse a subsequently allocated VLAN or a
			// replaced fabric/parent/VN. Never replay when the target now exists.
			latest, err := e.observe(ctx)
			if err != nil {
				return Task{}, err
			}
			for _, current := range latest {
				if current.want.resource() != o.want.resource() {
					continue
				}
				if current.exists || !current.ready || current.fabricID != o.fabricID || current.vlan != o.vlan || current.resource() != o.resource() {
					return Task{}, errConfigurationStale
				}
				return e.client.submitTask(ctx, fabricL2NetworksPath, []fabricL2Network{body})
			}
			return Task{}, errConfigurationStale
		}}
	}
	return first, nil
}
func (e *fabricL2Executor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Action != "Create" || op.TargetID != "" || !validAPIID(op.ParentID) || op.TargetSerial != "" || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	states, err := e.observe(ctx)
	if err != nil {
		return false, err
	}
	for _, o := range states {
		if o.resource() == op.Resource {
			if !o.ready || o.fabricID != op.ParentID {
				return false, errConfigurationStale
			}
			return o.exists && fabricL2Matches(o.want, o.actual, o.vlan), nil
		}
	}
	return false, errConfigurationStale
}
func (e *fabricL2Executor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	return e.reportWithPending(ctx, nil)
}
func (e *fabricL2Executor) reportWithPending(ctx context.Context, pending map[fabricL2Key]bool) ([]configapi.NetworkControllerDriftEntry, error) {
	states, err := e.observeWithPending(ctx, pending)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, o := range states {
		if o.exists && fabricL2Matches(o.want, o.actual, o.vlan) {
			continue
		}
		change := configapi.NetworkControllerDriftChangeAdd
		if o.exists {
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "fabric", Resource: o.want.resource(), Path: "/fabric_sites/l2_virtual_networks", Change: change})
	}
	return out, nil
}
