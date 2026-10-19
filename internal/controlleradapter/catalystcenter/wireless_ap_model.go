// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const wirelessAPPath = "/dna/intent/api/v1/wirelessSettings/apProfiles"

var errWirelessAPModel = errors.New("invalid AP profile model or metadata")

// Only public, explicitly mapped metadata is retained. Password values belong
// exclusively to the existing versioned Secret projection flow.
type wirelessAPIntent struct {
	name        string
	metadata    map[string]any
	credentials []configurationCredentialIntent
}
type wirelessAPMetadata struct {
	id, name string
	fields   map[string]any
}
type wirelessAPField struct {
	path, kind string
	fallback   any
	min, max   int64
	enums      string
}

var wirelessAPFields = map[string]wirelessAPField{
	"name":                               {path: "apProfileName", kind: "string", min: 1, max: 32},
	"description":                        {path: "description", kind: "string", max: 241},
	"remote_worker_enabled":              {path: "remoteWorkerEnabled", kind: "bool", fallback: false},
	"auth_type":                          {path: "managementSetting.authType", kind: "string", fallback: "NO-AUTH", enums: "NO-AUTH|EAP-TLS|EAP-PEAP|EAP-FAST"},
	"dot1x_username":                     {path: "managementSetting.dot1xUsername", kind: "string", min: 1, max: 32},
	"ssh_enabled":                        {path: "managementSetting.sshEnabled", kind: "bool", fallback: false},
	"telnet_enabled":                     {path: "managementSetting.telnetEnabled", kind: "bool", fallback: false},
	"management_user_name":               {path: "managementSetting.managementUserName", kind: "string", min: 1, max: 32},
	"cdp_state":                          {path: "managementSetting.cdpState", kind: "bool", fallback: false},
	"awips_enabled":                      {path: "awipsEnabled", kind: "bool", fallback: false},
	"awips_forensic_enabled":             {path: "awipsForensicEnabled", kind: "bool", fallback: false},
	"rogue_detection":                    {path: "rogueDetectionSetting.rogueDetection", kind: "bool", fallback: false},
	"rogue_detection_min_rssi":           {path: "rogueDetectionSetting.rogueDetectionMinRssi", kind: "int", fallback: int64(-90), min: -128, max: -70},
	"rogue_detection_transient_interval": {path: "rogueDetectionSetting.rogueDetectionTransientInterval", kind: "int", fallback: int64(0), max: 1800},
	"rogue_detection_report_interval":    {path: "rogueDetectionSetting.rogueDetectionReportInterval", kind: "int", fallback: int64(10), min: 10, max: 300},
	"pmf_denial_enabled":                 {path: "pmfDenialEnabled", kind: "bool", fallback: false},
	"mesh_enabled":                       {path: "meshEnabled", kind: "bool", fallback: false},
	"bridge_group_name":                  {path: "meshSetting.bridgeGroupName", kind: "string", max: 4096},
	"backhaul_client_access":             {path: "meshSetting.backhaulClientAccess", kind: "bool"},
	"range":                              {path: "meshSetting.range", kind: "int", min: 150, max: 132000},
	"ghz5_backhaul_data_rates":           {path: "meshSetting.ghz5BackhaulDataRates", kind: "string", enums: "auto|802.11abg|802.11ac|802.11ax|802.11n"},
	"ghz24_backhaul_data_rates":          {path: "meshSetting.ghz24BackhaulDataRates", kind: "string", enums: "auto|802.11abg|802.11ax|802.11n"},
	"rap_downlink_backhaul":              {path: "meshSetting.rapDownlinkBackhaul", kind: "string", enums: "5 GHz|2.4 GHz"},
	"power_profile":                      {path: "apPowerProfileName", kind: "string", min: 1, max: 32},
	"country_code":                       {path: "countryCode", kind: "string", enums: "AF|AE|AL|AR|AT|AO|AU|BD|BA|BB|BE|BG|BH|BM|BN|BO|BR|BT|BY|CA|CD|CH|CI|CL|CM|CN|CO|CR|CU|CY|CZ|DE|DK|DO|DZ|EC|EE|EG|EL|ES|ET|FI|FJ|FR|GB|GH|GI|GE|GR|GT|HK|HN|HR|HU|ID|IE|IL|IN|IQ|IS|IT|J2|J4|JM|JO|KE|KH|KN|KW|KZ|LA|LB|LI|LK|LT|LU|LV|LY|MA|MC|MD|ME|MK|MN|MM|MO|MT|MX|MY|NG|NI|NL|NO|NP|NZ|OM|PA|PE|PH|PK|PL|PR|PT|PY|QA|RO|RS|RU|SA|SD|SE|SG|SI|SK|SM|TH|TI|TN|TR|TW|TZ|UA|US|UY|VA|VE|VN|XK|YE|ZA|ZW|MU|ZM|BI|NA|BW|GA|UG|UZ"},
	"time_zone":                          {path: "timeZone", kind: "string", fallback: "Not Configured", enums: "Not Configured|Controller|Delta from Controller"},
	"time_zone_offset_hour":              {path: "timeZoneOffsetHour", kind: "int", fallback: int64(0), min: -12, max: 14},
	"time_zone_offset_minutes":           {path: "timeZoneOffsetMinutes", kind: "int", fallback: int64(0), max: 59},
	"client_limit":                       {path: "clientLimit", kind: "int", fallback: int64(0), max: 1200},
}
var wirelessAPPasswords = map[string]string{
	"dot1x_password":             "managementSetting.dot1xPassword",
	"management_password":        "managementSetting.managementPassword",
	"management_enable_password": "managementSetting.managementEnablePassword",
}

