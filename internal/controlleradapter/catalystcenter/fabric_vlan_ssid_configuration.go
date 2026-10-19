// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

type fabricVLANSSIDConfiguration struct {
	client     *client
	foundation configurationExecutor
	desired    []fabricVLANSSIDIntent
}

func (e *fabricVLANSSIDConfiguration) count() int {
	n := e.foundation.count()
	for _, site := range e.desired {
		for _, mapping := range site.mappings {
			n += len(mapping.SSIDs)
		}
	}
	return n
}
func (e *fabricVLANSSIDConfiguration) next(context.Context) (*configurationWrite, error) {
	return nil, errConfigurationDeviceAdmission
}
func (e *fabricVLANSSIDConfiguration) verify(context.Context, *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	return false, errConfigurationDeviceAdmission
}

type fabricVLANSSIDObservation struct {
	siteID, fabricID string
	mappings         []fabricVLANSSID
}

func (c *client) observeFabricVLANSSIDs(ctx context.Context, path string) (*fabricVLANSSIDObservation, error) {
	if !validSitePath(path) || path == "Global" {
		return nil, errInvalidResponse
	}
	states, err := (&fabricSiteExecutor{client: c, desired: []fabricSiteIntent{{path: path}}}).observe(ctx)
	if err != nil {
		return nil, err
	}
	if len(states) != 1 || !states[0].exists {
		return nil, errConfigurationBusy
	}
	mappings, err := c.fabricVLANSSIDs(ctx, states[0].actual.ID)
	if err != nil {
		return nil, err
	}
	return &fabricVLANSSIDObservation{siteID: states[0].siteID, fabricID: states[0].actual.ID, mappings: mappings}, nil
}
func (e *fabricVLANSSIDConfiguration) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	out := []configapi.NetworkControllerDriftEntry{}
	for _, site := range e.desired {
		before, err := e.client.observeFabricVLANSSIDs(ctx, site.path)
		if err != nil {
			return nil, err
		}
		after, err := e.client.observeFabricVLANSSIDs(ctx, site.path)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(before, after) {
			return nil, errConfigurationStale
		}
		observed := map[string]string{}
		for _, mapping := range before.mappings {
			for _, ssid := range mapping.SSIDs {
				observed[ssid.Name] = mapping.VLAN
			}
		}
		for _, mapping := range site.mappings {
			for _, ssid := range mapping.SSIDs {
				vlan, exists := observed[ssid.Name]
				if exists && vlan == mapping.VLAN {
					continue
				}
				change := configapi.NetworkControllerDriftChangeAdd
				if exists {
					change = configapi.NetworkControllerDriftChangeUpdate
				}
				raw, _ := json.Marshal([]string{site.path, before.siteID, before.fabricID, ssid.Name})
				out = append(out, configapi.NetworkControllerDriftEntry{Section: "fabric", Resource: fmt.Sprintf("fabric-vlan-ssid:%x", sha256.Sum256(raw)), Path: "/fabric_sites/wireless_ssids", Change: change})
			}
		}
	}
	return out, nil
}
