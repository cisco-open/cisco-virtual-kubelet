// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"slices"
	"strings"
)

type fabricL3AssociationExecutor struct {
	client  *client
	desired []fabricL3AssociationIntent
}
type fabricL3AssociationState struct {
	want                             fabricL3AssociationIntent
	siteID, fabricID, parentFabricID string
	network                          fabricL3Network
	exists, ready                    bool
}

func (e *fabricL3AssociationExecutor) count() int { return len(e.desired) }
func (s fabricL3AssociationState) prefix() string {
	return fmt.Sprintf("fabric-l3-member:%x/", sha256.Sum256([]byte(s.want.path+"\x00"+s.want.name+"\x00"+s.siteID+"\x00"+s.parentFabricID)))
}
func (e *fabricL3AssociationExecutor) observe(ctx context.Context) ([]fabricL3AssociationState, error) {
	siteIntents := []fabricSiteIntent{}
	zoneIntents := []fabricZoneIntent{}
	seenSites, seenZones := map[string]bool{}, map[string]bool{}
	for _, w := range e.desired {
		if w.parent == "" {
			if !seenSites[w.path] {
				siteIntents = append(siteIntents, fabricSiteIntent{path: w.path})
				seenSites[w.path] = true
			}
		} else {
			if !seenZones[w.path] {
				zoneIntents = append(zoneIntents, fabricZoneIntent{path: w.path, parent: w.parent})
				seenZones[w.path] = true
			}
		}
	}
	siteStates := map[string]fabricSiteState{}
	zoneStates := map[string]fabricZoneState{}
	if len(siteIntents) > 0 {
		rows, err := (&fabricSiteExecutor{client: e.client, desired: siteIntents}).observe(ctx)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			siteStates[row.want.path] = row
		}
	}
	if len(zoneIntents) > 0 {
		rows, err := (&fabricZoneExecutor{client: e.client, desired: zoneIntents}).observe(ctx)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			zoneStates[row.want.path] = row
		}
	}
	networks, err := e.client.fabricL3Networks(ctx)
	if err != nil {
		return nil, err
	}
	out := []fabricL3AssociationState{}
	for _, w := range e.desired {
		s := fabricL3AssociationState{want: w}
		if w.parent == "" {
			o := siteStates[w.path]
			s.siteID = o.siteID
			s.fabricID = o.actual.ID
		} else {
			o := zoneStates[w.path]
			s.siteID = o.siteID
			s.fabricID = o.actual.ID
			s.parentFabricID = o.parentID
		}
		n, exists := networks[w.name]
		if exists && !n.membershipsKnown {
			return nil, errInvalidResponse
		}
		s.network = n
		s.ready = exists && s.fabricID != ""
		if w.parent != "" && !slices.Contains(n.Fabrics, s.parentFabricID) {
			s.ready = false
		}
		s.exists = exists && s.fabricID != "" && slices.Contains(n.Fabrics, s.fabricID)
		if s.exists && !s.ready {
			return nil, errConfigurationStale
		}
		out = append(out, s)
	}
	return out, nil
}
func (e *fabricL3AssociationExecutor) next(ctx context.Context) (*configurationWrite, error) {
	states, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	// Preflight all targets before returning any write. The only deferred
	// dependency here is a zone's parent association explicitly in this set.
	for _, s := range states {
		if s.network.ID == "" || s.fabricID == "" {
			return nil, errConfigurationBusy
		}
		if !s.ready {
			planned := false
			for _, parent := range states {
				if parent.want.path == s.want.parent && parent.want.name == s.want.name && parent.want.parent == "" && parent.ready {
					planned = true
				}
			}
			if !planned {
				return nil, errConfigurationBusy
			}
		}
	}
	for _, s := range states {
		if s.exists {
			continue
		}
		if !s.ready {
			return nil, errConfigurationBusy
		}
		write, err := e.client.planFabricL3Association(ctx, s.want.name, s.fabricID)
		if err != nil {
			return nil, err
		}
		// A concurrent external attachment is not submission evidence. Replan from
		// fresh inventory instead of manufacturing a task or dereferencing nil.
		if write == nil {
			return nil, errConfigurationStale
		}
		submit := write.submit
		write.resource = s.prefix() + write.resource
		write.submit = func(ctx context.Context) (Task, error) {
			latest, err := e.observe(ctx)
			if err != nil {
				return Task{}, err
			}
			for _, current := range latest {
				if current.want == s.want {
					if !current.ready || current.prefix() != s.prefix() || current.fabricID != s.fabricID || current.network.ID != s.network.ID {
						return Task{}, errConfigurationStale
					}
					return submit(ctx)
				}
			}
			return Task{}, errConfigurationStale
		}
		return write, nil
	}
	return nil, nil
}
func (e *fabricL3AssociationExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil {
		return false, errConfigurationTransition
	}
	states, err := e.observe(ctx)
	if err != nil {
		return false, err
	}
	for _, s := range states {
		if strings.HasPrefix(op.Resource, s.prefix()) {
			if !s.ready || s.fabricID != op.ParentID || s.network.ID != op.TargetID {
				return false, errConfigurationStale
			}
			copy := *op
			copy.Resource = strings.TrimPrefix(op.Resource, s.prefix())
			return e.client.verifyFabricL3Association(ctx, s.want.name, s.fabricID, &copy)
		}
	}
	return false, errConfigurationStale
}
func (e *fabricL3AssociationExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	states, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, s := range states {
		if !s.exists {
			out = append(out, configapi.NetworkControllerDriftEntry{Section: "fabric", Resource: fmt.Sprintf("fabric-l3-member:%x", sha256.Sum256([]byte(s.want.path+"\x00"+s.want.name))), Path: "/fabric_sites/l3_virtual_networks", Change: configapi.NetworkControllerDriftChangeAdd})
		}
	}
	return out, nil
}
