// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type HierarchyCollectors struct {
	Cflowd          *HierarchyCflowd          `json:"cflowd,omitempty"`
	SecurityLogging *HierarchySecurityLogging `json:"security_logging,omitempty"`
}
type HierarchyCflowd struct {
	Settings HierarchyCflowdSettings `json:"settings,omitempty"`
	Servers  []HierarchyCflowdServer `json:"servers,omitempty"`
}
type HierarchyCflowdSettings struct {
	FlowActiveTimeout    *int   `json:"flow_active_timeout,omitempty"`
	FlowInactiveTimeout  *int   `json:"flow_inactive_timeout,omitempty"`
	FlowRefreshTime      *int   `json:"flow_refresh_time,omitempty"`
	FlowSamplingInterval *int   `json:"flow_sampling_interval,omitempty"`
	CollectTlocLoopback  *bool  `json:"collect_tloc_loopback,omitempty"`
	Protocol             string `json:"protocol,omitempty"`
	CollectTos           *bool  `json:"collect_tos,omitempty"`
	CollectDscpOutput    *bool  `json:"collect_dscp_output,omitempty"`
}
type HierarchyCflowdServer struct {
	VPNID            int    `json:"vpn_id"`
	Address          string `json:"address"`
	UDPPort          *int   `json:"udp_port,omitempty"`
	ExportSpread     *bool  `json:"export_spread,omitempty"`
	BFDMetricsExport *bool  `json:"bfd_metrics_export,omitempty"`
	ExportInterval   *int   `json:"export_interval,omitempty"`
}
type HierarchySecurityLogging struct {
	HighSpeedLoggingServers []HierarchyHighSpeedLoggingServer `json:"high_speed_logging_servers,omitempty"`
	ExternalSyslogServer    *HierarchyExternalSyslogServer    `json:"external_syslog_server,omitempty"`
}
type HierarchyHighSpeedLoggingServer struct {
	LANVPNName string `json:"lan_vpn_name"`
	ServerIP   string `json:"server_ip"`
	Port       *int   `json:"port,omitempty"`
}
type HierarchyExternalSyslogServer struct {
	LANVPNName string `json:"lan_vpn_name"`
	ServerIP   string `json:"server_ip"`
}

func globalOption(v any) map[string]any { return map[string]any{"optionType": "global", "value": v} }
func valueOr[T any](v *T, fallback T) T {
	if v != nil {
		return *v
	}
	return fallback
}

func compileHierarchyCollectors(c *HierarchyCollectors) ([]Resource, error) {
	if c == nil {
		return nil, nil
	}
	out := []Resource{}
	if c.Cflowd != nil {
		s := c.Cflowd.Settings
		active, inactive, refresh, sampling := valueOr(s.FlowActiveTimeout, 600), valueOr(s.FlowInactiveTimeout, 60), valueOr(s.FlowRefreshTime, 600), valueOr(s.FlowSamplingInterval, 1)
		protocol := s.Protocol
		if protocol == "" {
			protocol = "ipv4"
		}
		if active < 30 || active > 3600 || inactive < 1 || inactive > 3600 || refresh < 60 || refresh > 86400 || sampling < 1 || sampling > 65536 || (protocol != "ipv4" && protocol != "ipv6" && protocol != "both") {
			return nil, errors.New("invalid hierarchy cflowd settings")
		}
		data := map[string]any{"flowActiveTimeout": globalOption(active), "flowInactiveTimeout": globalOption(inactive), "flowRefreshTime": globalOption(refresh), "flowSamplingInterval": globalOption(sampling), "collectTlocLoopback": globalOption(valueOr(s.CollectTlocLoopback, false)), "protocol": globalOption(protocol), "customizedIpv4RecordFields": map[string]any{"collectTos": globalOption(valueOr(s.CollectTos, false)), "collectDscpOutput": globalOption(valueOr(s.CollectDscpOutput, false))}}
		if len(c.Cflowd.Servers) > 0 {
			servers := []any{}
			for _, v := range c.Cflowd.Servers {
				port := valueOr(v.UDPPort, 4739)
				bfd := valueOr(v.BFDMetricsExport, false)
				if v.VPNID < 0 || v.VPNID > 65530 || strings.TrimSpace(v.Address) == "" || port < 1024 || port > 65535 || (v.ExportInterval != nil && !bfd) {
					return nil, errors.New("invalid hierarchy cflowd collector")
				}
				server := map[string]any{"vpnId": globalOption(v.VPNID), "address": globalOption(v.Address), "udpPort": globalOption(port), "exportSpread": globalOption(valueOr(v.ExportSpread, false)), "bfdMetricsExport": globalOption(bfd)}
				if bfd {
					interval := valueOr(v.ExportInterval, 600)
					if interval < 1 || interval > 86400 {
						return nil, errors.New("invalid cflowd export interval")
					}
					server["exportInterval"] = globalOption(interval)
				}
				servers = append(servers, server)
			}
			data["collectors"] = servers
		}
		out = append(out, Resource{Key: "network_hierarchy/collectors/cflowd", Name: "Global cflowd", Section: "network_hierarchy", Type: "cflowd", Kind: "hierarchy-setting", Body: map[string]any{"data": data}})
	}
	if c.SecurityLogging != nil {
		d := map[string]any{}
		if len(c.SecurityLogging.HighSpeedLoggingServers) > 4 {
			return nil, errors.New("at most four high-speed logging servers")
		}
		if len(c.SecurityLogging.HighSpeedLoggingServers) > 0 {
			servers := []any{}
			for i, v := range c.SecurityLogging.HighSpeedLoggingServers {
				port := valueOr(v.Port, 2055)
				if v.LANVPNName == "" || v.ServerIP == "" || port < 1 || port > 65535 {
					return nil, errors.New("invalid high-speed logging server")
				}
				servers = append(servers, map[string]any{"name": fmt.Sprintf("server%d", i+1), "vrf": globalOption(v.LANVPNName), "serverIp": globalOption(v.ServerIP), "port": globalOption(port)})
			}
			d["highSpeedLogging"] = servers
		}
		if v := c.SecurityLogging.ExternalSyslogServer; v != nil {
			if v.LANVPNName == "" || v.ServerIP == "" {
				return nil, errors.New("invalid external syslog server")
			}
			d["utdSyslog"] = []any{map[string]any{"vpn": globalOption(v.LANVPNName), "serverIp": globalOption(v.ServerIP)}}
		}
		out = append(out, Resource{Key: "network_hierarchy/collectors/security_logging", Name: "Global security logging", Section: "network_hierarchy", Type: "security-logging", Kind: "hierarchy-setting", Body: map[string]any{"data": d}})
	}
	return out, nil
}

