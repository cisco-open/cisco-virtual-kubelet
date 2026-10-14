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
	Name                        string                    `json:"name"`
	Description                 string                    `json:"description"`
	DeviceModel                 string                    `json:"device_model"`
	SystemTemplate              string                    `json:"system_template,omitempty"`
	AAATemplate                 string                    `json:"aaa_template,omitempty"`
	BFDTemplate                 string                    `json:"bfd_template,omitempty"`
	OMPTemplate                 string                    `json:"omp_template,omitempty"`
	SecurityTemplate            string                    `json:"security_template,omitempty"`
	BannerTemplate              string                    `json:"banner_template,omitempty"`
	SNMPTemplate                string                    `json:"snmp_template,omitempty"`
	GlobalSettingsTemplate      string                    `json:"global_settings_template,omitempty"`
	CLITemplate                 string                    `json:"cli_template,omitempty"`
	ThousandEyesTemplate        string                    `json:"thousandeyes_template,omitempty"`
	LoggingTemplate             string                    `json:"logging_template,omitempty"`
	NTPTemplate                 string                    `json:"ntp_template,omitempty"`
	VPN0Template                *DeviceVPNReference       `json:"vpn_0_template,omitempty"`
	VPN512Template              *DeviceVPNReference       `json:"vpn_512_template,omitempty"`
	VPNServiceTemplates         []DeviceVPNReference      `json:"vpn_service_templates,omitempty"`
	SwitchportTemplates         []DeviceNamedReference    `json:"switchport_templates,omitempty"`
	CellularControllerTemplates []DeviceCellularReference `json:"cellular_controller_templates,omitempty"`
}
type DeviceNamedReference struct {
	Name               string `json:"name"`
	DHCPServerTemplate string `json:"dhcp_server_template,omitempty"`
}
type DeviceCellularReference struct {
	Name                     string                 `json:"name"`
	CellularProfileTemplates []DeviceNamedReference `json:"cellular_profile_templates,omitempty"`
}
type DeviceVPNReference struct {
	Name                          string                 `json:"name"`
	OSPFTemplate                  string                 `json:"ospf_template,omitempty"`
	BGPTemplate                   string                 `json:"bgp_template,omitempty"`
	IGMPTemplate                  string                 `json:"igmp_template,omitempty"`
	MulticastTemplate             string                 `json:"multicast_template,omitempty"`
	PIMTemplate                   string                 `json:"pim_template,omitempty"`
	SigCredentialsTemplate        string                 `json:"sig_credentials_template,omitempty"`
	SecureInternetGatewayTemplate string                 `json:"secure_internet_gateway_template,omitempty"`
	EthernetInterfaceTemplates    []DeviceNamedReference `json:"ethernet_interface_templates,omitempty"`
	IPsecInterfaceTemplates       []DeviceNamedReference `json:"ipsec_interface_templates,omitempty"`
	SVIInterfaceTemplates         []DeviceNamedReference `json:"svi_interface_templates,omitempty"`
	GREInterfaceTemplates         []DeviceNamedReference `json:"gre_interface_templates,omitempty"`
	CellularInterfaceTemplates    []DeviceNamedReference `json:"cellular_interface_templates,omitempty"`
}
type DeviceTemplateReference struct {
	Name, Type string
	Index      int
	Path       []int
}

const deviceTemplatePath = "/dataservice/template/device/"

type deviceFeatureNode struct {
	name, kind string
	children   []deviceFeatureNode
}

