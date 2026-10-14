// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type GroupDeviceTarget struct {
	ID            string
	TopologyLabel string
	Deploy        bool
	Variables     map[string]any
}

func compileGroupDeployments(sites []SDWANSite, configGroups []ConfigurationGroup, policyGroups []PolicyGroup, defaults *Defaults) ([]Resource, error) {
	known := map[string]bool{}
	for _, group := range configGroups {
		known["configuration_groups/"+group.Name] = true
	}
	for _, group := range policyGroups {
		known["policy_groups/"+group.Name] = true
	}
	byGroup := map[string][]GroupDeviceTarget{}
	configDeploy, policyDeploy := true, true
	if defaults != nil {
		if value := defaults.SDWAN.Sites.Routers.ConfigurationGroupDeploy; value != nil {
			configDeploy = *value
		}
		if value := defaults.SDWAN.Sites.Routers.PolicyGroupDeploy; value != nil {
			policyDeploy = *value
		}
	}
	for _, site := range sites {
		for _, router := range site.Routers {
			for _, spec := range []struct {
				section, name, label string
				deploy               *bool
				defaultDeploy        bool
				variables            map[string]any
			}{
				{"configuration_groups", router.ConfigurationGroup, router.TopologyLabel, router.ConfigurationGroupDeploy, configDeploy, stringVariables(router.DeviceVariables)},
				{"policy_groups", router.PolicyGroup, "", router.PolicyGroupDeploy, policyDeploy, router.PolicyVariables},
			} {
				if spec.name == "" {
					if spec.deploy != nil {
						return nil, errors.New("group deploy flag requires a group")
					}
					if spec.section == "policy_groups" && len(spec.variables) > 0 {
						return nil, errors.New("policy variables require a policy group")
					}
					if spec.section == "configuration_groups" && spec.label != "" {
						return nil, errors.New("topology label requires a configuration group")
					}
					continue
				}
				key := spec.section + "/" + spec.name
				if !known[key] {
					return nil, fmt.Errorf("router references undeclared %s", spec.section)
				}
				deploy := spec.defaultDeploy
				if spec.deploy != nil {
					deploy = *spec.deploy
				}
				for name, value := range spec.variables {
					if name == "" || strings.TrimSpace(name) != name || value == nil {
						return nil, errors.New("invalid group device variable")
					}
					switch value.(type) {
					case string, bool, int, float64, []any:
					default:
						return nil, errors.New("unsupported group device variable value")
					}
				}
				byGroup[key] = append(byGroup[key], GroupDeviceTarget{ID: router.ChassisID, TopologyLabel: spec.label, Deploy: deploy, Variables: spec.variables})
			}
		}
	}
	keys := make([]string, 0, len(byGroup))
	for key := range byGroup {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]Resource, 0, len(keys))
	for _, key := range keys {
		devices := byGroup[key]
		sort.Slice(devices, func(i, j int) bool { return devices[i].ID < devices[j].ID })
		parts := strings.SplitN(key, "/", 2)
		groupType, collection := "configuration_group", "/dataservice/v1/config-group"
		if parts[0] == "policy_groups" {
			groupType, collection = "policy_group", "/dataservice/v1/policy-group"
		}
		out = append(out, Resource{Key: key + "/devices", Name: parts[1], Section: parts[0], Kind: "group-deployment", Type: groupType, Collection: collection, GroupDevices: devices, Body: map[string]any{"devices": len(devices)}})
	}
	return out, nil
}

func stringVariables(values map[string]string) map[string]any {
	result := map[string]any{}
	for key, value := range values {
		result[key] = value
	}
	return result
}

func (c *Client) groupDeploymentID(ctx context.Context, r Resource) (string, error) {
	var list []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.Request(ctx, "GET", r.Collection, nil, &list); err != nil {
		return "", err
	}
	if list == nil {
		return "", ErrResponse
	}
	id := ""
	for _, item := range list {
		if item.Name == r.Name {
			if id != "" || !validID.MatchString(item.ID) {
				return "", ErrResponse
			}
			id = item.ID
		}
	}
	return id, nil
}

