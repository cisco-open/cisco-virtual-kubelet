// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strings"
)

const wirelessCleanAirPath = "/dna/intent/api/v1/featureTemplates/wireless/cleanAirConfigurations"
const wirelessCleanAirKind = "CLEANAIR_CONFIGURATION"

var cleanAirInterferers = map[string]string{
	"ble_beacon": "bleBeacon", "bluetooth_paging_inquiry": "bluetoothPagingInquiry", "bluetooth_sco_acl": "bluetoothScoAcl", "continuous_transmitter": "continuousTransmitter", "generic_dect": "genericDect", "generic_tdd": "genericTdd", "jammer": "jammer", "microwave_oven": "microwaveOven", "motorola_canopy": "motorolaCanopy", "si_fhss": "siFhss", "spectrum_80211_fh": "spectrum80211Fh", "spectrum_80211_non_standard_channel": "spectrum80211NonStandardChannel", "spectrum_802154": "spectrum802154", "spectrum_inverted": "spectrumInverted", "super_ag": "superAg", "video_camera": "videoCamera", "wimax_fixed": "wimaxFixed", "wimax_mobile": "wimaxMobile", "xbox": "xbox",
}

type wirelessCleanAirAttributes struct {
	Band        string          `json:"radioBand"`
	Enable      bool            `json:"cleanAir"`
	Reporting   bool            `json:"cleanAirDeviceReporting"`
	Propagation bool            `json:"persistentDevicePropagation"`
	Description *string         `json:"description,omitempty"`
	Interferers map[string]bool `json:"interferersFeatures,omitempty"`
}
type wirelessCleanAir struct {
	ID         string                     `json:"id,omitempty"`
	Name       string                     `json:"designName"`
	Attributes wirelessCleanAirAttributes `json:"featureAttributes"`
	Unlocked   []string                   `json:"unlockedAttributes,omitempty"`
}

