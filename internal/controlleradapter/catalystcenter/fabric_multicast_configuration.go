// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"sort"
)

type fabricMulticastSiteIntent struct {
	path   string
	intent fabricMulticastIntent
}

func parseFabricSitesWithMulticast(value any) (fabricGatewayModel, []fabricMulticastSiteIntent, error) {
	rows, ok := value.([]any)
	if !ok {
		return fabricGatewayModel{}, nil, errInvalidResponse
	}
	stripped := make([]any, 0, len(rows))
	out := []fabricMulticastSiteIntent{}
	for _, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			return fabricGatewayModel{}, nil, errInvalidResponse
		}
		copy := map[string]any{}
		for key, value := range m {
			if key != "multicast" {
				copy[key] = value
			}
		}
		if value, present := m["multicast"]; present && value != nil {
			intent, err := parseFabricMulticast(value)
			if err != nil {
				return fabricGatewayModel{}, nil, err
			}
			path, ok := m["name"].(string)
			if !ok {
				return fabricGatewayModel{}, nil, errInvalidResponse
			}
			if intent.replication != "" || intent.wireless != nil || len(intent.networks) > 0 {
				out = append(out, fabricMulticastSiteIntent{path, intent})
			}
		}
		stripped = append(stripped, copy)
	}
	model, err := parseFabricSitesWithGateways(stripped)
	if err != nil {
		return fabricGatewayModel{}, nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return *model, out, nil
}

// Guard the entire Apply composition, including prerequisite writes. Report is
// read-only; accepting its model must not accidentally enable physical execution.
type fabricMulticastConfiguration struct {
	client     *client
	foundation configurationExecutor
	desired    []fabricMulticastSiteIntent
}

func (e *fabricMulticastConfiguration) count() int {
	n := e.foundation.count()
	for _, s := range e.desired {
		n += len(s.intent.networks)
		if s.intent.replication != "" {
			n++
		}
		if s.intent.wireless != nil {
			n++
		}
	}
	return n
}
func (e *fabricMulticastConfiguration) next(context.Context) (*configurationWrite, error) {
	return nil, errConfigurationDeviceAdmission
}
func (e *fabricMulticastConfiguration) verify(context.Context, *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	return false, errConfigurationDeviceAdmission
}
func (e *fabricMulticastConfiguration) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	out := []configapi.NetworkControllerDriftEntry{}
	for _, site := range e.desired {
		if site.intent.wireless != nil {
			drift, err := e.client.reportFabricWirelessMulticast(ctx, site.path, *site.intent.wireless)
			if err != nil {
				return nil, err
			}
			out = append(out, drift...)
		}
		if len(site.intent.networks) > 0 {
			snapshot, err := e.client.multicastSnapshot(ctx, site.path, site.intent.networks)
			if err != nil {
				return nil, err
			}
			for _, want := range site.intent.networks {
				change := configapi.NetworkControllerDriftChangeAdd
				if got, exists := snapshot.before[want.name]; exists {
					same, err := fabricMulticastMatches(want, snapshot.fabricID, snapshot.nameIDs, got)
					if err != nil {
						return nil, err
					}
					if same {
						continue
					}
					change = configapi.NetworkControllerDriftChangeUpdate
				}
				out = append(out, configapi.NetworkControllerDriftEntry{Section: "fabric", Resource: multicastVNResource(site.path, want.name, ""), Path: "/fabric_sites/multicast/virtual_networks", Change: change})
			}
		}
		if site.intent.replication != "" {
			state, err := e.client.fabricReplicationState(ctx, site.path)
			if err != nil {
				return nil, err
			}
			if state.exists && state.mode == site.intent.replication {
				continue
			}
			change := configapi.NetworkControllerDriftChangeAdd
			if state.exists {
				change = configapi.NetworkControllerDriftChangeUpdate
			}
			out = append(out, configapi.NetworkControllerDriftEntry{Section: "fabric", Resource: fabricReplicationResource(site.path, site.intent.replication, state), Path: "/fabric_sites/multicast/replication_mode", Change: change})
		}
	}
	return out, nil
}
