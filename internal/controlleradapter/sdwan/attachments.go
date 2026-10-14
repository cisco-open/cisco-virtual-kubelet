// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

type DeviceAttachmentTarget struct {
	ID        string
	Variables map[string]string
}

func compileDeviceAttachments(sites []SDWANSite, templates []EdgeDeviceTemplate) ([]Resource, error) {
	known := map[string]bool{}
	for _, template := range templates {
		known[template.Name] = true
	}
	byTemplate := map[string][]DeviceAttachmentTarget{}
	for _, site := range sites {
		for _, router := range site.Routers {
			if router.DeviceTemplate == "" {
				if len(router.DeviceVariables) != 0 && router.ConfigurationGroup == "" {
					return nil, errors.New("device variables require a device template")
				}
				continue
			}
			if !known[router.DeviceTemplate] {
				return nil, errors.New("router references undeclared device template")
			}
			if !validID.MatchString(router.ChassisID) {
				return nil, errors.New("invalid attachment chassis ID")
			}
			for name := range router.DeviceVariables {
				if name == "" || strings.TrimSpace(name) != name {
					return nil, errors.New("invalid device variable name")
				}
			}
			byTemplate[router.DeviceTemplate] = append(byTemplate[router.DeviceTemplate], DeviceAttachmentTarget{ID: router.ChassisID, Variables: router.DeviceVariables})
		}
	}
	names := make([]string, 0, len(byTemplate))
	for name := range byTemplate {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Resource, 0, len(names))
	for _, name := range names {
		devices := byTemplate[name]
		sort.Slice(devices, func(i, j int) bool { return devices[i].ID < devices[j].ID })
		out = append(out, Resource{Key: "sites/device_attachments/" + name, Name: name, Section: "sites", Kind: "device-attachment", Type: "attach_feature_device_template", AttachDevices: devices, Body: map[string]any{"devices": len(devices)}})
	}
	return out, nil
}

func (c *Client) attachmentTemplateID(ctx context.Context, name string) (string, error) {
	var list struct {
		Data *[]struct {
			ID         string `json:"templateId"`
			Name       string `json:"templateName"`
			ConfigType string `json:"configType"`
		} `json:"data"`
	}
	if err := c.Request(ctx, "GET", deviceTemplatePath, nil, &list); err != nil {
		return "", err
	}
	if list.Data == nil {
		return "", ErrResponse
	}
	id := ""
	for _, item := range *list.Data {
		if item.Name == name {
			if id != "" || !validID.MatchString(item.ID) || item.ConfigType != "template" {
				return "", errors.New("device template identity conflict")
			}
			id = item.ID
		}
	}
	return id, nil
}

func (c *Client) attachedDevices(ctx context.Context, templateID string) (map[string]string, error) {
	var result struct {
		Data *[]struct {
			ID string `json:"uuid"`
			IP string `json:"deviceIP"`
		} `json:"data"`
	}
	if err := c.Request(ctx, "GET", deviceTemplatePath+"config/attached/"+url.PathEscape(templateID), nil, &result); err != nil {
		return nil, err
	}
	if result.Data == nil {
		return nil, ErrResponse
	}
	devices := map[string]string{}
	for _, device := range *result.Data {
		if !validID.MatchString(device.ID) || devices[device.ID] != "" {
			return nil, ErrResponse
		}
		devices[device.ID] = device.IP
	}
	return devices, nil
}

var attachmentVariableTitle = regexp.MustCompile(`\(([^()]*)\)`)

