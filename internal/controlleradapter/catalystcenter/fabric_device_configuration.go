// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strconv"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Separate from the membership projection used by multicast: a role-only GET
// must never be treated as sufficient evidence for a border configuration write.
type fabricDeviceConfiguration struct {
	ID              string                `json:"id,omitempty"`
	NetworkDeviceID string                `json:"networkDeviceId"`
	FabricID        string                `json:"fabricId"`
	Roles           []string              `json:"deviceRoles"`
	Border          *fabricBorderSettings `json:"borderDeviceSettings,omitempty"`
}
type fabricBorderSettings struct {
	Types  []string            `json:"borderTypes"`
	Layer3 *fabricBorderLayer3 `json:"layer3Settings,omitempty"`
}
type fabricBorderLayer3 struct {
	ASN            string `json:"localAutonomousSystemNumber"`
	DefaultExit    *bool  `json:"isDefaultExit"`
	ImportExternal *bool  `json:"importExternalRoutes"`
	Priority       *int   `json:"borderPriority,omitempty"`
	Prepend        *int   `json:"prependAutonomousSystemCount,omitempty"`
}

// Consume the resolved border-settings projection only. Handoffs/transits are
// separate resources and must be explicitly projected by the future compiler.
func parseFabricBorderSettings(model map[string]any) (*fabricBorderSettings, error) {
	for key := range model {
		switch key {
		case "border_types", "local_autonomous_system_number", "default_exit", "import_external_routes", "border_priority", "prepend_autonomous_system_count":
		default:
			return nil, errInvalidResponse
		}
	}
	rows, ok := model["border_types"].([]any)
	if !ok || len(rows) == 0 || len(rows) > 2 {
		return nil, errInvalidResponse
	}
	out := &fabricBorderSettings{}
	for _, v := range rows {
		s, ok := v.(string)
		if !ok || (s != "LAYER_2" && s != "LAYER_3") || slices.Contains(out.Types, s) {
			return nil, errInvalidResponse
		}
		out.Types = append(out.Types, s)
	}
	sort.Strings(out.Types)
	if !slices.Contains(out.Types, "LAYER_3") {
		for key, value := range model {
			if key != "border_types" && value != nil {
				return nil, errInvalidResponse
			}
		}
		return out, nil
	}
	asn, err := transitASN(model["local_autonomous_system_number"])
	if err != nil {
		return nil, err
	}
	exit, ok := model["default_exit"].(bool)
	if !ok {
		return nil, errInvalidResponse
	}
	external, ok := model["import_external_routes"].(bool)
	if !ok {
		return nil, errInvalidResponse
	}
	out.Layer3 = &fabricBorderLayer3{ASN: asn, DefaultExit: &exit, ImportExternal: &external}
	for key, target := range map[string]**int{"border_priority": &out.Layer3.Priority, "prepend_autonomous_system_count": &out.Layer3.Prepend} {
		value, present := model[key]
		if !present || value == nil {
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, errInvalidResponse
		}
		var n int
		if json.Unmarshal(raw, &n) != nil {
			return nil, errInvalidResponse
		}
		// The pinned module omits native defaults 10/0 rather than sending them.
		if key == "border_priority" && n == 10 || key == "prepend_autonomous_system_count" && n == 0 {
			continue
		}
		max := 10
		if key == "border_priority" {
			max = 9
		}
		if n < 1 || n > max {
			return nil, errInvalidResponse
		}
		*target = &n
	}
	return out, nil
}

func canonicalFabricDevice(d fabricDeviceConfiguration, create bool) (fabricDeviceConfiguration, error) {
	if !validAPIID(d.NetworkDeviceID) || !validAPIID(d.FabricID) || create && d.ID != "" || !create && !validAPIID(d.ID) || len(d.Roles) == 0 {
		return d, errInvalidResponse
	}
	d.Roles = append([]string{}, d.Roles...)
	sort.Strings(d.Roles)
	for i, role := range d.Roles {
		if i > 0 && d.Roles[i-1] == role {
			return d, errInvalidResponse
		}
		switch role {
		case "CONTROL_PLANE_NODE", "EDGE_NODE", "BORDER_NODE", "WIRELESS_CONTROLLER_NODE":
		case "EXTENDED_NODE":
			if create {
				return d, errInvalidResponse
			}
		default:
			return d, errInvalidResponse
		}
	}
	if !slices.Contains(d.Roles, "BORDER_NODE") {
		if d.Border != nil {
			return d, errInvalidResponse
		}
		return d, nil
	}
	if d.Border == nil || len(d.Border.Types) == 0 || len(d.Border.Types) > 2 {
		return d, errInvalidResponse
	}
	border := *d.Border
	d.Border = &border
	border.Types = append([]string{}, border.Types...)
	sort.Strings(border.Types)
	for i, kind := range border.Types {
		if kind != "LAYER_2" && kind != "LAYER_3" || i > 0 && border.Types[i-1] == kind {
			return d, errInvalidResponse
		}
	}
	if !slices.Contains(border.Types, "LAYER_3") {
		if border.Layer3 != nil {
			return d, errInvalidResponse
		}
		return d, nil
	}
	if border.Layer3 == nil {
		return d, errInvalidResponse
	}
	l3 := *border.Layer3
	border.Layer3 = &l3
	n, err := strconv.ParseUint(l3.ASN, 10, 32)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != l3.ASN || l3.DefaultExit == nil || l3.ImportExternal == nil {
		return d, errInvalidResponse
	}
	if l3.Priority != nil && (*l3.Priority < 1 || *l3.Priority > 10 || create && *l3.Priority == 10) {
		return d, errInvalidResponse
	}
	if l3.Prepend != nil && (*l3.Prepend < 0 || *l3.Prepend > 10 || create && *l3.Prepend == 0) {
		return d, errInvalidResponse
	}
	// Values are immutable snapshots even when the caller later edits its model.
	b := *l3.DefaultExit
	l3.DefaultExit = &b
	b2 := *l3.ImportExternal
	l3.ImportExternal = &b2
	if l3.Priority != nil {
		v := *l3.Priority
		l3.Priority = &v
	}
	if l3.Prepend != nil {
		v := *l3.Prepend
		l3.Prepend = &v
	}
	return d, nil
}