func validateWirelessCleanAir(p wirelessCleanAir) error {
	if !validProfileName(p.Name) || len(p.Name) > 64 || strings.ContainsAny(p.Name, "%&<>[]'/") || !slices.Contains([]string{"2_4GHZ", "5GHZ", "6GHZ"}, p.Attributes.Band) {
		return errInvalidResponse
	}
	if p.Attributes.Description != nil && (len(*p.Attributes.Description) > 4096 || strings.ContainsRune(*p.Attributes.Description, 0)) {
		return errInvalidResponse
	}
	return nil
}
func parseWirelessCleanAir(raw []byte) ([]wirelessCleanAir, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errors.New("feature_templates requires a section-specific model")
	}
	section, ok := root["feature_templates"].(map[string]any)
	if !ok || len(section) != 1 {
		return nil, errors.New("unsupported feature template domain")
	}
	wireless, ok := section["wireless"].(map[string]any)
	if !ok || len(wireless) != 1 {
		return nil, errors.New("unsupported wireless feature collection")
	}
	list, ok := wireless["cleanair"].([]any)
	if !ok || len(list) > 1000 {
		return nil, errors.New("invalid CleanAir collection")
	}
	fields := map[string]string{"radio_band": "radioBand", "enable": "cleanAir", "device_reporting": "cleanAirDeviceReporting", "persistent_device_propagation": "persistentDevicePropagation", "description": "description"}
	bands := map[string]string{"2.4GHz": "2_4GHZ", "5GHz": "5GHZ", "6GHz": "6GHZ"}
	out := []wirelessCleanAir{}
	seen := map[string]bool{}
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errInvalidResponse
		}
		name, ok := m["name"].(string)
		if !ok || seen[name] {
			return nil, errInvalidResponse
		}
		attrs := map[string]any{"cleanAir": false, "cleanAirDeviceReporting": false, "persistentDevicePropagation": false}
		for key, value := range m {
			if key == "name" {
				continue
			}
			if value == nil {
				return nil, errors.New("null CleanAir field")
			}
			if key == "interferers" {
				settings, ok := value.(map[string]any)
				if !ok {
					return nil, errInvalidResponse
				}
				translated := map[string]bool{}
				for key, value := range settings {
					dest, known := cleanAirInterferers[key]
					flag, ok := value.(bool)
					if !known || !ok {
						return nil, errors.New("unknown or non-boolean interferer")
					}
					translated[dest] = flag
				}
				attrs["interferersFeatures"] = translated
				continue
			}
			dest, ok := fields[key]
			if !ok {
				return nil, errors.New("unsupported CleanAir field")
			}
			attrs[dest] = value
		}
		band, ok := attrs["radioBand"].(string)
		if !ok {
			return nil, errInvalidResponse
		}
		if mapped, ok := bands[band]; ok {
			attrs["radioBand"] = mapped
		}
		raw, _ := json.Marshal(map[string]any{"designName": name, "featureAttributes": attrs})
		var p wirelessCleanAir
		if json.Unmarshal(raw, &p) != nil || validateWirelessCleanAir(p) != nil {
			return nil, errors.New("invalid CleanAir design")
		}
		seen[name] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (c *client) wirelessCleanAirRead(ctx context.Context, instance wirelessFeatureInstance) (wirelessCleanAir, error) {
	value, _, err := c.wirelessCleanAirReadState(ctx, instance)
	return value, err
}
func (c *client) wirelessCleanAirReadState(ctx context.Context, instance wirelessFeatureInstance) (wirelessCleanAir, string, error) {
	raw, err := c.get(ctx, wirelessCleanAirPath+"/"+url.PathEscape(instance.ID), nil, 200)
	if err != nil {
		return wirelessCleanAir{}, "", err
	}
	var response struct {
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return wirelessCleanAir{}, "", errInvalidResponse
	}
	var presence struct {
		Attributes map[string]json.RawMessage `json:"featureAttributes"`
	}
	if json.Unmarshal(response.Response, &presence) != nil {
		return wirelessCleanAir{}, "", errInvalidResponse
	}
	for _, key := range []string{"cleanAir", "cleanAirDeviceReporting", "persistentDevicePropagation"} {
		var value *bool
		if json.Unmarshal(presence.Attributes[key], &value) != nil || value == nil {
			return wirelessCleanAir{}, "", errInvalidResponse
		}
	}
	if raw, ok := presence.Attributes["interferersFeatures"]; ok && string(raw) != "null" {
		var settings map[string]*bool
		if json.Unmarshal(raw, &settings) != nil {
			return wirelessCleanAir{}, "", errInvalidResponse
		}
		for _, value := range settings {
			if value == nil {
				return wirelessCleanAir{}, "", errInvalidResponse
			}
		}
	}
	var p wirelessCleanAir
	if json.Unmarshal(response.Response, &p) != nil || p.ID != instance.ID || p.Name != instance.Name || validateWirelessCleanAir(p) != nil {
		return wirelessCleanAir{}, "", errInvalidResponse
	}
	fingerprint, err := wirelessFeatureNativeFingerprint(response.Response)
	if err != nil {
		return wirelessCleanAir{}, "", err
	}
	return p, fingerprint, nil
}
func wirelessCleanAirMatches(want, got wirelessCleanAir) bool {
	w, g := want.Attributes, got.Attributes
	if want.Name != got.Name || w.Band != g.Band || w.Enable != g.Enable || w.Reporting != g.Reporting || w.Propagation != g.Propagation {
		return false
	}
	if w.Description != nil && (g.Description == nil || *w.Description != *g.Description) {
		return false
	}
	for key, value := range w.Interferers {
		observed, ok := g.Interferers[key]
		if !ok || observed != value {
			return false
		}
	}
	return true
}

type wirelessCleanAirExecutor struct {
	submissionGuard func(context.Context, string, string, configurationResourceIdentity) error
	client          *client
	desired         []wirelessCleanAir
}

func (e *wirelessCleanAirExecutor) count() int { return len(e.desired) }
func (e *wirelessCleanAirExecutor) next(ctx context.Context) (*configurationWrite, error) {
	write, _, err := e.prepareFeature(ctx)
	return write, err
}

