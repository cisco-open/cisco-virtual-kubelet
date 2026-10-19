// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestLiveWirelessPowerRules(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_POWER_RULE_EXECUTION") != "isolated-definition" {
		t.Skip("isolated power-rule qualification not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := liveClient(t)
	baseline, err := c.wirelessPowerProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	aps, err := c.wirelessAPMetadataInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := wirelessQualificationName(t, "CVK_R_")
	id := ""
	t.Log("isolated power", name)
	if _, exists := baseline[name]; exists {
		t.Fatal("fixture name collision")
	}
	check := func() {
		t.Helper()
		current, err := c.wirelessPowerProfiles(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ap, err := c.wirelessAPMetadataInventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(aps, ap) {
			t.Fatal("AP metadata changed; retain", name, id)
		}
		for _, p := range ap {
			if p.fields["apPowerProfileName"] == name {
				t.Fatal("power profile acquired AP reference; retain", name, id)
			}
			calendar, _ := p.fields["calendarPowerProfiles"].([]any)
			for _, v := range calendar {
				if v.(map[string]any)["powerProfileName"] == name {
					t.Fatal("power profile acquired calendar reference; retain", name, id)
				}
			}
		}
		if id != "" {
			if current[name].ID != id {
				t.Fatal("power UUID changed; retain", name, id)
			}
			delete(current, name)
		} else if _, exists := current[name]; exists {
			t.Fatal("unowned fixture appeared", name)
		}
		if !reflect.DeepEqual(baseline, current) {
			t.Fatal("unrelated power baseline changed; retain", name, id)
		}
	}
	rule := func(kind, port, param, value string) map[string]any {
		return map[string]any{"interface_type": kind, "interface_id": port, "parameter_type": param, "parameter_value": value}
	}
	ethernet := rule("ETHERNET", "GIGABITETHERNET0", "SPEED", "1000MBPS")
	radio := rule("RADIO", "5GHZ", "SPATIALSTREAM", "FOUR_BY_FOUR")
	usb := rule("USB", "USB0", "STATE", "DISABLE")
	stages := []struct {
		name, description string
		rules             []any
	}{
		{"ETHERNET", "CVK isolated rules", []any{ethernet}},
		{"RADIO", "CVK isolated rules", []any{radio}},
		{"USB", "CVK isolated rules", []any{usb}},
		{"COMBINED", "CVK isolated rules", []any{ethernet, radio, usb}},
		{"REORDERED", "CVK isolated rules", []any{usb, radio, ethernet}},
		{"DESCRIPTION", "CVK description updated", []any{usb, radio, ethernet}},
		{"OMITTED_DESCRIPTION", "", []any{ethernet}},
	}
	compile := func(description string, rules []any, omitDescription bool) *wirelessConfiguration {
		t.Helper()
		power := map[string]any{"name": name, "rules": rules}
		if !omitDescription {
			power["description"] = description
		}
		raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"wireless": map[string]any{"power_profiles": []any{power}}}})
		if err != nil {
			t.Fatal(err)
		}
		e, err := c.compileWirelessConfiguration(raw)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	for i, stage := range stages {
		check()
		e := compile(stage.description, stage.rules, stage.name == "OMITTED_DESCRIPTION")
		w, err := e.next(ctx)
		action := "Update"
		if i == 0 {
			action = "Create"
		}
		if err != nil || w == nil || w.action != action || w.identity.target != id {
			t.Fatal("stage plan", stage.name, err)
		}
		submitted := time.Now()
		task, err := w.submit(ctx)
		if err != nil {
			t.Fatal("stage submission uncertain; retain", stage.name, name, id, err)
		}
		t.Log("task", stage.name, w.action, task.ID)
		if err = waitSiteCredentialTask(ctx, c, task, submitted); err != nil {
			t.Fatal("task uncertain; retain", stage.name, name, id, err)
		}
		op := &ciskov1.NetworkControllerConfigurationOperation{Action: w.action, Resource: w.resource, TargetID: w.identity.target, TaskID: task.ID}
		if ok, err := e.verify(ctx, op); err != nil || !ok {
			t.Fatal("stage readback incomplete; retain", stage.name, name, id, err)
		}
		current, err := c.wirelessPowerProfiles(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			id = current[name].ID
			t.Log("owned power", name, id)
		}
		if stage.name == "OMITTED_DESCRIPTION" && (current[name].Description == nil || *current[name].Description != "CVK description updated") {
			t.Fatal("omitted description not preserved")
		}
		if !validAPIID(id) || current[name].ID != id {
			t.Fatal("UUID readback", name, id)
		}
		if next, err := compile(stage.description, stage.rules, stage.name == "OMITTED_DESCRIPTION").next(ctx); err != nil || next != nil {
			t.Fatal("fresh compiler not converged", stage.name, err)
		}
		check()
		t.Log("qualified", stage.name, "same UUID, exact rules/description, no-op and baseline")
	}
	check()
	submitted := time.Now()
	raw, err := c.mutate(ctx, http.MethodDelete, wirelessPowerPath+"/"+url.PathEscape(id), nil)
	if err != nil {
		t.Fatal("cleanup uncertain; retain", name, id, err)
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		t.Fatal("cleanup receipt missing", name, id)
	}
	t.Log("cleanup", id, receipt.Response.ID)
	if err = waitSiteCredentialTask(ctx, c, receipt.Response, submitted); err != nil {
		t.Fatal("cleanup task uncertain", err)
	}
	id = ""
	check()
	t.Log("power and AP baselines restored; no assignment or device action submitted")
}
