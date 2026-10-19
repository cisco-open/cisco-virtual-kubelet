// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

func TestLiveWirelessSSIDExecution(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_SSID_EXECUTION") != "isolated-definition" {
		t.Skip("explicit isolated definition test not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := liveClient(t)
	site, err := (&wirelessSSIDExecutor{client: c}).global(ctx)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := c.wirelessSSIDInventory(ctx, site)
	if err != nil {
		t.Fatal(err)
	}
	name := "CVK_" + time.Now().UTC().Format("02150405")
	resumeName, resumeID := os.Getenv("CATC_SSID_RESUME_NAME"), os.Getenv("CATC_SSID_RESUME_ID")
	resume := resumeName != "" || resumeID != ""
	credential := os.Getenv("CATC_SSID_CREDENTIAL_EXECUTION") == "isolated-definition"
	if resume && credential {
		t.Fatal("credential fixtures require supervised recovery")
	}
	if resume {
		// Resume only an explicitly identified, disabled, unassociated test
		// definition from an otherwise empty baseline; never recreate it.
		actual, exists := baseline[resumeName]
		if !exists || len(baseline) != 1 || resumeID == "" || actual.id != resumeID || actual.fields["isEnabled"] != false || actual.fields["authType"] != "OPEN" || actual.fields["clientExclusionTimeout"] != int64(180) {
			t.Fatal("resume fixture identity or baseline mismatch")
		}
		name = resumeName
		delete(baseline, name)
	}
	if _, exists := baseline[name]; exists {
		t.Fatal("fixture exists")
	}
	row := ssidTestRow(name)
	row["ssid_radio_type"] = "5GHz"
	row["enabled"] = false
	if credential {
		row["auth_type"] = "WPA2_PERSONAL"
		row["auth_key_psk"] = true
		row["rsn_cipher_suite_ccmp128"] = true
		row["passphrase_version"] = 1
	}
	desired, err := parseWirelessSSIDs(ssidTestModel(row))
	if err != nil {
		t.Fatal(err)
	}
	e := &wirelessSSIDExecutor{client: c, desired: desired}
	project := func(version int64) {
		t.Helper()
		// Native qualification only: fresh values stay in memory. Worker tests
		// separately qualify Kubernetes projection and durable receipt storage.
		bytes := make([]byte, 24)
		if _, err := rand.Read(bytes); err != nil {
			t.Fatal(err)
		}
		e.owner = "owner"
		e.desired[0].credentials[0].version = version
		e.secrets = ssidCredentialSnapshot(e.desired[0], map[string]string{"passphrase": hex.EncodeToString(bytes)})
	}
	if credential {
		project(1)
	}
	verify := func(w *configurationWrite, task Task) (bool, error) {
		op := &ciskov1.NetworkControllerConfigurationOperation{Action: w.action, Resource: w.resource, TargetID: w.identity.target, TaskID: task.ID, ParentID: site, Section: "wireless", ConfigUID: e.owner, CredentialVersions: w.identity.credentials, SSIDCredentialHistory: w.identity.ssidCredentialHistory}
		if !credential {
			return e.verify(ctx, op)
		}
		id, ok, err := e.verifyCredential(ctx, op)
		if err == nil && ok {
			e.receipts = []ciskov1.NetworkControllerConfigurationCredentialApplication{{Section: "wireless", ConfigUID: e.owner, Resource: w.resource, TargetID: id, OperationID: strings.Repeat("a", 32), Versions: w.identity.credentials}}
		}
		return ok, err
	}
	var w *configurationWrite
	var task Task
	var verified bool
	submitted := time.Now()
	if !resume {
		w, err = e.next(ctx)
		if err != nil || w == nil {
			t.Fatal("definition plan", err)
		}
		submitted = time.Now()
		task, err = w.submit(ctx)
		if err != nil {
			t.Fatal("submission uncertain; inspect fixture", name, err)
		}
		t.Log("create", name, task.ID)
		if err = waitSiteCredentialTask(ctx, c, task, submitted); err != nil {
			t.Fatal("task uncertain; retain fixture", name, err)
		}
		verified, err = verify(w, task)
		if err != nil || !verified {
			t.Fatal("readback incomplete; retain fixture", name, err)
		}
		if w, err = e.next(ctx); err != nil || w != nil {
			t.Fatal("definition no-op failed", err)
		}

	} else if w, err = e.next(ctx); err != nil || w != nil {
		t.Fatal("resume initial no-op", err)
	}

	e.desired[0].metadata["clientExclusionTimeout"] = int64(240)
	if credential {
		project(2)
	}
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
	verified, err = verify(w, task)
	if err != nil || !verified {
		t.Fatal("update readback", name, err)
	}
	if w, err = e.next(ctx); err != nil || w != nil {
		t.Fatal("update no-op", err)
	}
	current, err := c.wirelessSSIDInventory(ctx, site)
	if err != nil {
		t.Fatal(err)
	}
	created := current[name]
	delete(current, name)
	if !reflect.DeepEqual(baseline, current) {
		t.Fatal("baseline or association changed; retain fixture", name)
	}
	submitted = time.Now()
	raw, err := c.mutate(ctx, http.MethodDelete, wirelessSSIDPath(site)+"/"+url.PathEscape(created.id), nil)
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
	after, err := c.wirelessSSIDInventory(ctx, site)
	if err != nil || !reflect.DeepEqual(baseline, after) {
		t.Fatal("baseline restoration failed", err)
	}
}