func (c *Client) hierarchySettingPath(ctx context.Context, r Resource) (string, error) {
	nodes, e := c.hierarchy(ctx)
	if e != nil {
		return "", e
	}
	id := ""
	for _, n := range nodes {
		data, _ := n["data"].(map[string]any)
		if data["label"] == "GLOBAL" {
			v, ok := n["id"].(string)
			if !ok || !validID.MatchString(v) || id != "" {
				return "", ErrResponse
			}
			id = v
		}
	}
	if id == "" {
		return "", ErrResponse
	}
	return hierarchyPath + "/" + url.PathEscape(id) + "/network-settings/" + r.Type, nil
}
func (c *Client) observeHierarchySetting(ctx context.Context, r Resource) (*Observation, error) {
	path, e := c.hierarchySettingPath(ctx, r)
	if e != nil {
		return nil, e
	}
	var listed map[string]any
	if e = c.Request(ctx, "GET", path, nil, &listed); e != nil {
		return nil, e
	}
	var entries []any
	if data, ok := listed["data"].([]any); ok {
		entries = data
	} else if _, ok := listed["id"].(string); ok {
		entries = []any{listed}
	}
	if len(entries) == 0 {
		return nil, nil
	}
	if len(entries) != 1 {
		return nil, ErrResponse
	}
	entry, ok := entries[0].(map[string]any)
	if !ok {
		return nil, ErrResponse
	}
	id, ok := entry["id"].(string)
	if !ok || !validID.MatchString(id) {
		return nil, ErrResponse
	}
	var body map[string]any
	if e = c.Request(ctx, "GET", path+"/"+url.PathEscape(id), nil, &body); e != nil {
		return nil, e
	}
	if _, ok := body["payload"].(map[string]any); !ok {
		return nil, ErrResponse
	}
	return &Observation{ID: id, Body: body}, nil
}
func (c *Client) createHierarchySetting(ctx context.Context, r Resource) (string, error) {
	path, e := c.hierarchySettingPath(ctx, r)
	if e != nil {
		return "", e
	}
	var result map[string]any
	if e = c.Request(ctx, "POST", path, r.Body, &result); e != nil {
		return "", e
	}
	id, ok := result["id"].(string)
	if !ok || !validID.MatchString(id) {
		return "", ErrResponse
	}
	return id, nil
}
func (c *Client) updateHierarchySetting(ctx context.Context, r Resource, o *Observation) error {
	path, e := c.hierarchySettingPath(ctx, r)
	if e != nil {
		return e
	}
	return c.Request(ctx, "PUT", path+"/"+url.PathEscape(o.ID), r.Body, nil)
}
func (c *Client) deleteHierarchySetting(ctx context.Context, r Resource, id string) error {
	if !validID.MatchString(id) {
		return ErrResponse
	}
	path, e := c.hierarchySettingPath(ctx, r)
	if e != nil {
		return e
	}
	return c.Request(ctx, "DELETE", path+"/"+url.PathEscape(id), nil, nil)
}
func equalHierarchySetting(r Resource, observed map[string]any) (bool, error) {
	payload, ok := observed["payload"].(map[string]any)
	if !ok {
		return false, ErrResponse
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		return false, ErrResponse
	}
	want := r.Body["data"].(map[string]any)
	for key, value := range want {
		a, _ := json.Marshal(value)
		b, _ := json.Marshal(data[key])
		if string(a) != string(b) {
			return false, nil
		}
	}
	return true, nil
}