func deviceVPNNode(vpn DeviceVPNReference, scope string) (deviceFeatureNode, error) {
	if vpn.Name == "" {
		return deviceFeatureNode{}, errors.New("VPN template reference requires a name")
	}
	if scope != "vpn0" && (vpn.SigCredentialsTemplate != "" || vpn.SecureInternetGatewayTemplate != "" || len(vpn.GREInterfaceTemplates) != 0 || len(vpn.CellularInterfaceTemplates) != 0) {
		return deviceFeatureNode{}, errors.New("VPN template contains fields outside its NetAsCode scope")
	}
	if scope == "vpn512" && (vpn.OSPFTemplate != "" || vpn.BGPTemplate != "" || vpn.IGMPTemplate != "" || vpn.MulticastTemplate != "" || vpn.PIMTemplate != "" || len(vpn.IPsecInterfaceTemplates) != 0) {
		return deviceFeatureNode{}, errors.New("management VPN template contains unsupported children")
	}
	if scope != "service" && (vpn.IGMPTemplate != "" || vpn.MulticastTemplate != "" || vpn.PIMTemplate != "") {
		return deviceFeatureNode{}, errors.New("transport VPN template contains service-only children")
	}
	appendOne := func(children *[]deviceFeatureNode, name, kind string) {
		if name != "" {
			*children = append(*children, deviceFeatureNode{name: name, kind: kind})
		}
	}
	children := []deviceFeatureNode{}
	appendOne(&children, vpn.OSPFTemplate, "cisco_ospf")
	appendOne(&children, vpn.BGPTemplate, "cisco_bgp")
	appendInterfaces := func(items []DeviceNamedReference, kind string, allowDHCP bool) error {
		for _, item := range items {
			if item.Name == "" || (!allowDHCP && item.DHCPServerTemplate != "") {
				return errors.New("invalid VPN interface template reference")
			}
			node := deviceFeatureNode{name: item.Name, kind: kind}
			if item.DHCPServerTemplate != "" {
				node.children = append(node.children, deviceFeatureNode{name: item.DHCPServerTemplate, kind: "cisco_dhcp_server"})
			}
			children = append(children, node)
		}
		return nil
	}
	if err := appendInterfaces(vpn.EthernetInterfaceTemplates, "cisco_vpn_interface", scope == "service"); err != nil {
		return deviceFeatureNode{}, err
	}
	appendOne(&children, vpn.IGMPTemplate, "cedge_igmp")
	if err := appendInterfaces(vpn.IPsecInterfaceTemplates, "cisco_vpn_interface_ipsec", true); err != nil {
		return deviceFeatureNode{}, err
	}
	appendOne(&children, vpn.MulticastTemplate, "cedge_multicast")
	appendOne(&children, vpn.PIMTemplate, "cedge_pim")
	if err := appendInterfaces(vpn.SVIInterfaceTemplates, "vpn-interface-svi", scope == "service"); err != nil {
		return deviceFeatureNode{}, err
	}
	appendOne(&children, vpn.SecureInternetGatewayTemplate, "cisco_secure_internet_gateway")
	if err := appendInterfaces(vpn.GREInterfaceTemplates, "cisco_vpn_interface_gre", false); err != nil {
		return deviceFeatureNode{}, err
	}
	if err := appendInterfaces(vpn.CellularInterfaceTemplates, "vpn-cedge-interface-cellular", false); err != nil {
		return deviceFeatureNode{}, err
	}
	return deviceFeatureNode{name: vpn.Name, kind: "cisco_vpn", children: children}, nil
}