func wirelessAPInteger(v any) (int64, error) {
	raw, err := json.Marshal(v)
	var n int64
	if err != nil || json.Unmarshal(raw, &n) != nil || v == nil {
		return 0, errWirelessAPModel
	}
	return n, nil
}
func wirelessAPValue(f wirelessAPField, v any) (any, error) {
	switch f.kind {
	case "bool":
		b, ok := v.(bool)
		if ok {
			return b, nil
		}
	case "int":
		n, err := wirelessAPInteger(v)
		if err == nil && n >= f.min && n <= f.max {
			return n, nil
		}
	case "string":
		s, ok := v.(string)
		if !ok || strings.ContainsAny(s, "\x00\r\n") || int64(len(s)) < f.min || f.max > 0 && int64(len(s)) > f.max {
			break
		}
		if f.enums == "" {
			return s, nil
		}
		for _, allowed := range strings.Split(f.enums, "|") {
			if s == allowed {
				return s, nil
			}
		}
	}
	return nil, errWirelessAPModel
}
func setWirelessAPField(out map[string]any, path string, value any) {
	parts := strings.SplitN(path, ".", 2)
	if len(parts) == 1 {
		out[path] = value
		return
	}
	child, ok := out[parts[0]].(map[string]any)
	if !ok {
		child = map[string]any{}
		out[parts[0]] = child
	}
	setWirelessAPField(child, parts[1], value)
}
func getWirelessAPField(in map[string]any, path string) (any, bool) {
	parts := strings.SplitN(path, ".", 2)
	v, ok := in[parts[0]]
	if !ok {
		return nil, false
	}
	if len(parts) == 1 {
		return v, true
	}
	child, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	return getWirelessAPField(child, parts[1])
}
func parseWirelessAPProfiles(raw []byte) ([]wirelessAPIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errWirelessAPModel
	}
	section, ok := root["wireless"].(map[string]any)
	if !ok || len(section) != 1 {
		return nil, errWirelessAPModel
	}
	rows, ok := section["ap_profiles"].([]any)
	if !ok || len(rows) == 0 || len(rows) > 1000 {
		return nil, errWirelessAPModel
	}
	out := []wirelessAPIntent{}
	seen := map[string]bool{}
	for index, v := range rows {
		row, ok := v.(map[string]any)
		if !ok {
			return nil, errWirelessAPModel
		}
		intent := wirelessAPIntent{metadata: map[string]any{}}
		for key, v := range row {
			if v == nil {
				return nil, errWirelessAPModel
			}
			if _, ok := wirelessAPFields[key]; ok {
				continue
			}
			if key == "calendar_power_profiles" {
				continue
			}
			if _, secret := wirelessAPPasswords[key]; secret {
				return nil, errConfigurationSecret
			}
			if _, version := wirelessAPPasswords[strings.TrimSuffix(key, "_version")]; version && strings.HasSuffix(key, "_version") {
				continue
			}
			return nil, errWirelessAPModel
		}
		for key, f := range wirelessAPFields {
			v, ok := row[key]
			if !ok {
				v = f.fallback
			}
			if v == nil {
				continue
			}
			normalized, err := wirelessAPValue(f, v)
			if err != nil {
				return nil, err
			}
			if key == "rogue_detection_transient_interval" && normalized.(int64) != 0 && normalized.(int64) < 120 {
				return nil, errWirelessAPModel
			}
			setWirelessAPField(intent.metadata, f.path, normalized)
		}
		intent.name, _ = intent.metadata["apProfileName"].(string)
		if !validProfileName(intent.name) || seen[intent.name] {
			return nil, errWirelessAPModel
		}
		seen[intent.name] = true
		if v, exists := row["calendar_power_profiles"]; exists {
			cal, err := parseWirelessAPCalendar(v)
			if err != nil {
				return nil, err
			}
			intent.metadata["calendarPowerProfiles"] = cal
		}
		for key, field := range wirelessAPPasswords {
			if v, exists := row[key+"_version"]; exists {
				version, err := wirelessAPInteger(v)
				if err != nil || version < 1 {
					return nil, errConfigurationSecret
				}
				intent.credentials = append(intent.credentials, configurationCredentialIntent{path: "/ap_profiles/" + strconv.Itoa(index) + "/" + key, field: field, version: version})
			}
		}
		sort.Slice(intent.credentials, func(i, j int) bool { return intent.credentials[i].path < intent.credentials[j].path })
		out = append(out, intent)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// Calendar times use the provider's canonical 12-hour API representation.
// Date updates must still be verified; never substitute old dates for intent.
func parseWirelessAPCalendar(value any) ([]any, error) {
	rows, ok := value.([]any)
	if !ok || len(rows) > 1000 {
		return nil, errWirelessAPModel
	}
	out := []any{}
	for _, v := range rows {
		row, ok := v.(map[string]any)
		if !ok {
			return nil, errWirelessAPModel
		}
		for k, v := range row {
			if v == nil || !strings.Contains("|power_profile|scheduler_type|scheduler_start_time|scheduler_end_time|scheduler_day|scheduler_date|", "|"+k+"|") {
				return nil, errWirelessAPModel
			}
		}
		entry := map[string]any{}
		duration := map[string]any{}
		entry["duration"] = duration
		if v, exists := row["power_profile"]; exists {
			p, ok := v.(string)
			if !ok || !validProfileName(p) || len(p) > 32 {
				return nil, errWirelessAPModel
			}
			entry["powerProfileName"] = p
		}
		kind, ok := row["scheduler_type"].(string)
		if !ok || (kind != "DAILY" && kind != "WEEKLY" && kind != "MONTHLY") {
			return nil, errWirelessAPModel
		}
		entry["schedulerType"] = kind
		for key, dest := range map[string]string{"scheduler_start_time": "schedulerStartTime", "scheduler_end_time": "schedulerEndTime"} {
			s, ok := row[key].(string)
			if !ok {
				return nil, errWirelessAPModel
			}
			t, err := time.Parse("15:04", s)
			if err != nil {
				t, err = time.Parse("03:04 PM", strings.ToUpper(s))
			}
			if err != nil {
				return nil, errWirelessAPModel
			}
			duration[dest] = t.Format("03:04 PM")
		}
		for key, dest := range map[string]string{"scheduler_day": "schedulerDay", "scheduler_date": "schedulerDate"} {
			raw, exists := row[key]
			if !exists {
				continue
			}
			values, ok := raw.([]any)
			if !ok {
				return nil, errWirelessAPModel
			}
			normalized := []string{}
			seen := map[string]bool{}
			for _, v := range values {
				s, ok := v.(string)
				if !ok {
					return nil, errWirelessAPModel
				}
				if key == "scheduler_day" {
					s = strings.ToLower(s)
					if !strings.Contains("|monday|tuesday|wednesday|thursday|friday|saturday|sunday|", "|"+s+"|") {
						return nil, errWirelessAPModel
					}
				} else {
					n, err := strconv.Atoi(s)
					if err != nil || n < 1 || n > 31 {
						return nil, errWirelessAPModel
					}
					s = strconv.Itoa(n)
				}
				if seen[s] {
					return nil, errWirelessAPModel
				}
				seen[s] = true
				normalized = append(normalized, s)
			}
			sort.Strings(normalized)
			duration[dest] = normalized
		}
		out = append(out, entry)
	}
	return out, nil
}

// Read only the public allowlist, even if an appliance returns passwords.
func decodeWirelessAPMetadata(raw []byte) (wirelessAPMetadata, error) {
	var row map[string]any
	var out wirelessAPMetadata
	if json.Unmarshal(raw, &row) != nil {
		return out, errInvalidResponse
	}
	out.id, _ = row["id"].(string)
	out.name, _ = row["apProfileName"].(string)
	if !validAPIID(out.id) || !validProfileName(out.name) {
		return out, errInvalidResponse
	}
	out.fields = map[string]any{}
	for _, f := range wirelessAPFields {
		v, ok := getWirelessAPField(row, f.path)
		if !ok || v == nil {
			continue
		}
		// Observed appliance sentinels are not valid user configuration values.
		if f.path == "countryCode" && v == "unconfigured" {
			setWirelessAPField(out.fields, f.path, v)
			continue
		}
		if f.path == "timeZone" {
			if text, ok := v.(string); ok {
				for _, label := range strings.Split(f.enums, "|") {
					if strings.EqualFold(text, label) {
						v = label
						break
					}
				}
			}
		}
		if (f.path == "managementSetting.dot1xUsername" || f.path == "managementSetting.managementUserName" || f.path == "apPowerProfileName") && v == "" {
			setWirelessAPField(out.fields, f.path, "")
			continue
		}
		normalized, err := wirelessAPValue(f, v)
		if err != nil {
			return wirelessAPMetadata{}, errInvalidResponse
		}
		setWirelessAPField(out.fields, f.path, normalized)
	}
	if rawCalendar, exists := row["calendarPowerProfiles"]; exists && rawCalendar != nil {
		rows, ok := rawCalendar.([]any)
		if !ok {
			return wirelessAPMetadata{}, errInvalidResponse
		}
		model := []any{}
		for _, raw := range rows {
			calendar, ok := raw.(map[string]any)
			if !ok {
				return wirelessAPMetadata{}, errInvalidResponse
			}
			duration, ok := calendar["duration"].(map[string]any)
			if !ok {
				return wirelessAPMetadata{}, errInvalidResponse
			}
			entry := map[string]any{}
			for key, dest := range map[string]string{"powerProfileName": "power_profile", "schedulerType": "scheduler_type"} {
				if v, ok := calendar[key]; ok && v != nil {
					entry[dest] = v
				}
			}
			for key, dest := range map[string]string{"schedulerStartTime": "scheduler_start_time", "schedulerEndTime": "scheduler_end_time", "schedulerDay": "scheduler_day", "schedulerDate": "scheduler_date"} {
				if v, ok := duration[key]; ok && v != nil {
					entry[dest] = v
				}
			}
			model = append(model, entry)
		}
		calendar, err := parseWirelessAPCalendar(model)
		if err != nil {
			return wirelessAPMetadata{}, errInvalidResponse
		}
		out.fields["calendarPowerProfiles"] = calendar
	}
	return out, nil
}
func (c *client) wirelessAPMetadataInventory(ctx context.Context) (map[string]wirelessAPMetadata, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	rows, err := listInventoryQueryWithGet(ctx, get, wirelessAPPath, nil, func(raw json.RawMessage) string {
		var row struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &row) != nil {
			return ""
		}
		return row.ID
	})
	if err != nil {
		return nil, err
	}
	out := map[string]wirelessAPMetadata{}
	seen := map[string]bool{}
	for _, raw := range rows {
		m, err := decodeWirelessAPMetadata(raw)
		if err != nil || seen[m.id] {
			return nil, errInvalidResponse
		}
		if _, exists := out[m.name]; exists {
			return nil, errInvalidResponse
		}
		seen[m.id] = true
		out[m.name] = m
	}
	return out, nil
}
