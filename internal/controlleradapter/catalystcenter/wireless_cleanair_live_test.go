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
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestLiveWirelessCleanAir(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_CLEANAIR_EXECUTION") != "isolated-definition" {
		t.Skip("explicit isolated definition test not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := liveClient(t)
	baseline, err := c.wirelessFeatureInstances(ctx, wirelessCleanAirKind)
	if err != nil {
		t.Fatal(err)
	}
	name := "CVK_" + time.Now().UTC().Format("02150405")
	if _, exists := baseline[name]; exists {
		t.Fatal("fixture exists")
	}
	e := &wirelessCleanAirExecutor{client: c, desired: []wirelessCleanAir{{Name: name, Attributes: wirelessCleanAirAttributes{Band: "2_4GHZ"}}}}
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
	if _, replayErr := w.submit(ctx); replayErr != errConfigurationTransition {
		t.Fatal("prepared create reuse was not rejected; inspect fixture", name, replayErr)
	}
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

	e.desired[0].Attributes.Enable = true
	w, err = e.next(ctx)
	if err != nil || w == nil || w.action != "Update" || !validAPIID(w.identity.target) {
		t.Fatal("update plan", err)
	}
	// Simulate another actor editing an omitted field on this generated-only
	// definition after planning. The retained PUT must refuse that stale snapshot.
	description := "CVK guarded-dispatch qualification"
	otherDesired := append([]wirelessCleanAir(nil), e.desired...)
	otherDesired[0].Attributes.Enable = false
	otherDesired[0].Attributes.Description = &description
	other := &wirelessCleanAirExecutor{client: c, desired: otherDesired}
	concurrent, err := other.next(ctx)
	if err != nil || concurrent == nil || concurrent.action != "Update" || concurrent.identity.target != w.identity.target {
		t.Fatal("isolated concurrent-edit plan", name, err)
	}
	changedAt := time.Now()
	changedTask, err := concurrent.submit(ctx)
	if err != nil {
		t.Fatal("concurrent edit uncertain; retain fixture", name, err)
	}
	t.Log("generated-only concurrent edit", concurrent.identity.target, changedTask.ID)
	if err = waitSiteCredentialTask(ctx, c, changedTask, changedAt); err != nil {
		t.Fatal("concurrent edit task uncertain", name, err)
	}
	if _, staleErr := w.submit(ctx); staleErr != errConfigurationStale {
		t.Fatal("stale native snapshot not refused", name, staleErr)
	}
	if _, replayErr := w.submit(ctx); replayErr != errConfigurationTransition {
		t.Fatal("stale permit reusable", name, replayErr)
	}
	w, err = e.next(ctx)
	if err != nil || w == nil || w.action != "Update" || w.identity.target != concurrent.identity.target {
		t.Fatal("fresh update plan", name, err)
	}
	submitted = time.Now()
	task, err = w.submit(ctx)
	if err != nil {
		t.Fatal("update uncertain; retain fixture", name, err)
	}
	t.Log("update", w.identity.target, task.ID)
	if _, replayErr := w.submit(ctx); replayErr != errConfigurationTransition {
		t.Fatal("prepared update reuse was not rejected; inspect fixture", name, replayErr)
	}
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
	current, err := c.wirelessFeatureInstances(ctx, wirelessCleanAirKind)
	if err != nil {
		t.Fatal(err)
	}
	created := current[name]
	detail, err := c.wirelessCleanAirRead(ctx, created)
	if err != nil || detail.Attributes.Description == nil || *detail.Attributes.Description != description {
		t.Fatal("concurrent omitted description lost", name, err)
	}
	t.Log("stale dispatch refused, permit consumed, omitted native description preserved")
	if os.Getenv("CATC_WIRELESS_CLEANAIR_BANDS") == "isolated-definition" {
		for _, band := range []string{"5GHZ", "6GHZ", "2_4GHZ", "6GHZ", "5GHZ", "2_4GHZ"} {
			e.desired[0].Attributes.Band = band
			write, err := e.next(ctx)
			if err != nil || write == nil || write.action != "Update" || write.identity.target != created.ID {
				t.Fatal("band update plan", name, band, err)
			}
			started := time.Now()
			receipt, err := write.submit(ctx)
			if err != nil {
				t.Fatal("band update submission uncertain; retain fixture", name, created.ID, band, err)
			}
			t.Log("band update", created.ID, band, receipt.ID)
			if _, replayErr := write.submit(ctx); replayErr != errConfigurationTransition {
				t.Fatal("band update permit reusable", name, band, replayErr)
			}
			if err = waitSiteCredentialTask(ctx, c, receipt, started); err != nil {
				t.Fatal("band task incomplete; retain fixture", name, created.ID, band, err)
			}
			op := &ciskov1.NetworkControllerConfigurationOperation{Action: write.action, Resource: write.resource, TargetID: write.identity.target, TaskID: receipt.ID}
			if ok, err := e.verify(ctx, op); err != nil || !ok {
				t.Fatal("band task readback", name, band, err)
			}
			after, err := c.wirelessCleanAirRead(ctx, created)
			if err != nil || after.Attributes.Description == nil || *after.Attributes.Description != description {
				t.Fatal("band update omitted description lost", name, band, err)
			}
			if next, err := e.next(ctx); err != nil || next != nil {
				t.Fatal("band update no-op", name, band, err)
			}
		}
	}

	delete(current, name)
	if !reflect.DeepEqual(baseline, current) {
		t.Fatal("baseline or association changed; retain fixture", name)
	}
	submitted = time.Now()
	raw, err := c.mutate(ctx, http.MethodDelete, wirelessCleanAirPath+"/"+url.PathEscape(created.ID), nil)
	if err != nil {
		t.Fatal("deletion uncertain", name, created.ID, err)
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		t.Fatal("delete receipt missing; inspect fixture", name)
	}
	t.Log("delete", created.ID, receipt.Response.ID)
	if err = waitSiteCredentialTask(ctx, c, receipt.Response, submitted); err != nil {
		t.Fatal("delete incomplete", err)
	}
	after, err := c.wirelessFeatureInstances(ctx, wirelessCleanAirKind)
	if err != nil || !reflect.DeepEqual(baseline, after) {
		t.Fatal("baseline restoration failed", err)
	}
}