func (c *Client) groupAssociatedDevices(ctx context.Context, r Resource, id string) (map[string]map[string]any, error) {
	var result struct {
		Devices *[]map[string]any `json:"devices"`
	}
	if err := c.Request(ctx, "GET", r.Collection+"/"+url.PathEscape(id)+"/device/associate/", nil, &result); err != nil {
		return nil, err
	}
	if result.Devices == nil {
		return nil, ErrResponse
	}
	devices := map[string]map[string]any{}
	for _, device := range *result.Devices {
		name, ok := device["id"].(string)
		if !ok || !validID.MatchString(name) || devices[name] != nil {
			return nil, ErrResponse
		}
		devices[name] = device
	}
	return devices, nil
}

func (c *Client) observeGroupDeployment(ctx context.Context, r Resource) (*Observation, error) {
	id, err := c.groupDeploymentID(ctx, r)
	if err != nil || id == "" {
		return nil, err
	}
	associated, err := c.groupAssociatedDevices(ctx, r, id)
	if err != nil {
		return nil, err
	}
	variables := map[string]map[string]any{}
	needVariables := false
	for _, target := range r.GroupDevices {
		needVariables = needVariables || len(target.Variables) > 0
	}
	if needVariables {
		var result struct {
			Devices *[]struct {
				ID        string `json:"device-id"`
				Variables []struct {
					Name  string `json:"name"`
					Value any    `json:"value"`
				} `json:"variables"`
			} `json:"devices"`
		}
		if err := c.Request(ctx, "GET", r.Collection+"/"+url.PathEscape(id)+"/device/variables/", nil, &result); err != nil {
			return nil, err
		}
		if result.Devices == nil {
			return nil, ErrResponse
		}
		for _, device := range *result.Devices {
			if !validID.MatchString(device.ID) || variables[device.ID] != nil {
				return nil, ErrResponse
			}
			values := map[string]any{}
			for _, variable := range device.Variables {
				if variable.Name != "" && variable.Value != nil {
					values[variable.Name] = variable.Value
				}
			}
			variables[device.ID] = values
		}
	}
	observed := map[string]any{}
	for _, target := range r.GroupDevices {
		device, ok := associated[target.ID]
		if !ok {
			return nil, nil
		}
		label, _ := device["groupTopologyLabel"].(string)
		upToDate := !target.Deploy
		if target.Deploy {
			field := "configGroupUpToDate"
			if r.Type == "policy_group" {
				field = "policyGroupUpToDate"
			}
			upToDate, _ = device[field].(bool)
		}
		observed[target.ID] = map[string]any{"label": label, "up_to_date": upToDate, "variables": variables[target.ID]}
	}
	return &Observation{ID: id, Body: map[string]any{"devices": observed}}, nil
}

func equalGroupDeployment(r Resource, body map[string]any) (bool, error) {
	devices, ok := body["devices"].(map[string]any)
	if !ok {
		return false, ErrResponse
	}
	for _, target := range r.GroupDevices {
		device, ok := devices[target.ID].(map[string]any)
		if !ok {
			return false, ErrResponse
		}
		if r.Type == "configuration_group" && device["label"] != target.TopologyLabel {
			return false, nil
		}
		if target.Deploy && device["up_to_date"] == false {
			return false, nil
		}
		actual, _ := device["variables"].(map[string]any)
		for name, wanted := range target.Variables {
			lookup := name
			if r.Type == "configuration_group" && lookup == "region_id" {
				lookup = "region-id"
			}
			if !equalGroupVariable(wanted, actual[lookup]) {
				return false, nil
			}
		}
	}
	return true, nil
}

func equalGroupVariable(wanted, actual any) bool {
	if reflect.DeepEqual(wanted, actual) {
		return true
	}
	if wanted == nil || actual == nil {
		return false
	}
	w, wok := wanted.([]any)
	a, aok := actual.([]any)
	if wok || aok {
		if !wok || !aok || len(w) != len(a) {
			return false
		}
		for i := range w {
			if !equalGroupVariable(w[i], a[i]) {
				return false
			}
		}
		return true
	}
	return fmt.Sprint(wanted) == fmt.Sprint(actual)
}