func (c *client) fabricDeviceConfigurations(ctx context.Context, fabricID string) (map[string]fabricDeviceConfiguration, error) {
	if !validAPIID(fabricID) {
		return nil, errInvalidResponse
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		return c.get(ctx, path, q, http.StatusOK)
	}
	rows, err := listInventoryQueryWithGet(ctx, get, fabricDevicesPath, url.Values{"fabricId": {fabricID}}, func(d fabricDeviceConfiguration) string { return d.ID })
	if err != nil {
		return nil, err
	}
	out := map[string]fabricDeviceConfiguration{}
	for _, row := range rows {
		row, err = canonicalFabricDevice(row, false)
		if err != nil {
			return nil, err
		}
		if row.FabricID != fabricID {
			return nil, errInvalidResponse
		}
		if _, exists := out[row.NetworkDeviceID]; exists {
			return nil, errInvalidResponse
		}
		out[row.NetworkDeviceID] = row
	}
	return out, nil
}

func fabricDeviceConfigurationMatches(want, got fabricDeviceConfiguration) bool {
	got.ID = ""
	if want.Border != nil && want.Border.Layer3 != nil && got.Border != nil && got.Border.Layer3 != nil {
		border := *got.Border
		l3 := *border.Layer3
		border.Layer3 = &l3
		got.Border = &border
		if want.Border.Layer3.Priority == nil && l3.Priority != nil && *l3.Priority == 10 {
			l3.Priority = nil
		}
		if want.Border.Layer3.Prepend == nil && l3.Prepend != nil && *l3.Prepend == 0 {
			l3.Prepend = nil
		}
	}
	return reflect.DeepEqual(want, got)
}
func fabricDeviceCreateResource(want fabricDeviceConfiguration) string {
	raw, _ := json.Marshal(want)
	return fmt.Sprintf("fabric-device-create:%x", sha256.Sum256(raw))
}

// Private native create primitive, not registered with the manifest compiler.
// Resolved fabric/device IDs are not physical authority: runtime integration
// still needs identity/role prerequisites, group admission and shared journal.
func (c *client) planFabricDeviceCreate(ctx context.Context, want fabricDeviceConfiguration) (*configurationWrite, error) {
	want, err := canonicalFabricDevice(want, true)
	if err != nil {
		return nil, err
	}
	before, err := c.fabricDeviceConfigurations(ctx, want.FabricID)
	if err != nil {
		return nil, err
	}
	if got, exists := before[want.NetworkDeviceID]; exists {
		if fabricDeviceConfigurationMatches(want, got) {
			return nil, nil
		}
		return nil, errConfigurationTransition
	}
	body, err := json.Marshal([]fabricDeviceConfiguration{want})
	if err != nil {
		return nil, err
	}
	return &configurationWrite{resource: fabricDeviceCreateResource(want), action: "Create", identity: configurationResourceIdentity{target: want.NetworkDeviceID, parent: want.FabricID}, submit: func(ctx context.Context) (Task, error) {
		latest, err := c.fabricDeviceConfigurations(ctx, want.FabricID)
		if err != nil {
			return Task{}, err
		}
		if !reflect.DeepEqual(before, latest) {
			return Task{}, errConfigurationStale
		}
		raw, err := c.mutate(ctx, http.MethodPost, fabricDevicesPath, body)
		if err != nil {
			return Task{}, err
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			return Task{}, errInvalidResponse
		}
		return Task{ID: receipt.Response.ID}, nil
	}}, nil
}
func (c *client) verifyFabricDeviceCreate(ctx context.Context, want fabricDeviceConfiguration, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	want, err := canonicalFabricDevice(want, true)
	if err != nil {
		return false, err
	}
	if op == nil || op.Action != "Create" || op.TargetID != want.NetworkDeviceID || op.ParentID != want.FabricID || op.Resource != fabricDeviceCreateResource(want) || op.TargetSerial != "" || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	rows, err := c.fabricDeviceConfigurations(ctx, want.FabricID)
	if err != nil {
		return false, err
	}
	got, exists := rows[want.NetworkDeviceID]
	return exists && fabricDeviceConfigurationMatches(want, got), nil
}