func compileDeviceTemplates(items []EdgeDeviceTemplate) ([]Resource, error) {
	out := []Resource{}
	seen := map[string]bool{}
	for _, item := range items {
		if item.Name == "" || len(item.Name) > 128 || item.Description == "" || item.DeviceModel == "" || seen[item.Name] {
			return nil, errors.New("invalid or duplicate edge device template")
		}
		seen[item.Name] = true
		if item.SystemTemplate == "" && (item.LoggingTemplate != "" || item.NTPTemplate != "") {
			return nil, errors.New("logging and NTP require a system template")
		}
		if item.VPN0Template == nil && item.VPN512Template == nil && len(item.VPNServiceTemplates) == 0 && len(item.SwitchportTemplates) == 0 && len(item.CellularControllerTemplates) == 0 && item.SystemTemplate == "" && item.AAATemplate == "" && item.BFDTemplate == "" && item.OMPTemplate == "" && item.SecurityTemplate == "" && item.BannerTemplate == "" && item.SNMPTemplate == "" && item.GlobalSettingsTemplate == "" && item.CLITemplate == "" && item.ThousandEyesTemplate == "" {
			return nil, errors.New("edge device template needs a feature template")
		}
		nodes := []deviceFeatureNode{}
		appendOne := func(name, kind string) {
			if name != "" {
				nodes = append(nodes, deviceFeatureNode{name: name, kind: kind})
			}
		}
		if item.SystemTemplate != "" {
			system := deviceFeatureNode{name: item.SystemTemplate, kind: "cisco_system"}
			if item.LoggingTemplate != "" {
				system.children = append(system.children, deviceFeatureNode{name: item.LoggingTemplate, kind: "cisco_logging"})
			}
			if item.NTPTemplate != "" {
				system.children = append(system.children, deviceFeatureNode{name: item.NTPTemplate, kind: "cisco_ntp"})
			}
			nodes = append(nodes, system)
		}
		for _, ref := range []struct{ name, kind string }{{item.AAATemplate, "cedge_aaa"}, {item.BFDTemplate, "cisco_bfd"}, {item.OMPTemplate, "cisco_omp"}, {item.SecurityTemplate, "cisco_security"}, {item.BannerTemplate, "cisco_banner"}, {item.SNMPTemplate, "cisco_snmp"}, {item.GlobalSettingsTemplate, "cedge_global"}, {item.CLITemplate, "cli-template"}} {
			appendOne(ref.name, ref.kind)
		}
		if item.VPN0Template != nil {
			appendOne(item.VPN0Template.SigCredentialsTemplate, "cisco_sig_credentials")
		}
		for _, ref := range item.SwitchportTemplates {
			if ref.Name == "" || ref.DHCPServerTemplate != "" {
				return nil, errors.New("invalid switchport template reference")
			}
			appendOne(ref.Name, "switchport")
		}
		appendOne(item.ThousandEyesTemplate, "cisco_thousandeyes")
		for _, scoped := range []struct {
			vpn   *DeviceVPNReference
			scope string
		}{{item.VPN0Template, "vpn0"}, {item.VPN512Template, "vpn512"}} {
			if scoped.vpn != nil {
				node, err := deviceVPNNode(*scoped.vpn, scoped.scope)
				if err != nil {
					return nil, err
				}
				nodes = append(nodes, node)
			}
		}
		for _, vpn := range item.VPNServiceTemplates {
			node, err := deviceVPNNode(vpn, "service")
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, node)
		}
		for _, controller := range item.CellularControllerTemplates {
			if controller.Name == "" {
				return nil, errors.New("invalid cellular controller template reference")
			}
			node := deviceFeatureNode{name: controller.Name, kind: "cellular-cedge-controller"}
			for _, profile := range controller.CellularProfileTemplates {
				if profile.Name == "" || profile.DHCPServerTemplate != "" {
					return nil, errors.New("invalid cellular profile template reference")
				}
				node.children = append(node.children, deviceFeatureNode{name: profile.Name, kind: "cellular-cedge-profile"})
			}
			nodes = append(nodes, node)
		}
		general := []any{}
		refs := []DeviceTemplateReference{}
		var encode func(deviceFeatureNode, []int) map[string]any
		encode = func(node deviceFeatureNode, path []int) map[string]any {
			refs = append(refs, DeviceTemplateReference{Name: node.name, Type: node.kind, Index: path[0], Path: append([]int(nil), path...)})
			children := make([]any, 0, len(node.children))
			for index, child := range node.children {
				children = append(children, encode(child, append(append([]int(nil), path...), index)))
			}
			return map[string]any{"templateId": "", "templateType": node.kind, "subTemplates": children}
		}
		for index, node := range nodes {
			general = append(general, encode(node, []int{index}))
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
		path := ref.Path
		if len(path) == 0 {
			path = []int{ref.Index}
		}
		if path[0] < 0 || path[0] >= len(general) {
			return ErrResponse
		}
		row, ok := general[path[0]].(map[string]any)
		if !ok {
			return ErrResponse
		}
		for _, index := range path[1:] {
			children, ok := row["subTemplates"].([]any)
			if !ok || index < 0 || index >= len(children) {
				return ErrResponse
			}
			row, ok = children[index].(map[string]any)
			if !ok {
				return ErrResponse
			}
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
	return equalDeviceTemplateRows(want, got)
}
func equalDeviceTemplateRows(want, got []any) (bool, error) {
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
				wantChildren, _ := w["subTemplates"].([]any)
				gotChildren, _ := g["subTemplates"].([]any)
				childrenEqual, err := equalDeviceTemplateRows(wantChildren, gotChildren)
				if err != nil {
					return false, err
				}
				if !childrenEqual {
					continue
				}
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
