// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

type EdgeDeviceTemplate struct {
	Name                   string `json:"name"`
	Description            string `json:"description"`
	DeviceModel            string `json:"device_model"`
	SystemTemplate         string `json:"system_template,omitempty"`
	AAATemplate            string `json:"aaa_template,omitempty"`
	BFDTemplate            string `json:"bfd_template,omitempty"`
	OMPTemplate            string `json:"omp_template,omitempty"`
	SecurityTemplate       string `json:"security_template,omitempty"`
	BannerTemplate         string `json:"banner_template,omitempty"`
	SNMPTemplate           string `json:"snmp_template,omitempty"`
	GlobalSettingsTemplate string `json:"global_settings_template,omitempty"`
	CLITemplate            string `json:"cli_template,omitempty"`
	ThousandEyesTemplate   string `json:"thousandeyes_template,omitempty"`
}
type DeviceTemplateReference struct {
	Name, Type string
	Index      int
}

const deviceTemplatePath = "/dataservice/template/device/"

func compileDeviceTemplates(items []EdgeDeviceTemplate) ([]Resource, error) {
	out := []Resource{}
	seen := map[string]bool{}
	for _, item := range items {
		if item.Name == "" || len(item.Name) > 128 || item.Description == "" || item.DeviceModel == "" || seen[item.Name] {
			return nil, errors.New("invalid or duplicate edge device template")
		}
		seen[item.Name] = true
		general := []any{}
		refs := []DeviceTemplateReference{}
		for _, ref := range []struct{ name, kind string }{
			{item.SystemTemplate, "cisco_system"}, {item.AAATemplate, "cedge_aaa"}, {item.BFDTemplate, "cisco_bfd"}, {item.OMPTemplate, "cisco_omp"},
			{item.SecurityTemplate, "cisco_security"}, {item.BannerTemplate, "cisco_banner"}, {item.SNMPTemplate, "cisco_snmp"}, {item.GlobalSettingsTemplate, "cedge_global"},
			{item.CLITemplate, "cli-template"}, {item.ThousandEyesTemplate, "cisco_thousandeyes"},
		} {
			if ref.name == "" {
				continue
			}
			refs = append(refs, DeviceTemplateReference{Name: ref.name, Type: ref.kind, Index: len(general)})
			general = append(general, map[string]any{"templateId": "", "templateType": ref.kind, "subTemplates": []any{}})
		}
		if len(general) == 0 {
			return nil, errors.New("edge device template needs a feature template")
		}
		body := map[string]any{"configType": "template", "factoryDefault": false, "featureTemplateUidRange": []any{}, "templateName": item.Name, "templateDescription": item.Description, "deviceType": "vedge-" + item.DeviceModel, "deviceRole": "sdwan-edge", "policyId": "", "generalTemplates": general}
		out = append(out, Resource{Key: "edge_device_templates/" + item.Name, Name: item.Name, Section: "edge_device_templates", Type: "feature_device_template", Kind: "device-template", DeviceTemplateRefs: refs, Body: body})
	}
	return out, nil
}

func (c *Client) resolveDeviceTemplateRefs(ctx context.Context, r Resource) error {
	var list struct {
		Data *[]struct {
			ID   string `json:"templateId"`
			Name string `json:"templateName"`
			Type string `json:"templateType"`
		} `json:"data"`
	}
	if e := c.Request(ctx, "GET", "/dataservice/template/feature", nil, &list); e != nil {
		return e
	}
	if list.Data == nil {
		return ErrResponse
	}
	general, ok := r.Body["generalTemplates"].([]any)
	if !ok {
		return ErrResponse
	}
	for _, ref := range r.DeviceTemplateRefs {
		id := ""
		for _, item := range *list.Data {
			if item.Name == ref.Name && item.Type == ref.Type {
				if id != "" || !validID.MatchString(item.ID) {
					return ErrResponse
				}
				id = item.ID
			}
		}
		if id == "" {
			return ErrDependencyPending
		}
		if ref.Index < 0 || ref.Index >= len(general) {
			return ErrResponse
		}
		row, ok := general[ref.Index].(map[string]any)
		if !ok {
			return ErrResponse
		}
		row["templateId"] = id
	}
	return nil
}
func (c *Client) observeDeviceTemplate(ctx context.Context, r Resource) (*Observation, error) {
	if e := c.resolveDeviceTemplateRefs(ctx, r); e != nil {
		if errors.Is(e, ErrDependencyPending) {
			return nil, nil
		}
		return nil, e
	}
	var list struct {
		Data *[]struct {
			ID   string `json:"templateId"`
			Name string `json:"templateName"`
			Type string `json:"configType"`
		} `json:"data"`
	}
	if e := c.Request(ctx, "GET", deviceTemplatePath, nil, &list); e != nil {
		return nil, e
	}
	if list.Data == nil {
		return nil, ErrResponse
	}
	id := ""
	for _, item := range *list.Data {
		if item.Name == r.Name {
			if id != "" || !validID.MatchString(item.ID) || item.Type != "template" {
				return nil, ErrResponse
			}
			id = item.ID
		}
	}
	if id == "" {
		return nil, nil
	}
	var body map[string]any
	if e := c.Request(ctx, "GET", deviceTemplatePath+"object/"+url.PathEscape(id), nil, &body); e != nil {
		return nil, e
	}
	if body["templateName"] != r.Name {
		return nil, ErrResponse
	}
	attached := false
	if count, ok := body["devicesAttached"].(float64); ok && count > 0 {
		attached = true
	}
	return &Observation{ID: id, Body: body, Attached: attached}, nil
}
func equalDeviceTemplate(r Resource, body map[string]any) (bool, error) {
	for _, key := range []string{"configType", "templateName", "templateDescription", "deviceType", "deviceRole", "policyId"} {
		if body[key] != r.Body[key] {
			return false, nil
		}
	}
	want := r.Body["generalTemplates"].([]any)
	got, ok := body["generalTemplates"].([]any)
	if !ok {
		return false, ErrResponse
	}
	if len(got) != len(want) {
		return false, nil
	}
	matched := map[int]bool{}
	for _, raw := range want {
		w := raw.(map[string]any)
		found := false
		for i, other := range got {
			if matched[i] {
				continue
			}
			g, ok := other.(map[string]any)
			if !ok {
				return false, ErrResponse
			}
			if g["templateId"] == w["templateId"] && g["templateType"] == w["templateType"] {
				matched[i] = true
				found = true
				break
			}
		}
		if !found {
			return false, nil
		}
	}
	return true, nil
}
func (c *Client) createDeviceTemplate(ctx context.Context, r Resource) (string, error) {
	if e := c.resolveDeviceTemplateRefs(ctx, r); e != nil {
		return "", e
	}
	var result struct {
		ID string `json:"templateId"`
	}
	if e := c.Request(ctx, "POST", deviceTemplatePath+"feature/", r.Body, &result); e != nil {
		return "", e
	}
	if !validID.MatchString(result.ID) {
		return "", ErrResponse
	}
	return result.ID, nil
}
func (c *Client) updateDeviceTemplate(ctx context.Context, r Resource, o *Observation) error {
	if e := c.resolveDeviceTemplateRefs(ctx, r); e != nil {
		return e
	}
	return c.Request(ctx, "PUT", deviceTemplatePath+url.PathEscape(o.ID), r.Body, nil)
}
func (c *Client) deleteDeviceTemplate(ctx context.Context, id string) error {
	if !validID.MatchString(id) {
		return fmt.Errorf("invalid device template identity")
	}
	return c.Request(ctx, "DELETE", deviceTemplatePath+url.PathEscape(id), nil, nil)
}
