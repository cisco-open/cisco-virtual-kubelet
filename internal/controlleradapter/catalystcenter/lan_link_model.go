// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"net/netip"
	"net/url"
	"sort"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

type lanLinkIntent struct {
	name, action, primaryIP, primaryInterface, secondaryIP, secondaryInterface, pool string
}

// Parse only the resolved pinned link stripe. Session discovery is a separate
// effect and must not be silently dropped by a link-only execution plan.
func parseLANLinks(raw []byte) ([]lanLinkIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errInvalidResponse
	}
	section, ok := root["lan_automation"].(map[string]any)
	if !ok || len(section) != 1 {
		return nil, errInvalidResponse
	}
	rows, ok := section["links"].([]any)
	if !ok || len(rows) == 0 || len(rows) > 1000 {
		return nil, errInvalidResponse
	}
	out := make([]lanLinkIntent, 0, len(rows))
	names, endpoints := map[string]bool{}, map[string]bool{}
	for _, row := range rows {
		fields, ok := row.(map[string]any)
		if !ok {
			return nil, errInvalidResponse
		}
		var want lanLinkIntent
		mapping := map[string]*string{
			"name": &want.name, "action": &want.action,
			"primary_device_management_ip_address":   &want.primaryIP,
			"primary_device_interface_name":          &want.primaryInterface,
			"secondary_device_management_ip_address": &want.secondaryIP,
			"secondary_device_interface_name":        &want.secondaryInterface,
			"ip_pool_name":                           &want.pool,
		}
		for key, value := range fields {
			dest, known := mapping[key]
			text, isString := value.(string)
			if !known || !isString || text == "" {
				return nil, errInvalidResponse
			}
			*dest = text
		}
		if err := want.validate(); err != nil || names[want.name+"\x00"+want.action] {
			return nil, errInvalidResponse
		}
		names[want.name+"\x00"+want.action] = true
		// A port cannot participate in two requested changes, including a
		// reverse-spelled duplicate or an add/delete conflict in one model.
		for _, endpoint := range []string{want.primaryIP + "\x00" + strings.ToLower(want.primaryInterface), want.secondaryIP + "\x00" + strings.ToLower(want.secondaryInterface)} {
			if endpoints[endpoint] {
				return nil, errInvalidResponse
			}
			endpoints[endpoint] = true
		}
		out = append(out, want)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].name != out[j].name {
			return out[i].name < out[j].name
		}
		return out[i].action < out[j].action
	})
	return out, nil
}

func (want lanLinkIntent) validate() error {
	if !validProfileName(want.name) || (want.action != "ADD_LINK" && want.action != "DELETE_LINK") || (want.pool != "" && !validPoolName(want.pool)) || (want.action == "ADD_LINK" && want.pool == "") {
		return errInvalidResponse
	}
	// This bounded implementation addresses managed IPv4 devices. Do not accept DNS,
	// scoped IPv6, mapped aliases or unspecified/multicast endpoint identities.
	for _, text := range []string{want.primaryIP, want.secondaryIP} {
		ip, err := netip.ParseAddr(text)
		if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() || ip.String() != text {
			return errInvalidResponse
		}
	}
	if want.primaryIP == want.secondaryIP {
		return errInvalidResponse
	}
	for _, name := range []string{want.primaryInterface, want.secondaryInterface} {
		if !validLANInterfaceName(name) {
			return errInvalidResponse
		}
	}
	return nil
}

// The pinned provider maps ADD_LINK/DELETE_LINK to a feature query on PUT;
// secondary_* model fields become destination* native fields. This builder
// grants no mutation authority or completion evidence.
func lanLinkPayload(want lanLinkIntent) (string, []byte, error) {
	if err := want.validate(); err != nil {
		return "", nil, err
	}
	feature := "LINK_ADD"
	if want.action == "DELETE_LINK" {
		feature = "LINK_DELETE"
	}
	link := map[string]string{
		"sourceDeviceManagementIPAddress":      want.primaryIP,
		"sourceDeviceInterfaceName":            want.primaryInterface,
		"destinationDeviceManagementIPAddress": want.secondaryIP,
		"destinationDeviceInterfaceName":       want.secondaryInterface,
	}
	if want.pool != "" {
		link["ipPoolName"] = want.pool
	}
	body, err := json.Marshal(map[string]any{"linkUpdate": link})
	return "/dna/intent/api/v1/lan-automation/updateDevice?" + url.Values{"feature": {feature}}.Encode(), body, err
}

// Preserve the simple typed entry-point name when unambiguous; composed sources
// use the pinned module's name/action key for repeated names across actions.
func lanLinkSourceKeys(wants []lanLinkIntent) map[lanLinkIntent]string {
	counts := map[string]int{}
	for _, want := range wants {
		counts[want.name]++
	}
	keys := map[lanLinkIntent]string{}
	for _, want := range wants {
		key := want.name
		if counts[key] > 1 {
			key += "#_#" + want.action
		}
		keys[want] = key
	}
	return keys
}
