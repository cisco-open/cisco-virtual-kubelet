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

func TestLiveWirelessAnchor(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_ANCHOR_EXECUTION") != "isolated-definition" {
		t.Skip("explicit isolated definition test not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := liveClient(t)
	baseline, err := c.wirelessAnchorGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := "CVK_" + time.Now().UTC().Format("02150405")
	if _, exists := baseline[name]; exists {
		t.Fatal("fixture exists")
	}
	desired, err := parseWirelessAnchors([]byte(strings.Replace(wirelessAnchorModel, "CVK_LAB", name, 1)))
	if err != nil {
		t.Fatal(err)
	}
	e := &wirelessAnchorExecutor{client: c, desired: desired}
	w, err := e.next(ctx)
	if err != nil || w == nil || w.action != "Create" || w.identity.target != "" {
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

	createdInventory, err := c.wirelessAnchorGroups(ctx)
	if err != nil || !validAPIID(createdInventory[name].ID) {
		t.Fatal("created identity", name, err)
	}
	originalID := createdInventory[name].ID
	stages := []string{"single-priority"}
	if os.Getenv("CATC_WIRELESS_ANCHOR_MULTI_PEER") == "isolated-definition" {
		stages = append(stages, "add-peer", "swap-priorities", "remove-peer", "stale-plan")
	}
	for _, stage := range stages {
		switch stage {
		case "single-priority":
			e.desired[0].Peers[0].Priority = "SECONDARY"
		case "add-peer":
			peer := e.desired[0].Peers[0]
			peer.DeviceName = "external-wlc-two"
			peer.IP = "192.0.2.201"
			peer.Priority = "PRIMARY"
			mac := "02:00:00:00:00:02"
			peer.MAC = &mac
			e.desired[0].Peers = append(e.desired[0].Peers, peer)
		case "swap-priorities":
			e.desired[0].Peers[0].Priority = "PRIMARY"
			e.desired[0].Peers[1].Priority = "SECONDARY"
		case "remove-peer":
			e.desired[0].Peers = e.desired[0].Peers[1:]
		case "stale-plan":
			e.desired[0].Peers[0].Priority = "PRIMARY"
		}
		w, err = e.next(ctx)
		if err != nil || w == nil || w.action != "Update" || w.identity.target != originalID {
			t.Fatal(stage, "update plan", name, err)
		}
		if stage == "stale-plan" {
			// Both writes concern only the generated fixture. Establish a newer state
			// through the same executor before attempting the retained stale closure.
			raw, _ := json.Marshal(e.desired)
			var newer []wirelessAnchorGroup
			json.Unmarshal(raw, &newer)
			newer[0].Peers[0].Priority = "TERTIARY"
			alternate := &wirelessAnchorExecutor{client: c, desired: newer}
			fresh, err := alternate.next(ctx)
			if err != nil || fresh == nil || fresh.identity.target != originalID {
				t.Fatal("alternate plan", err)
			}
			submitted = time.Now()
			task, err = fresh.submit(ctx)
			if err != nil {
				t.Fatal("alternate uncertain; retain", name, originalID, err)
			}
			t.Log(stage, "alternate", originalID, task.ID)
			if err = waitSiteCredentialTask(ctx, c, task, submitted); err != nil {
				t.Fatal("alternate task", err)
			}
			ok, err := alternate.verify(ctx, &ciskov1.NetworkControllerConfigurationOperation{Action: "Update", Resource: fresh.resource, TargetID: originalID, TaskID: task.ID})
			if err != nil || !ok {
				t.Fatal("alternate readback", err)
			}
			if _, err = w.submit(ctx); err == nil {
				t.Fatal("stale plan accepted")
			}
			if _, err = w.submit(ctx); err == nil {
				t.Fatal("stale plan reused")
			}
			ok, err = alternate.verify(ctx, &ciskov1.NetworkControllerConfigurationOperation{Action: "Update", Resource: fresh.resource, TargetID: originalID, TaskID: task.ID})
			if err != nil || !ok {
				t.Fatal("stale attempt changed selected state", err)
			}
			e = alternate
		} else {
			submitted = time.Now()
			task, err = w.submit(ctx)
			if err != nil {
				t.Fatal(stage, "update uncertain; retain", name, originalID, err)
			}
			t.Log(stage, originalID, task.ID)
			if err = waitSiteCredentialTask(ctx, c, task, submitted); err != nil {
				t.Fatal(stage, "task uncertain", name, err)
			}
			op = &ciskov1.NetworkControllerConfigurationOperation{Action: w.action, Resource: w.resource, TargetID: originalID, TaskID: task.ID}
			verified, err = e.verify(ctx, op)
			if err != nil || !verified {
				t.Fatal(stage, "readback", name, err)
			}
			if _, err = w.submit(ctx); err == nil {
				t.Fatal("successful plan reused")
			}
		}
		if w, err = e.next(ctx); err != nil || w != nil {
			t.Fatal(stage, "no-op", err)
		}
	}
	current, err := c.wirelessAnchorGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created := current[name]
	if created.ID != originalID {
		t.Fatal("cleanup identity changed", name, originalID, created.ID)
	}
	delete(current, name)
	if !reflect.DeepEqual(baseline, current) {
		t.Fatal("baseline or association changed; retain fixture", name)
	}
	submitted = time.Now()
	raw, err := c.mutate(ctx, http.MethodDelete, wirelessAnchorPath+"/"+url.PathEscape(created.ID), nil)
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
	after, err := c.wirelessAnchorGroups(ctx)
	if err != nil || !reflect.DeepEqual(baseline, after) {
		t.Fatal("baseline restoration failed", err)
	}
}