func (c *Client) attachmentInput(ctx context.Context, templateID, deviceID string, edited bool) (map[string]any, map[string][]string, error) {
	body := map[string]any{"templateId": templateID, "deviceIds": []string{deviceID}, "isEdited": edited, "isMasterEdited": false}
	var result struct {
		Header struct {
			Columns []struct {
				Property string `json:"property"`
				Title    string `json:"title"`
				Editable bool   `json:"editable"`
			} `json:"columns"`
		} `json:"header"`
		Data []map[string]any `json:"data"`
	}
	if err := c.Request(ctx, "POST", deviceTemplatePath+"config/input", body, &result); err != nil {
		return nil, nil, err
	}
	if len(result.Data) != 1 || result.Data[0]["csv-deviceId"] != deviceID {
		return nil, nil, ErrResponse
	}
	properties := map[string][]string{}
	add := func(name, property string) {
		for _, existing := range properties[name] {
			if existing == property {
				return
			}
		}
		properties[name] = append(properties[name], property)
	}
	for _, column := range result.Header.Columns {
		if !column.Editable || column.Property == "" {
			continue
		}
		add(column.Property, column.Property)
		matches := attachmentVariableTitle.FindAllStringSubmatch(column.Title, -1)
		if len(matches) > 0 {
			add(matches[len(matches)-1][1], column.Property)
		}
		switch column.Property {
		case "//system/host-name":
			add("system_host_name", column.Property)
		case "//system/system-ip":
			add("system_system_ip", column.Property)
		case "//system/site-id":
			add("system_site_id", column.Property)
		}
	}
	return result.Data[0], properties, nil
}

func attachmentValues(row map[string]any, properties map[string][]string, variables map[string]string) (map[string]string, error) {
	values := map[string]string{}
	for name := range variables {
		paths := properties[name]
		if len(paths) == 0 {
			return nil, fmt.Errorf("unknown device template variable %s", name)
		}
		for _, property := range paths {
			var value string
			switch raw := row[property].(type) {
			case string:
				value = raw
			case float64, bool:
				value = fmt.Sprint(raw)
			default:
				return nil, ErrResponse
			}
			if prior, exists := values[name]; exists && prior != value {
				return nil, errors.New("device variable has conflicting property values")
			}
			values[name] = value
		}
	}
	return values, nil
}

func (c *Client) observeDeviceAttachment(ctx context.Context, r Resource) (*Observation, error) {
	id, err := c.attachmentTemplateID(ctx, r.Name)
	if err != nil || id == "" {
		return nil, err
	}
	attached, err := c.attachedDevices(ctx, id)
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	for _, target := range r.AttachDevices {
		if _, ok := attached[target.ID]; !ok {
			return nil, nil
		}
		row, properties, err := c.attachmentInput(ctx, id, target.ID, false)
		if err != nil {
			return nil, err
		}
		deviceValues, err := attachmentValues(row, properties, target.Variables)
		if err != nil {
			return nil, err
		}
		values[target.ID] = deviceValues
	}
	return &Observation{ID: id, Body: map[string]any{"devices": values}}, nil
}

func (c *Client) attachDeviceTemplate(ctx context.Context, r Resource, edited bool) (string, error) {
	id, err := c.attachmentTemplateID(ctx, r.Name)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", ErrDependencyPending
	}
	devices := make([]any, 0, len(r.AttachDevices))
	for _, target := range r.AttachDevices {
		row, properties, err := c.attachmentInput(ctx, id, target.ID, edited)
		if err != nil {
			return "", err
		}
		for name, value := range target.Variables {
			paths := properties[name]
			if len(paths) == 0 {
				return "", fmt.Errorf("unknown device template variable %s", name)
			}
			for _, property := range paths {
				row[property] = value
			}
		}
		devices = append(devices, row)
	}
	body := map[string]any{"deviceTemplateList": []any{map[string]any{"templateId": id, "device": devices, "isEdited": edited, "isMasterEdited": false, "isDraftDisabled": false}}}
	var result struct {
		TaskID string `json:"id"`
	}
	if err := c.Request(ctx, "POST", deviceTemplatePath+"config/attachfeature", body, &result); err != nil {
		return "", err
	}
	if err := c.waitCustomApplicationTask(ctx, result.TaskID); err != nil {
		return "", err
	}
	return id, nil
}

func equalDeviceAttachment(r Resource, observed map[string]any) (bool, error) {
	devices, ok := observed["devices"].(map[string]any)
	if !ok {
		return false, ErrResponse
	}
	for _, target := range r.AttachDevices {
		values, ok := devices[target.ID].(map[string]string)
		if !ok {
			return false, ErrResponse
		}
		for name, value := range target.Variables {
			if values[name] != value {
				return false, nil
			}
		}
	}
	return true, nil
}
