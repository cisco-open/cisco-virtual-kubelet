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

const wirelessFRAPath = "/dna/intent/api/v1/featureTemplates/wireless/rrmFraConfigurations"
const wirelessFRAKind = "RRM_FRA_CONFIGURATION"

type wirelessFRAAttributes struct {
	Band        string `json:"radioBand"`
	Freeze      bool   `json:"fraFreeze"`
	Status      bool   `json:"fraStatus"`
	Interval    int    `json:"fraInterval"`
	Sensitivity string `json:"fraSensitivity,omitempty"`
}
type wirelessFRA struct {
	ID         string                `json:"id,omitempty"`
	Name       string                `json:"designName"`
	Attributes wirelessFRAAttributes `json:"featureAttributes"`
	Unlocked   []string              `json:"unlockedAttributes,omitempty"`
}

func validateWirelessFRA(p wirelessFRA) error {
	if !validProfileName(p.Name) || len(p.Name) > 64 || strings.ContainsAny(p.Name, "%&<>[]'/") {
		return errInvalidResponse
	}
	a := p.Attributes
	if a.Band != "2_4GHZ_5GHZ" && a.Band != "5GHZ_6GHZ" || a.Interval < 1 || a.Interval > 24 {
		return errInvalidResponse
	}
	if a.Sensitivity != "" && (a.Band != "2_4GHZ_5GHZ" || !slices.Contains([]string{"Low", "Medium", "High", "Higher", "Even Higher", "Super High"}, a.Sensitivity)) {
		return errInvalidResponse
	}
	return nil
}
func parseWirelessFRA(raw []byte) ([]wirelessFRA, error) {
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
		return nil, errors.New("unsupported wireless feature template collection")
	}
	list, ok := wireless["rrm_fra"].([]any)
	if !ok || len(list) > 1000 {
		return nil, errors.New("invalid RRM FRA collection")
	}
	out := []wirelessFRA{}
	seen := map[string]bool{}
	sensitivity := map[string]string{"LOW": "Low", "MEDIUM": "Medium", "HIGH": "High", "HIGHER": "Higher", "EVEN_HIGHER": "Even Higher", "SUPER_HIGH": "Super High"}
	fields := map[string]string{"radio_band": "radioBand", "fra_freeze": "fraFreeze", "fra_status": "fraStatus", "fra_interval": "fraInterval", "fra_sensitivity": "fraSensitivity"}
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errInvalidResponse
		}
		name, ok := m["name"].(string)
		if !ok || seen[name] {
			return nil, errInvalidResponse
		}
		attrs := map[string]any{"fraFreeze": false, "fraStatus": false, "fraInterval": 3}
		for key, value := range m {
			if key == "name" {
				continue
			}
			dest, ok := fields[key]
			if !ok || value == nil {
				return nil, errors.New("unsupported or null FRA field")
			}
			attrs[dest] = value
		}
		band, ok := attrs["radioBand"].(string)
		if !ok {
			return nil, errInvalidResponse
		}
		if band == "2.4GHz_5GHz" {
			attrs["radioBand"] = "2_4GHZ_5GHZ"
		} else if band == "5GHz_6GHz" {
			attrs["radioBand"] = "5GHZ_6GHZ"
		}
		// The pinned module emits sensitivity only for the model's 2.4GHz label.
		if band == "2.4GHz_5GHz" {
			if _, ok := attrs["fraSensitivity"]; !ok {
				attrs["fraSensitivity"] = "Medium"
			}
			if s, ok := attrs["fraSensitivity"].(string); ok {
				if mapped, ok := sensitivity[s]; ok {
					attrs["fraSensitivity"] = mapped
				}
			}
		} else if _, ok := attrs["fraSensitivity"]; ok {
			return nil, errors.New("fra_sensitivity requires radio_band 2.4GHz_5GHz")
		}
		body, _ := json.Marshal(map[string]any{"designName": name, "featureAttributes": attrs})
		var p wirelessFRA
		if json.Unmarshal(body, &p) != nil || validateWirelessFRA(p) != nil {
			return nil, errors.New("invalid FRA design")
		}
		seen[name] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (c *client) wirelessFRARead(ctx context.Context, instance wirelessFeatureInstance) (wirelessFRA, error) {
	value, _, err := c.wirelessFRAReadState(ctx, instance)
	return value, err
}
func (c *client) wirelessFRAReadState(ctx context.Context, instance wirelessFeatureInstance) (wirelessFRA, string, error) {
	raw, err := c.get(ctx, wirelessFRAPath+"/"+url.PathEscape(instance.ID), nil, 200)
	if err != nil {
		return wirelessFRA{}, "", err
	}
	var response struct {
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return wirelessFRA{}, "", errInvalidResponse
	}
	var presence struct {
		Attributes map[string]json.RawMessage `json:"featureAttributes"`
	}
	if json.Unmarshal(response.Response, &presence) != nil {
		return wirelessFRA{}, "", errInvalidResponse
	}
	for _, key := range []string{"fraFreeze", "fraStatus"} {
		var value *bool
		if json.Unmarshal(presence.Attributes[key], &value) != nil || value == nil {
			return wirelessFRA{}, "", errInvalidResponse
		}
	}
	var p wirelessFRA
	if json.Unmarshal(response.Response, &p) != nil || p.ID != instance.ID || p.Name != instance.Name || validateWirelessFRA(p) != nil {
		return wirelessFRA{}, "", errInvalidResponse
	}
	fingerprint, err := wirelessFeatureNativeFingerprint(response.Response)
	if err != nil {
		return wirelessFRA{}, "", err
	}
	return p, fingerprint, nil
}
func wirelessFRAMatches(want, got wirelessFRA) bool {
	w, g := want.Attributes, got.Attributes
	return want.Name == got.Name && w.Band == g.Band && w.Freeze == g.Freeze && w.Status == g.Status && w.Interval == g.Interval && (w.Sensitivity == "" || w.Sensitivity == g.Sensitivity)
}