func (c *Client) groupVariableTypes(ctx context.Context, base string) map[string]string {
	var schema []struct {
		Variables []struct {
			Schema struct {
				Properties map[string]struct {
					Properties struct {
						Value struct {
							Type  string `json:"type"`
							Items struct {
								Type string `json:"type"`
							} `json:"items"`
						} `json:"value"`
					} `json:"properties"`
				} `json:"properties"`
			} `json:"schema"`
		} `json:"variables"`
	}
	result := map[string]string{}
	if err := c.Request(ctx, "GET", base+"variables/schema", nil, &schema); err != nil {
		return result
	}
	for _, item := range schema {
		for _, variable := range item.Variables {
			for name, field := range variable.Schema.Properties {
				kind := field.Properties.Value.Type
				if kind == "array" {
					kind = field.Properties.Value.Items.Type
				}
				result[name] = kind
			}
		}
	}
	return result
}

func groupVariableValue(value any, kind string) (any, error) {
	if values, ok := value.([]any); ok {
		out := make([]any, len(values))
		for i, item := range values {
			converted, err := groupVariableValue(item, kind)
			if err != nil {
				return nil, err
			}
			out[i] = converted
		}
		return out, nil
	}
	text := fmt.Sprint(value)
	switch kind {
	case "integer":
		return strconv.Atoi(text)
	case "number":
		return strconv.ParseFloat(text, 64)
	case "boolean":
		return strconv.ParseBool(text)
	default:
		return value, nil
	}
}

func (c *Client) applyGroupDeployment(ctx context.Context, r Resource) (string, error) {
	id, err := c.groupDeploymentID(ctx, r)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", ErrDependencyPending
	}
	associated, err := c.groupAssociatedDevices(ctx, r, id)
	if err != nil {
		return "", err
	}
	missing := []any{}
	for _, target := range r.GroupDevices {
		if current, exists := associated[target.ID]; exists {
			if r.Type != "configuration_group" || current["groupTopologyLabel"] == target.TopologyLabel || (current["groupTopologyLabel"] == nil && target.TopologyLabel == "") {
				continue
			}
		}
		device := map[string]any{"id": target.ID}
		if r.Type == "configuration_group" && target.TopologyLabel != "" {
			device["groupTopologyLabel"] = target.TopologyLabel
		}
		missing = append(missing, device)
	}
	base := r.Collection + "/" + url.PathEscape(id) + "/device/"
	if len(missing) > 0 {
		method := "PUT"
		if len(associated) == 0 {
			method = "POST"
		}
		if err := c.Request(ctx, method, base+"associate/", map[string]any{"devices": missing}, nil); err != nil {
			return "", err
		}
	}
	variables := []any{}
	varTypes := map[string]string{}
	for _, target := range r.GroupDevices {
		if len(target.Variables) > 0 {
			varTypes = c.groupVariableTypes(ctx, base)
			break
		}
	}
	for _, target := range r.GroupDevices {
		if len(target.Variables) == 0 {
			continue
		}
		keys := make([]string, 0, len(target.Variables))
		for key := range target.Variables {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		items := []any{}
		for _, key := range keys {
			name := key
			if r.Type == "configuration_group" && name == "region_id" {
				name = "region-id"
			}
			value, err := groupVariableValue(target.Variables[key], varTypes[name])
			if err != nil {
				return "", fmt.Errorf("invalid group variable %s: %w", name, err)
			}
			items = append(items, map[string]any{"name": name, "value": value})
		}
		variables = append(variables, map[string]any{"device-id": target.ID, "variables": items})
	}
	if len(variables) > 0 {
		if err := c.Request(ctx, "PUT", base+"variables/", map[string]any{"solution": "sdwan", "devices": variables}, nil); err != nil {
			return "", err
		}
	}
	deploy := []any{}
	for _, target := range r.GroupDevices {
		if target.Deploy {
			deploy = append(deploy, map[string]any{"id": target.ID})
		}
	}
	if len(deploy) > 0 {
		var result struct {
			TaskID string `json:"parentTaskId"`
		}
		if err := c.Request(ctx, "POST", base+"deploy/", map[string]any{"devices": deploy}, &result); err != nil {
			return "", err
		}
		if err := c.waitCustomApplicationTask(ctx, result.TaskID); err != nil {
			return "", err
		}
	}
	return id, nil
}
