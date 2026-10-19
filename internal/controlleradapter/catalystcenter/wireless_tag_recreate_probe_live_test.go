//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
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

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Observe an exact retained source/operation without submitting or replanning.
// Only public metadata/task identities and comparison results are logged.
func TestLiveWirelessTagRecreationReadOnlyProbe(t *testing.T) {
	probeLiveWirelessTagRecreation(t, false)
}

func TestLiveWirelessTagIdentityCycleProbe(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_TAG_IDENTITY_PROBE") != "isolated-cycle" {
		t.Skip("requires explicit isolated identity experiment")
	}
	probeLiveWirelessTagRecreation(t, true)
}

func probeLiveWirelessTagRecreation(t *testing.T, identityCycle bool) {
	t.Helper()
	path := os.Getenv("CATC_WIRELESS_TAG_RECREATE_PROBE")
	if path == "" {
		t.Skip("requires an explicit retained public source/operation document")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		Source    json.RawMessage                             `json:"source"`
		Operation cvk.NetworkControllerConfigurationOperation `json:"operation"`
	}
	if json.Unmarshal(data, &input) != nil || !wirelessTagPrivateEnvelope(&input.Operation) || !wirelessTagRecreateSelected(input.Operation.Resource) {
		t.Fatal("invalid retained recreation probe input")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c := liveClient(t)
	e, err := c.compileNetworkProfileConfiguration(input.Source)
	if err != nil {
		t.Fatal(err)
	}
	for _, tags := range e.tagExecutors {
		if tags.policy {
			continue
		}
		rows, err := tags.observe(ctx)
		if err != nil {
			t.Fatal("tag observation", err)
		}
		rows, err = bindWirelessTagRows(ctx, c, rows, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			t.Log("public native tag", row.tagID, "selector matches", "wireless-site-tags/"+row.resource == wirelessTagCanonical(input.Operation.Resource))
		}
	}
	_, done, err := e.boundTagReadback(ctx, &input.Operation)
	t.Log("public native readback", done, err)
	complete, taskErr := c.configurationTaskComplete(ctx, input.Operation.TaskID, input.Operation.SubmittedAt.Time, time.Now())
	t.Log("original task complete", complete, taskErr)
	tree, treeErr := c.configurationTaskTreeComplete(ctx, input.Operation.TaskID, input.Operation.SubmittedAt.Time, time.Now())
	t.Log("original task tree complete", tree, treeErr)
	if err != nil || !done || taskErr != nil || !complete || treeErr != nil || !tree {
		t.Fatal("retained recreation has incomplete public readback/task proof")
	}
	if identityCycle {
		// A supervised experiment only after the original task and source have
		// been independently verified. Never replay that original submission.
		profiles, err := c.wirelessProfileInventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		name := ""
		for n, profile := range profiles {
			if profile.id == input.Operation.ParentID {
				name = n
			}
		}
		if !strings.HasPrefix(name, "CVK_N_") {
			t.Fatal("identity experiment requires an exact generated fixture")
		}
		inventory, err := c.wirelessProfileTagInventory(ctx, input.Operation.ParentID, false)
		prior, exists := inventory[name+"_S"]
		if err != nil || !exists || prior.unmapped || len(inventory) != 1 {
			t.Fatal("unexpected fixture tag inventory", err)
		}
		ids, ok := prior.fields["siteIds"].([]string)
		if !ok || len(ids) != 1 {
			t.Fatal("identity experiment requires one isolated empty site")
		}
		if err := c.wirelessTagUnusedScopes(ctx, ids); err != nil {
			t.Fatal("fixture has consumers; refuse experiment", err)
		}
		submit := func(method, path string, payload any) {
			t.Helper()
			var body []byte
			if payload != nil {
				body, err = json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
			}
			at := time.Now()
			raw, err := c.mutate(ctx, method, path, body)
			if err != nil {
				t.Fatal("identity experiment uncertain; do not replay", err)
			}
			var receipt struct {
				Response Task `json:"response"`
			}
			if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
				t.Fatal("identity experiment missing acknowledgement")
			}
			if err := waitSiteCredentialTask(ctx, c, receipt.Response, at); err != nil {
				t.Fatal("identity experiment task uncertain; do not replay", err)
			}
			t.Log("supervised identity experiment task", method, receipt.Response.ID)
		}
		path := wirelessProfileTagPath(input.Operation.ParentID, false)
		submit(http.MethodDelete, path+"/"+url.PathEscape(prior.id), nil)
		absent, err := c.wirelessProfileTagInventory(ctx, input.Operation.ParentID, false)
		if err != nil || len(absent) != 0 {
			t.Fatal("delete did not prove exact tag absence", err)
		}
		if err := c.wirelessTagUnusedScopes(ctx, ids); err != nil {
			t.Fatal(err)
		}
		submit(http.MethodPost, path+"/bulk", map[string]any{"items": []any{prior.fields}})
		after, err := c.wirelessProfileTagInventory(ctx, input.Operation.ParentID, false)
		next, exists := after[prior.name]
		if err != nil || !exists || len(after) != 1 || next.unmapped || !reflect.DeepEqual(prior.fields, next.fields) {
			t.Fatal("identity experiment changed metadata outside the exact fixture", err)
		}
		t.Log("native tag identifier reused after proven delete/create", next.id == prior.id)
	}
}