type wirelessFRAExecutor struct {
	submissionGuard func(context.Context, string, string, configurationResourceIdentity) error
	client          *client
	desired         []wirelessFRA
}

func (e *wirelessFRAExecutor) count() int { return len(e.desired) }
func (e *wirelessFRAExecutor) next(ctx context.Context) (*configurationWrite, error) {
	write, _, err := e.prepareFeature(ctx)
	return write, err
}

func (e *wirelessFRAExecutor) prepareFeature(ctx context.Context) (*configurationWrite, func(context.Context) error, error) {
	instances, err := e.client.wirelessFeatureInstances(ctx, wirelessFRAKind)
	if err != nil {
		return nil, nil, err
	}
	var first *configurationWrite
	names := make([]string, 0, len(e.desired))
	observed := map[string]wirelessFeatureObservation[wirelessFRA]{}
	desiredJSON, err := json.Marshal(e.desired)
	if err != nil {
		return nil, nil, err
	}
	for _, want := range e.desired {
		names = append(names, want.Name)
		instance, exists := instances[want.Name]
		method, path, action := http.MethodPost, wirelessFRAPath, "Create"
		identity := configurationResourceIdentity{}
		if exists {
			got, err := e.client.wirelessFRARead(ctx, instance)
			if err != nil {
				return nil, nil, err
			}
			observed[want.Name] = wirelessFeatureObservation[wirelessFRA]{Instance: instance, Detail: &got}
			if wirelessFRAMatches(want, got) {
				continue
			}
			if *instance.System {
				return nil, nil, errors.New("system feature templates cannot be modified")
			}
			method, path, action = http.MethodPut, wirelessFRAPath+"/"+url.PathEscape(got.ID), "Update"
			identity.target = got.ID
			want.Unlocked = got.Unlocked
			if want.Attributes.Sensitivity == "" && want.Attributes.Band == got.Attributes.Band {
				want.Attributes.Sensitivity = got.Attributes.Sensitivity
			}
		}
		body, _ := json.Marshal(want)
		if first == nil {
			first = &configurationWrite{resource: "rrm-fra:" + want.Name, action: action, identity: identity, submit: func(ctx context.Context) (Task, error) {

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
		fresh, err := wirelessFeatureSnapshot(ctx, e.client, wirelessFRAKind, names, e.client.wirelessFRARead)
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
func (e *wirelessFRAExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op.ParentID != "" || op.TargetSerial != "" || (op.Action != "Create" && op.Action != "Update") || (op.Action == "Create" && op.TargetID != "") || (op.Action == "Update" && !validAPIID(op.TargetID)) {
		return false, errConfigurationTransition
	}
	instances, err := e.client.wirelessFeatureInstances(ctx, wirelessFRAKind)
	if err != nil {
		return false, err
	}
	for _, want := range e.desired {
		if op.Resource == "rrm-fra:"+want.Name {
			instance, ok := instances[want.Name]
			if !ok {
				return false, nil
			}
			if *instance.System || op.TargetID != "" && op.TargetID != instance.ID {
				return false, errConfigurationTransition
			}
			got, err := e.client.wirelessFRARead(ctx, instance)
			return err == nil && wirelessFRAMatches(want, got), err
		}
	}
	return false, errConfigurationTransition
}
func (e *wirelessFRAExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	instances, err := e.client.wirelessFeatureInstances(ctx, wirelessFRAKind)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, want := range e.desired {
		instance, ok := instances[want.Name]
		change := configapi.NetworkControllerDriftChangeAdd
		if ok {
			got, err := e.client.wirelessFRARead(ctx, instance)
			if err != nil {
				return nil, err
			}
			if wirelessFRAMatches(want, got) {
				continue
			}
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "feature_templates", Resource: "rrm-fra:" + want.Name, Path: "/wireless/rrm_fra", Change: change})
	}
	return out, nil
}