func (e *wirelessCleanAirExecutor) prepareFeature(ctx context.Context) (*configurationWrite, func(context.Context) error, error) {
	instances, err := e.client.wirelessFeatureInstances(ctx, wirelessCleanAirKind)
	if err != nil {
		return nil, nil, err
	}
	var first *configurationWrite
	names := make([]string, 0, len(e.desired))
	observed := map[string]wirelessFeatureObservation[wirelessCleanAir]{}
	desiredJSON, err := json.Marshal(e.desired)
	if err != nil {
		return nil, nil, err
	}
	for _, want := range e.desired {
		names = append(names, want.Name)
		instance, exists := instances[want.Name]
		method, path, action := http.MethodPost, wirelessCleanAirPath, "Create"
		identity := configurationResourceIdentity{}
		if exists {
			got, err := e.client.wirelessCleanAirRead(ctx, instance)
			if err != nil {
				return nil, nil, err
			}
			observed[want.Name] = wirelessFeatureObservation[wirelessCleanAir]{Instance: instance, Detail: &got}
			if wirelessCleanAirMatches(want, got) {
				continue
			}
			if *instance.System {
				return nil, nil, errors.New("system feature templates cannot be modified")
			}
			method, path, action = http.MethodPut, wirelessCleanAirPath+"/"+url.PathEscape(got.ID), "Update"
			identity.target = got.ID
			want.Unlocked = got.Unlocked
			if want.Attributes.Description == nil {
				want.Attributes.Description = got.Attributes.Description
			}
			merged := map[string]bool{}
			for key, value := range got.Attributes.Interferers {
				merged[key] = value
			}
			for key, value := range want.Attributes.Interferers {
				merged[key] = value
			}
			want.Attributes.Interferers = merged
		}
		body, _ := json.Marshal(want)
		if first == nil {
			first = &configurationWrite{resource: "cleanair:" + want.Name, action: action, identity: identity, submit: func(ctx context.Context) (Task, error) {

				return e.client.wirelessFeatureMutationTask(ctx, method, path, body)
			}}
		}
	}
	unchanged := func(ctx context.Context) error {
		currentDesired, err := json.Marshal(e.desired)
		if err != nil {
			return err
		}
		if string(currentDesired) != string(desiredJSON) {
			return errConfigurationStale
		}
		fresh, err := wirelessFeatureSnapshot(ctx, e.client, wirelessCleanAirKind, names, e.client.wirelessCleanAirRead)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(observed, fresh) {
			return errConfigurationStale
		}
		return nil
	}
	guardWirelessDefinitionPreflight(first, func(ctx context.Context) error {
		if err := unchanged(ctx); err != nil {
			return err
		}
		if first != nil && e.submissionGuard != nil {
			return e.submissionGuard(ctx, first.resource, first.action, first.identity)
		}
		return nil
	})
	return first, unchanged, nil
}
func (e *wirelessCleanAirExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op.ParentID != "" || op.TargetSerial != "" || (op.Action != "Create" && op.Action != "Update") || (op.Action == "Create" && op.TargetID != "") || (op.Action == "Update" && !validAPIID(op.TargetID)) {
		return false, errConfigurationTransition
	}
	instances, err := e.client.wirelessFeatureInstances(ctx, wirelessCleanAirKind)
	if err != nil {
		return false, err
	}
	for _, want := range e.desired {
		if op.Resource == "cleanair:"+want.Name {
			instance, ok := instances[want.Name]
			if !ok {
				return false, nil
			}
			if *instance.System || op.TargetID != "" && op.TargetID != instance.ID {
				return false, errConfigurationTransition
			}
			got, err := e.client.wirelessCleanAirRead(ctx, instance)
			return err == nil && wirelessCleanAirMatches(want, got), err
		}
	}
	return false, errConfigurationTransition
}
func (e *wirelessCleanAirExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	instances, err := e.client.wirelessFeatureInstances(ctx, wirelessCleanAirKind)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, want := range e.desired {
		instance, ok := instances[want.Name]
		change := configapi.NetworkControllerDriftChangeAdd
		if ok {
			got, err := e.client.wirelessCleanAirRead(ctx, instance)
			if err != nil {
				return nil, err
			}
			if wirelessCleanAirMatches(want, got) {
				continue
			}
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "feature_templates", Resource: "cleanair:" + want.Name, Path: "/wireless/cleanair", Change: change})
	}
	return out, nil
}
