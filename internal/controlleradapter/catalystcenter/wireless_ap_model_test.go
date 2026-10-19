// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const wirelessAPModel = `{"catalyst_center":{"wireless":{"ap_profiles":[{"name":"AP-LAB"}]}}}`

func TestWirelessAPDefaultsAndCredentialBindings(t *testing.T) {
	raw := strings.Replace(wirelessAPModel, `"name":"AP-LAB"`, `"name":"AP-LAB","management_password_version":2,"management_enable_password_version":3,"dot1x_password_version":1`, 1)
	profiles, err := parseWirelessAPProfiles([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	p := profiles[0]
	if p.name != "AP-LAB" || len(p.credentials) != 3 {
		t.Fatal("missing credential bindings")
	}
	for key, want := range map[string]any{"managementSetting.authType": "NO-AUTH", "managementSetting.sshEnabled": false, "rogueDetectionSetting.rogueDetectionMinRssi": int64(-90), "rogueDetectionSetting.rogueDetectionReportInterval": int64(10), "timeZone": "Not Configured", "clientLimit": int64(0)} {
		got, ok := getWirelessAPField(p.metadata, key)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Fatal("incorrect default", key, got)
		}
	}
	for _, leaf := range p.credentials {
		if !strings.HasPrefix(leaf.path, "/ap_profiles/0/") || !strings.HasPrefix(leaf.field, "managementSetting.") {
			t.Fatal("incorrect secret destination")
		}
	}
	for _, field := range wirelessAPPasswords {
		if _, ok := getWirelessAPField(p.metadata, field); ok {
			t.Fatal("credential entered public payload")
		}
	}
}
func TestWirelessAPInputRejectsInvalidFieldsAndInlinePasswords(t *testing.T) {
	for _, field := range []string{`"ssh_enabled":"false"`, `"client_limit":1201`, `"client_limit":1.5`, `"auth_type":"bad"`, `"dot1x_password":"sensitive"`, `"management_password":"sensitive"`, `"management_password_version":0`, `"management_password_version":null`, `"unknown":true`, `"rogue_detection_transient_interval":1`, `"time_zone_offset_hour":-13`, `"country_code":"XX"`, `"description":null`, `"name":""`} {
		raw := strings.Replace(wirelessAPModel, `"name":"AP-LAB"`, `"name":"AP-LAB",`+field, 1)
		if _, err := parseWirelessAPProfiles([]byte(raw)); err == nil {
			t.Fatal("invalid field accepted", field)
		} else if strings.Contains(err.Error(), "sensitive") {
			t.Fatal("credential leaked in error")
		}
	}
}
func TestWirelessAPCalendarMapping(t *testing.T) {
	var value any
	json.Unmarshal([]byte(`[{"power_profile":"POWER","scheduler_type":"MONTHLY","scheduler_start_time":"00:15","scheduler_end_time":"23:59","scheduler_date":["31","01"]},{"scheduler_type":"WEEKLY","scheduler_start_time":"12:00 PM","scheduler_end_time":"01:00 PM","scheduler_day":["MONDAY","Sunday"]}]`), &value)
	cal, err := parseWirelessAPCalendar(value)
	if err != nil {
		t.Fatal(err)
	}
	a := cal[0].(map[string]any)["duration"].(map[string]any)
	if a["schedulerStartTime"] != "12:15 AM" || a["schedulerEndTime"] != "11:59 PM" || !reflect.DeepEqual(a["schedulerDate"], []string{"1", "31"}) {
		t.Fatal("calendar mapping", a)
	}
	for _, bad := range []string{`[{"scheduler_type":"DAILY","scheduler_start_time":"24:00","scheduler_end_time":"01:00"}]`, `[{"scheduler_type":"MONTHLY","scheduler_start_time":"12:00","scheduler_end_time":"13:00","scheduler_date":["32"]}]`, `[{"scheduler_type":"WEEKLY","scheduler_start_time":"12:00","scheduler_end_time":"13:00","scheduler_day":["Monday","monday"]}]`} {
		json.Unmarshal([]byte(bad), &value)
		if _, err := parseWirelessAPCalendar(value); err == nil {
			t.Fatal("invalid calendar accepted")
		}
	}
}
func TestWirelessAPMetadataNeverRetainsReturnedPasswords(t *testing.T) {
	raw := []byte(`{"id":"ap-id","apProfileName":"AP-LAB","managementSetting":{"authType":"NO-AUTH","sshEnabled":false,"dot1xPassword":"sensitive-one","managementPassword":"sensitive-two","managementEnablePassword":"sensitive-three"},"unexpectedPassword":"sensitive-four"}`)
	metadata, err := decodeWirelessAPMetadata(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprintf("%+v", metadata), "sensitive") {
		t.Fatal("response password retained")
	}
	if _, exists := getWirelessAPField(metadata.fields, "managementSetting.sshEnabled"); !exists {
		t.Fatal("explicit false dropped")
	}
	if _, exists := getWirelessAPField(metadata.fields, "meshEnabled"); exists {
		t.Fatal("missing observation defaulted")
	}
}

func TestWirelessAPCalendarReadbackAndStableSecretPaths(t *testing.T) {
	raw := `{"catalyst_center":{"wireless":{"ap_profiles":[{"name":"Z","management_password_version":5,"calendar_power_profiles":[{"power_profile":"POWER","scheduler_type":"MONTHLY","scheduler_start_time":"22:00","scheduler_end_time":"23:00","scheduler_date":["2","12"]}]},{"name":"A","management_password_version":7}]}}}`
	profiles, err := parseWirelessAPProfiles([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if profiles[0].name != "A" || profiles[0].credentials[0].path != "/ap_profiles/1/management_password" || profiles[1].credentials[0].path != "/ap_profiles/0/management_password" {
		t.Fatal("sorting changed secret source index")
	}
	payload := profiles[1].metadata
	payload["id"] = "profile-id"
	body, _ := json.Marshal(payload)
	actual, err := decodeWirelessAPMetadata(body)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual.fields["calendarPowerProfiles"], payload["calendarPowerProfiles"]) {
		t.Fatal("calendar dropped or changed during readback")
	}
}

func TestWirelessAPObservedSentinelsAreNotManifestValues(t *testing.T) {
	raw := []byte(`{"id":"default-id","apProfileName":"default-ap-profile","countryCode":"unconfigured","timeZone":"NOT CONFIGURED","managementSetting":{"dot1xUsername":"","managementUserName":""},"calendarPowerProfiles":[],"apPowerProfileName":""}`)
	got, err := decodeWirelessAPMetadata(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.fields["countryCode"] != "unconfigured" || got.fields["timeZone"] != "Not Configured" || got.fields["apPowerProfileName"] != "" {
		t.Fatal("observation normalization")
	}
	for _, field := range []string{`"country_code":"unconfigured"`, `"dot1x_username":""`, `"management_user_name":""`, `"power_profile":""`} {
		if _, err := parseWirelessAPProfiles([]byte(strings.Replace(wirelessAPModel, `"name":"AP-LAB"`, `"name":"AP-LAB",`+field, 1))); err == nil {
			t.Fatal("observation exception widened manifest contract")
		}
	}
}
