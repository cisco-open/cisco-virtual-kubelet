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

func TestLiveWirelessFRA(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_FRA_EXECUTION") != "isolated-definition" {
		t.Skip("explicit isolated definition test not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := liveClient(t)
	baseline, err := c.wirelessFeatureInstances(ctx, wirelessFRAKind)
	if err != nil {
		t.Fatal(err)
	}
	name := "CVK_" + time.Now().UTC().Format("02150405")
	if _, exists := baseline[name]; exists {
		t.Fatal("fixture exists")
	}
	e := &wirelessFRAExecutor{client: c, desired: []wirelessFRA{{Name: name, Attributes: wirelessFRAAttributes{Band: "2_4GHZ_5GHZ", Interval: 3, Sensitivity: "Medium"}}}}
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
		t.Fatal("prepared create reuse", name, replayErr)
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

	e.desired[0].Attributes.Interval = 6
	w, err = e.next(ctx)
	if err != nil || w == nil || w.action != "Update" || !validAPIID(w.identity.target) {
		t.Fatal("update plan", err)
	}
	if os.Getenv("CATC_WIRELESS_FRA_TRANSITIONS") == "isolated-definition" {
		otherDesired := append([]wirelessFRA(nil), e.desired...)
		otherDesired[0].Attributes.Interval = 3
		otherDesired[0].Attributes.Freeze = true
		other := &wirelessFRAExecutor{client: c, desired: otherDesired}
		concurrent, err := other.next(ctx)
		if err != nil || concurrent == nil || concurrent.action != "Update" || concurrent.identity.target != w.identity.target {
			t.Fatal("generated-only concurrent FRA edit plan", name, err)
		}
		started := time.Now()
		changed, err := concurrent.submit(ctx)
		if err != nil {
			t.Fatal("concurrent FRA edit uncertain; retain fixture", name, err)
		}
		t.Log("generated-only concurrent freeze edit", concurrent.identity.target, changed.ID)
		if err = waitSiteCredentialTask(ctx, c, changed, started); err != nil {
			t.Fatal("concurrent FRA task incomplete", name, err)
		}
		if _, staleErr := w.submit(ctx); staleErr != errConfigurationStale {
			t.Fatal("stale native FRA snapshot accepted", name, staleErr)
		}
		if _, replayErr := w.submit(ctx); replayErr != errConfigurationTransition {
			t.Fatal("stale FRA permit reusable", name, replayErr)
		}
		w, err = e.next(ctx)
		if err != nil || w == nil || w.action != "Update" || w.identity.target != concurrent.identity.target {
			t.Fatal("fresh FRA update plan", name, err)
		}
		t.Log("stale FRA snapshot refused and permit consumed")
	}
	submitted = time.Now()
	task, err = w.submit(ctx)
	if err != nil {
		t.Fatal("update uncertain; retain fixture", name, err)
	}
	t.Log("update", w.identity.target, task.ID)
	if _, replayErr := w.submit(ctx); replayErr != errConfigurationTransition {
		t.Fatal("prepared update reuse", name, replayErr)
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
	if os.Getenv("CATC_WIRELESS_FRA_TRANSITIONS") == "isolated-definition" {
		id := op.TargetID
		states := wirelessFRAQualificationStates()[2:]
		for _, state := range states {
			e.desired[0].Attributes = state
			write, err := e.next(ctx)
			if err != nil || write == nil || write.action != "Update" || write.identity.target != id {
				t.Fatal("FRA transition plan", name, state, err)
			}
			started := time.Now()
			task, err := write.submit(ctx)
			if err != nil {
				t.Fatal("FRA transition submission uncertain; retain fixture", name, id, state, err)
			}
			t.Log("transition", id, state, task.ID)
			if _, replayErr := write.submit(ctx); replayErr != errConfigurationTransition {
				t.Fatal("FRA transition permit reused", name, replayErr)
			}
			if err = waitSiteCredentialTask(ctx, c, task, started); err != nil {
				t.Fatal("FRA transition incomplete; retain fixture", name, id, state, err)
			}
			operation := &ciskov1.NetworkControllerConfigurationOperation{Action: write.action, Resource: write.resource, TargetID: write.identity.target, TaskID: task.ID}
			if ok, err := e.verify(ctx, operation); err != nil || !ok {
				t.Fatal("FRA transition readback", name, id, state, err)
			}
			if next, err := e.next(ctx); err != nil || next != nil {
				t.Fatal("FRA transition no-op", name, state, err)
			}
		}
	}
	current, err := c.wirelessFeatureInstances(ctx, wirelessFRAKind)
	if err != nil {
		t.Fatal(err)
	}
	created := current[name]
	delete(current, name)
	if !reflect.DeepEqual(baseline, current) {
		t.Fatal("baseline or association changed; retain fixture", name)
	}
	submitted = time.Now()
	raw, err := c.mutate(ctx, http.MethodDelete, wirelessFRAPath+"/"+url.PathEscape(created.ID), nil)
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
	after, err := c.wirelessFeatureInstances(ctx, wirelessFRAKind)
	if err != nil || !reflect.DeepEqual(baseline, after) {
		t.Fatal("baseline restoration failed", err)
	}
}
