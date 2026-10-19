// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestWirelessSSIDNativeCredentialCycle(t *testing.T) {
	ctx := context.Background()
	row := ssidTestRow("CVK")
	row["auth_type"] = "WPA2_PERSONAL"
	row["auth_key_psk"] = true
	row["rsn_cipher_suite_ccmp128"] = true
	row["passphrase_version"] = 1
	desired, err := parseWirelessSSIDs(ssidTestModel(row))
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{}
	writes := 0
	site := "global"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: site, Name: "Global", NameHierarchy: "Global", Type: "global"}}})
		case r.Method == "GET" && r.URL.Path == wirelessSSIDPath(site):
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.Method == "POST" && r.URL.Path == wirelessSSIDPath(site) || r.Method == "PUT" && r.URL.Path == wirelessSSIDPath(site)+"/ssid-id":
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid payload")
				return
			}
			if body["passphrase"] != "fixture-secret" {
				t.Error("missing projected credential")
			}
			body["id"] = "ssid-id"
			body["inheritedSiteId"] = site
			rows = []map[string]any{body}
			writes++
			fmt.Fprint(w, `{"response":{"taskId":"ssid-task"}}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(400)
		}
	})
	e := &wirelessSSIDExecutor{client: c, desired: desired, owner: "owner"}
	for _, version := range []int64{1, 2} {
		e.desired[0].credentials[0].version = version
		e.secrets = ssidCredentialSnapshot(e.desired[0], map[string]string{"passphrase": "fixture-secret"})
		write, err := e.next(ctx)
		if err != nil || write == nil || write.identity.parent != site {
			t.Fatal("write plan", err)
		}
		if _, err = write.submit(ctx); err != nil {
			t.Fatal(err)
		}
		op := &ciskov1.NetworkControllerConfigurationOperation{Section: "wireless", ConfigUID: "owner", Action: write.action, Resource: write.resource, ParentID: site, TargetID: write.identity.target, CredentialVersions: write.identity.credentials, SSIDCredentialHistory: write.identity.ssidCredentialHistory}
		restarted := *e
		id, ok, err := restarted.verifyCredential(ctx, op)
		if err != nil || !ok || id != "ssid-id" {
			t.Fatal("credential verification", err)
		}
		if _, err = restarted.verify(ctx, op); err == nil {
			t.Fatal("credential bypassed receipt boundary")
		}
		e.receipts = []ciskov1.NetworkControllerConfigurationCredentialApplication{{Section: "wireless", ConfigUID: "owner", Resource: write.resource, TargetID: id, OperationID: strings.Repeat("a", 32), Versions: write.identity.credentials, SSIDCredentialHistory: write.identity.ssidCredentialHistory}}
		if write, err = e.next(ctx); err != nil || write != nil {
			t.Fatal("replayed credential mutation", err)
		}
		op.ParentID = "replacement-site"
		if _, _, err = e.verifyCredential(ctx, op); err == nil {
			t.Fatal("site replacement accepted")
		}
	}
	if writes != 2 {
		t.Fatal("write count", writes)
	}
	rows[0]["id"] = "replacement-ssid"
	if write, err := e.next(ctx); err == nil || write != nil {
		t.Fatal("foreign credential SSID adopted")
	}
	rows[0]["id"] = "ssid-id"
	site = "new-global"
	if write, err := e.next(ctx); err == nil || write != nil {
		t.Fatal("receipt crossed Global site identity")
	}
}

func TestWirelessSSIDPreflightAndCompanionOrder(t *testing.T) {
	open := ssidTestRow("Z-open")
	secured := ssidTestRow("A-secured")
	secured["auth_type"] = "OPEN_SECURED"
	secured["protected_management_frame"] = "REQUIRED"
	secured["rsn_cipher_suite_ccmp128"] = true
	secured["auth_key_owe"] = true
	secured["open_ssid"] = "Z-open"
	desired, err := parseWirelessSSIDs(ssidTestModel(secured, open))
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("preflight submitted mutation")
		}
		if r.URL.Path == sitesPath {
			json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
	})
	e := &wirelessSSIDExecutor{client: c, desired: desired}
	write, err := e.next(context.Background())
	if err != nil || write == nil || write.resource != "ssid:global:Z-open" {
		t.Fatal("companion not planned first", err)
	}
	e.desired[0].metadata["openSsid"] = "absent"
	if write, err = e.next(context.Background()); err == nil || write != nil {
		t.Fatal("invalid dependent allowed prerequisite write")
	}
	e.desired[0].metadata["openSsid"] = "Z-open"
	rows = []map[string]any{{"id": "foreign", "ssid": "foreign", "authType": "OPEN-SECURED", "wlanType": "Enterprise", "openSsid": "Z-open"}}
	if write, err = e.next(context.Background()); err == nil || write != nil {
		t.Fatal("shared OWE companion accepted")
	}
	for i := range e.desired {
		if e.desired[i].name == "Z-open" {
			e.desired[i].metadata["authType"] = "WPA2_ENTERPRISE"
			e.desired[i].metadata["isAuthKey8021x"] = true
			e.desired[i].metadata["rsnCipherSuiteCcmp128"] = true
			e.desired = []wirelessSSIDIntent{e.desired[i]}
			break
		}
	}
	if write, err = e.next(context.Background()); err == nil || write != nil {
		t.Fatal("referenced open companion authentication changed")
	}
}

func TestWirelessSSIDMultiPSKComparisonUsesPriority(t *testing.T) {
	want := map[string]any{"multiPSKSettings": []map[string]any{{"priority": int64(4)}, {"priority": int64(0), "passphraseType": "ASCII"}}}
	got := map[string]any{"multiPSKSettings": []map[string]any{{"priority": int64(0), "passphraseType": "ASCII"}, {"priority": int64(4), "passphraseType": "HEX"}}}
	if !wirelessSSIDMatches(want, got) {
		t.Fatal("server ordering caused drift")
	}
	got["multiPSKSettings"].([]map[string]any)[0]["passphraseType"] = "HEX"
	if wirelessSSIDMatches(want, got) {
		t.Fatal("owned public metadata drift missed")
	}
}
