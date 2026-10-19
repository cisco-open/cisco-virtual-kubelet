// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestLiveWirelessRFExecution(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_RF_EXECUTION") != "isolated-definition" {
		t.Skip("explicit isolated definition test not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := liveClient(t)
	baseline, err := c.wirelessRFProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := "CVK_" + time.Now().UTC().Format("02150405")
	if _, exists := baseline[name]; exists {
		t.Fatal("fixture exists")
	}
	desired, err := parseWirelessRFProfiles([]byte(strings.Replace(wirelessRFModel, "CVK_LAB", name, 1)))
	if err != nil {
		t.Fatal(err)
	}
	e := &wirelessRFExecutor{client: c, desired: desired}
	w, err := e.next(ctx)
	if err != nil || w == nil {
		t.Fatal("definition plan", err)
	}
	submitted := time.Now()
	task, err := w.submit(ctx)
	if err != nil {
		t.Fatal("submission uncertain; inspect fixture", name, err)
	}
	t.Log("create", name, task.ID)
	if err = waitSiteCredentialTask(ctx, c, task, submitted); err != nil {
		t.Fatal("task uncertain; retain fixture", name, err)
	}
	op := &ciskov1.NetworkControllerConfigurationOperation{Action: w.action, Resource: w.resource, TaskID: task.ID}
	verified, err := e.verify(ctx, op)
	if err != nil || !verified {
		t.Fatal("readback incomplete; retain fixture", name, err)
	}
	if w, err = e.next(ctx); err != nil || w != nil {
		t.Fatal("definition no-op failed", err)
	}

	setWirelessAPField(e.desired[0].fields, "radioTypeAProperties.maxRadioClients", int64(200))
	w, err = e.next(ctx)
	if err != nil || w == nil || w.action != "Update" || !validAPIID(w.identity.target) {
		t.Fatal("update plan", err)
	}
	submitted = time.Now()
	task, err = w.submit(ctx)
	if err != nil {
		t.Fatal("update uncertain; retain fixture", name, err)
	}
	t.Log("update", w.identity.target, task.ID)
	if err = waitSiteCredentialTask(ctx, c, task, submitted); err != nil {
		t.Fatal("update task uncertain", name, err)
	}
	op = &ciskov1.NetworkControllerConfigurationOperation{Action: w.action, Resource: w.resource, TargetID: w.identity.target, TaskID: task.ID}
	verified, err = e.verify(ctx, op)
	if err != nil || !verified {
		t.Fatal("update readback", name, err)
	}
	if w, err = e.next(ctx); err != nil || w != nil {
		t.Fatal("update no-op", err)
	}
	current, err := c.wirelessRFProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created := current[name]
	delete(current, name)
	if !reflect.DeepEqual(baseline, current) {
		t.Fatal("baseline or association changed; retain fixture", name)
	}
	submitted = time.Now()
	raw, err := c.mutate(ctx, http.MethodDelete, wirelessRFPath+"/"+url.PathEscape(created.id), nil)
	if err != nil {
		t.Fatal("deletion uncertain", name, created.id, err)
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		t.Fatal("delete receipt missing; inspect fixture", name)
	}
	t.Log("delete", created.id, receipt.Response.ID)
	if err = waitSiteCredentialTask(ctx, c, receipt.Response, submitted); err != nil {
		t.Fatal("delete incomplete", err)
	}
	after, err := c.wirelessRFProfiles(ctx)
	if err != nil || !reflect.DeepEqual(baseline, after) {
		t.Fatal("baseline restoration failed", err)
	}
}
