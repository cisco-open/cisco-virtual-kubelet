// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestLiveDiscoveryOutcomes(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_OUTCOMES") != "existing-lab" {
		t.Skip("explicit read-only lab discovery qualification not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := liveClient(t)
	jobs, err := c.discoveries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, ok := jobs["Switches"]
	if !ok || job.Condition != "Complete" || job.Status != "Inactive" {
		t.Fatal("lab job absent or incomplete")
	}
	rows, err := c.discoveryDevices(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]bool{"198.51.100.100": false, "198.51.100.101": false, "198.51.100.103": false}
	for _, row := range rows {
		if _, ok := expected[row.IP]; !ok {
			t.Fatal("unexpected lab discovery target")
		}
		expected[row.IP] = true
		if row.CLI != "SUCCESS" || row.SNMP != "SUCCESS" || row.NETCONF != "SUCCESS" || row.Collection != "Managed" || row.Reachability != "Reachable" {
			t.Fatal("lab discovery does not prove successful authentication and collection")
		}
	}
	for _, seen := range expected {
		if !seen {
			t.Fatal("missing lab discovery target")
		}
	}
	summary := discoveryOutcomeSummary{}
	summary.add(rows)
	t.Log(summary.message())
}

func TestLiveCombinedDiscoveryNoOp(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_OUTCOMES") != "existing-lab" {
		t.Skip("explicit read-only lab qualification not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := liveClient(t)
	jobs, err := c.discoveries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, ok := jobs["Switches"]
	if !ok || job.Condition != "Complete" || job.Status != "Inactive" {
		t.Fatal("lab discovery unavailable")
	}
	credentials, err := c.globalCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, id := range job.CredentialIDs {
		found := ""
		for _, group := range credentials {
			for _, credential := range group {
				if credential.ID == id {
					if found != "" {
						t.Fatal("ambiguous credential ID")
					}
					found = credential.Name
				}
			}
		}
		if found == "" {
			t.Fatal("missing credential name")
		}
		names = append(names, found)
	}
	raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{
		"templates": map[string]any{"tags": []any{map[string]any{"name": "CVK-NAC-Journal-0146d7a983a4"}}},
		"inventory": map[string]any{"discovery": []any{map[string]any{"name": job.Name, "type": "MULTI RANGE", "ip_address_list": job.Addresses, "protocol_order": job.Protocol, "global_credential_list": names}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, sections := range [][]string{{"templates", "inventory"}, {"inventory", "templates"}} {
		plan, err := c.compileConfigurationApply(raw, sections, nil)
		if err != nil {
			t.Fatal(err)
		}
		write, err := plan.executor.next(ctx)
		if err != nil || write != nil {
			t.Fatal("existing lab composition did not converge without mutation", err)
		}
		if plan.executor.count() != 2 {
			t.Fatal("combined resource count incorrect")
		}
	}
	t.Log("existing tag and discovery converged without mutation in both section orders")
}

func TestLiveDiscoveryExecution(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_EXECUTION") != "existing-lab-101" {
		t.Skip("explicit single-device discovery qualification not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	c := liveClient(t)
	before, err := c.discoveries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	baseline, ok := before["Switches"]
	if !ok || baseline.Condition != "Complete" || baseline.Status != "Inactive" {
		t.Fatal("existing lab discovery is missing or active")
	}
	devices, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	device, err := resolveConfigurationDevice(configurationDeviceReference{IP: "198.51.100.101"}, devices)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := c.globalCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := []any{}
	for _, id := range baseline.CredentialIDs {
		name := ""
		for _, group := range credentials {
			for _, credential := range group {
				if credential.ID == id {
					if name != "" {
						t.Fatal("duplicate credential ID")
					}
					name = credential.Name
				}
			}
		}
		if name == "" {
			t.Fatal("lab discovery credential reference unresolved")
		}
		names = append(names, name)
	}
	name := "CVK-Discovery-" + time.Now().UTC().Format("20060102-150405")
	desired, err := parseDiscoveryModels([]any{map[string]any{"name": name, "type": "SINGLE", "ip_address_list": "198.51.100.101", "protocol_order": "SSH", "global_credential_list": names}})
	if err != nil {
		t.Fatal(err)
	}
	e := &discoveryExecutor{client: c, desired: desired}
	write, err := e.next(ctx)
	if err != nil || write == nil {
		t.Fatal("discovery plan failed", err)
	}
	task, err := write.submit(ctx)
	if err != nil {
		t.Fatal("discovery submission uncertain", name, err)
	}
	t.Log("discovery submitted", name, task.ID)
	op := &ciskov1.NetworkControllerConfigurationOperation{Action: "Create", Resource: write.resource, TaskID: task.ID}
	submitted := time.Now()
	if err = waitSiteCredentialTask(ctx, c, task, submitted); err != nil {
		t.Fatal("discovery task uncertain; retain job", name, err)
	}
	verified := false
	for i := 0; i < 180; i++ {
		verified, err = e.verify(ctx, op)
		if err != nil {
			t.Fatal("discovery verification failed; retain job", name, err)
		}
		if verified {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	if !verified {
		t.Fatal("discovery remains incomplete; retain job", name)
	}
	actual, err := c.discoveries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job := actual[name]
	if next, err := e.next(ctx); err != nil || next != nil {
		t.Fatal("completed discovery not idempotent", err)
	}
	devices, err = c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterDevice, err := resolveConfigurationDevice(configurationDeviceReference{IP: "198.51.100.101"}, devices)
	if err != nil || afterDevice.ID != device.ID || afterDevice.Serial != device.Serial {
		t.Fatal("existing device identity changed", err)
	}
	t.Log("discovery task binding, completed metadata and no-op verified", job.ID)
	raw, err := c.mutate(ctx, http.MethodDelete, discoveryPath+"/"+job.ID, nil)
	if err != nil {
		t.Fatal("discovery cleanup uncertain", job.ID, err)
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		t.Fatal("discovery deletion receipt missing", job.ID)
	}
	if err = waitSiteCredentialTask(ctx, c, receipt.Response, time.Now()); err != nil {
		t.Fatal("discovery cleanup task pending", job.ID, err)
	}
	final, err := c.discoveries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(final) != len(before) {
		t.Fatal("discovery baseline count changed")
	}
	for name, job := range before {
		if final[name].ID != job.ID {
			t.Fatal("discovery baseline identity changed")
		}
	}
	t.Log("temporary discovery deleted; baseline retained", receipt.Response.ID)
}
