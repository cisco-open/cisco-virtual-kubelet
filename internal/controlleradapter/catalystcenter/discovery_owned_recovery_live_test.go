// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Only generated qualification artifacts with an already published creation
// receipt are accepted. This is not public resource deletion or name adoption.
func TestLiveRetainedOwnedDiscoveryCleanup(t *testing.T) {
	dir := os.Getenv("CATC_DISCOVERY_OWNED_RETAINED")
	if dir == "" {
		t.Skip("no retained owned discovery supplied")
	}
	var identity struct {
		Name, ID, IP   string
		Posts, Deletes int
	}
	read := func(file string, value any) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || json.Unmarshal(raw, value) != nil {
			t.Fatal("private discovery recovery evidence unavailable", file, err)
		}
	}
	read("identity.json", &identity)
	var state cvk.NetworkControllerConfigurationStatus
	read("journal.json", &state)
	var before map[string]discoveryMetadata
	read("baseline.json", &before)
	source, err := os.ReadFile(filepath.Join(dir, "source.json"))
	if err != nil {
		t.Fatal(err)
	}
	intent, err := parseInventoryIntent(source)
	if err != nil || len(intent.discoveries) != 1 || len(intent.devices) != 0 || len(intent.tags) != 0 || len(state.ActionApplications) != 1 {
		t.Fatal("retained discovery source/receipt shape refused", err)
	}
	want := intent.discoveries[0]
	receipt := state.ActionApplications[0]
	if !strings.HasPrefix(identity.Name, "CVK_DISC_") || want.name != identity.Name || identity.IP != "198.51.100.101" || want.fields["ipAddressList"] != identity.IP || identity.Posts < 1 || identity.Posts > 2 || identity.Deletes != identity.Posts-1 || !validOwnedDiscoveryReceipt(receipt) || receipt.Operation.Action != "Create" || receipt.Operation.ConfigUID != "config" || receipt.Operation.ConfigName != "sites" || receipt.Operation.ConfigGeneration != int64(identity.Posts) {
		t.Fatal("retained qualification ownership refused")
	}
	prefix := ownedDiscoveryReceiptKey(identity.Name, "")
	id := strings.TrimPrefix(receipt.Resource, prefix)
	if !strings.HasPrefix(receipt.Resource, prefix) || !validAPIID(id) {
		t.Fatal("retained native UUID unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := liveClient(t)
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	trace := &discoveryOwnershipTrace{base: base, name: identity.Name, id: id, protocol: "SSH", maxPosts: 0, maxDeletes: 1}
	rest.HTTPClient.Transport = trace
	cleanupPath := filepath.Join(dir, "cleanup.json")
	var cleanup qualificationCleanup
	resumed := false
	if raw, err := os.ReadFile(cleanupPath); err == nil {
		if json.Unmarshal(raw, &cleanup) != nil || cleanup.Name != identity.Name || cleanup.ID != id || !cleanup.DeleteAttempted || cleanup.SubmittedAt.IsZero() || cleanup.UpdateTaskID != receipt.Operation.TaskID || !validAPIID(cleanup.DeleteTaskID) {
			t.Fatal("prior cleanup uncertain; no replay")
		}
		resumed = true
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	save := func() {
		raw, _ := json.Marshal(cleanup)
		if err := os.WriteFile(cleanupPath, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if !resumed {
		if err := waitSiteCredentialTask(ctx, c, Task{ID: receipt.Operation.TaskID}, receipt.Operation.SubmittedAt.Time); err != nil {
			t.Fatal(err)
		}
		e := &ownedDiscoveryExecutor{base: &discoveryExecutor{client: c, desired: intent.discoveries}, owner: receipt.Operation.ConfigUID}
		observed, done, err := e.verifyOwned(ctx, &receipt.Operation)
		if err != nil || !done || observed != id {
			t.Fatal("retained task/UUID/effect not verified", err)
		}
		for range 2 {
			current, err := c.discoveries(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if current[identity.Name].ID != id {
				t.Fatal("retained UUID replaced")
			}
			delete(current, identity.Name)
			if !reflect.DeepEqual(before, current) {
				t.Fatal("original discovery baseline changed")
			}
		}
		if os.Getenv("CATC_DISCOVERY_CLEANUP_OBSERVATION") == "existing-lab-single" {
			beforeAttempts := trace.attempts
			qualifyLiveRetainedDiscoveryCleanupObservation(t, c, source, &state, id, dir)
			if trace.attempts != beforeAttempts {
				t.Fatal("cleanup prerequisite performed a native mutation")
			}
		}
		cleanup = qualificationCleanup{Name: identity.Name, ID: id, UpdateTaskID: receipt.Operation.TaskID, SubmittedAt: time.Now(), DeleteAttempted: true}
		if err := claimQualificationCleanup(cleanupPath, cleanup); err != nil {
			t.Fatal("cleanup already claimed; no replay", err)
		}
		trace.allowDelete = true
		task, err := c.deleteDiscovery(ctx, id)
		if err != nil {
			t.Fatal("exact deletion uncertain; no replay", err)
		}
		trace.allowDelete = false
		cleanup.DeleteTaskID = task.ID
		save()
	}
	if err := waitSiteCredentialTask(ctx, c, Task{ID: cleanup.DeleteTaskID}, cleanup.SubmittedAt); err != nil {
		t.Fatal(err)
	}
	absent := false
	for range 30 {
		current, err := c.discoveries(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := current[identity.Name]; !exists {
			if !reflect.DeepEqual(before, current) {
				t.Fatal("original discovery baseline changed")
			}
			absent = true
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	if !absent {
		t.Fatal("exact deletion not absent; no retry")
	}
	if resumed && trace.attempts != 0 || !resumed && (trace.attempts != 1 || trace.deletes != 1) {
		t.Fatal("cleanup mutation budget exceeded")
	}
	cleanup.Deleted = true
	save()
	t.Log("verified retained discovery exact cleanup/absence; original baseline restored", identity.Name, id, cleanup.DeleteTaskID, "read-only-resume", resumed)
}
