// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The actual appliance refuses NCND86002 and NCND86004. Reject these before
// any earlier definition in the same declaration can be submitted.
func TestWirelessPowerNativeConstraintsBeforePlanning(t *testing.T) {
	valid, err := parseWirelessPower([]byte(wirelessPowerModel))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"empty-rules", "empty-description", "blank-description"} {
		t.Run(kind, func(t *testing.T) {
			bad := valid[0]
			bad.Name = "LATER"
			switch kind {
			case "empty-rules":
				bad.Rules = []wirelessPowerRule{}
			case "empty-description":
				s := ""
				bad.Description = &s
			case "blank-description":
				s := " \t\n "
				bad.Description = &s
			}
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				t.Error("native request for invalid desired model")
				w.WriteHeader(500)
			})
			w, err := (&wirelessPowerExecutor{client: c, desired: []wirelessPowerProfile{valid[0], bad}}).next(context.Background())
			if err == nil || w != nil || calls != 0 {
				t.Fatal("invalid later intent did not fence first plan", err, calls)
			}
		})
	}
}

func TestWirelessPowerNativeConstraintsAtModelBoundary(t *testing.T) {
	var model map[string]any
	if json.Unmarshal([]byte(wirelessPowerModel), &model) != nil {
		t.Fatal("fixture")
	}
	power := model["catalyst_center"].(map[string]any)["wireless"].(map[string]any)["power_profiles"].([]any)[0].(map[string]any)
	power["rules"] = []any{}
	emptyRules, _ := json.Marshal(model)
	if _, err := parseWirelessPower(emptyRules); err == nil {
		t.Fatal("empty rules accepted")
	}
	for _, description := range []string{"", "  ", "\t\n"} {
		value, _ := json.Marshal(description)
		raw := strings.Replace(wirelessPowerModel, `"name":"CVK_LAB"`, `"name":"CVK_LAB","description":`+string(value), 1)
		if _, err := parseWirelessPower([]byte(raw)); err == nil {
			t.Fatal("blank explicit description accepted")
		}
		// A prospective interface must not be released for an invalid later power
		// definition; section compilation must reject before any native call.
		raw = strings.Replace(raw, `"wireless":{`, `"wireless":{"interfaces":[{"name":"EARLIER","vlan_id":123}],`, 1)
		if _, err := (&client{}).compileWirelessConfiguration([]byte(raw)); err == nil {
			t.Fatal("invalid power admitted in combined model")
		}
	}
}

func TestWirelessPowerOmittedUnconfiguredDescription(t *testing.T) {
	for _, observed := range []string{"", " \t "} {
		t.Run(observed, func(t *testing.T) {
			desired, err := parseWirelessPower([]byte(wirelessPowerModel))
			if err != nil {
				t.Fatal(err)
			}
			actual := desired[0]
			actual.ID = "power-id"
			actual.Description = &observed
			actual.Rules = append([]wirelessPowerRule{}, actual.Rules...)
			actual.Rules[0].ParameterValue = "1000MBPS"
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					w.Write([]byte(`{"Token":"test"}`))
					return
				}
				if r.Method == http.MethodGet {
					json.NewEncoder(w).Encode(map[string]any{"response": []wirelessPowerProfile{actual}})
					return
				}
				if r.Method != http.MethodPut || r.URL.Path != wirelessPowerPath+"/power-id" {
					t.Error("unexpected request")
				}
				var payload map[string]any
				if json.NewDecoder(r.Body).Decode(&payload) != nil {
					t.Error("invalid payload")
				}
				if _, exists := payload["description"]; exists {
					t.Error("observation-only blank description resubmitted")
				}
				writes++
				w.Write([]byte(`{"response":{"taskId":"task"}}`))
			})
			plan, err := (&wirelessPowerExecutor{client: c, desired: desired}).next(context.Background())
			if err != nil || plan == nil {
				t.Fatal("valid rule update held by unowned metadata", err)
			}
			if _, err := plan.submit(context.Background()); err != nil || writes != 1 {
				t.Fatal("omitted-description update failed", err)
			}
		})
	}
}
