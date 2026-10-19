// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestWirelessSSIDEmptyMultiPSKOwnershipReadback(t *testing.T) {
	empty := map[string]any{"ssid": "CVK", "multiPSKSettings": []map[string]any{}}
	for _, tc := range []struct {
		name    string
		got     map[string]any
		matches bool
	}{
		{"omitted", map[string]any{"ssid": "CVK"}, true},
		{"explicit empty", map[string]any{"ssid": "CVK", "multiPSKSettings": []map[string]any{}}, true},
		{"populated", map[string]any{"ssid": "CVK", "multiPSKSettings": []map[string]any{{"priority": int64(0)}}}, false},
		{"malformed", map[string]any{"ssid": "CVK", "multiPSKSettings": ""}, false},
		{"nil unnormalized", map[string]any{"ssid": "CVK", "multiPSKSettings": nil}, false},
		{"other drift", map[string]any{"ssid": "Other"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if wirelessSSIDMatches(empty, tc.got) != tc.matches {
				t.Fatal("incorrect empty-set match")
			}
		})
	}
	if !wirelessSSIDMatches(map[string]any{"ssid": "CVK"}, map[string]any{"ssid": "CVK", "multiPSKSettings": []map[string]any{{"priority": int64(0)}}}) {
		t.Fatal("omitted intent acquired collection ownership")
	}
}

func TestWirelessSSIDInventoryCanonicalMultiPSKObservation(t *testing.T) {
	order := []int{4, 0}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		entries := []map[string]any{}
		for _, priority := range order {
			entries = append(entries, map[string]any{"priority": priority, "passphraseType": "ASCII", "passphrase": "never-retain-readback"})
		}
		json.NewEncoder(w).Encode(map[string]any{"response": []map[string]any{{"id": "ssid-id", "ssid": "CVK", "authType": "WPA2_PERSONAL", "wlanType": "Enterprise", "multiPSKSettings": entries}}})
	})
	first, err := c.wirelessSSIDInventory(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	order = []int{0, 4}
	next, err := c.wirelessSSIDInventory(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, next) {
		t.Fatal("native order changed public identity")
	}
	entries := next["CVK"].fields["multiPSKSettings"].([]map[string]any)
	if entries[0]["priority"] != int64(0) || entries[1]["priority"] != int64(4) {
		t.Fatal("noncanonical readback")
	}
	for _, entry := range entries {
		if _, present := entry["passphrase"]; present {
			t.Fatal("retained returned credential")
		}
	}
}

func TestWirelessSSIDSubmissionAcceptsNativePriorityReorder(t *testing.T) {
	row := ssidTestRow("CVK")
	row["auth_type"] = "WPA2_PERSONAL"
	row["auth_key_psk"] = true
	row["rsn_cipher_suite_ccmp128"] = true
	row["multi_psk_settings"] = []any{map[string]any{"priority": 4, "passphrase_type": "ASCII", "passphrase_wo_version": 2}, map[string]any{"priority": 0, "passphrase_type": "ASCII", "passphrase_wo_version": 1}}
	desired, err := parseWirelessSSIDs(ssidTestModel(row))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := ssidCredentialSnapshot(desired[0], map[string]string{"multiPSKSettings.0.passphrase": "priority-four-secret", "multiPSKSettings.1.passphrase": "priority-zero-secret"})
	_, versions, err := resolveVersionedConfigurationCredentials(snapshot, "owner", "wireless", desired[0].credentials)
	if err != nil {
		t.Fatal(err)
	}
	prior := append([]ciskov1.NetworkControllerConfigurationCredentialVersion(nil), versions...)
	for i := range prior {
		prior[i].Version = 1
	}
	reads, writes := 0, 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}}})
		case r.Method == "GET":
			order := []int{4, 0}
			if reads%2 == 1 {
				order = []int{0, 4}
			}
			reads++
			entries := []map[string]any{}
			for _, priority := range order {
				entries = append(entries, map[string]any{"priority": priority, "passphraseType": "ASCII"})
			}
			json.NewEncoder(w).Encode(map[string]any{"response": []map[string]any{{"id": "ssid-id", "ssid": "CVK", "authType": "WPA2_PERSONAL", "wlanType": "Enterprise", "isAuthKeyPSK": true, "rsnCipherSuiteCcmp128": true, "multiPSKSettings": entries}}})
		case r.Method == "PUT" && r.URL.Path == wirelessSSIDPath("global")+"/ssid-id":
			writes++
			fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	e := &wirelessSSIDExecutor{client: c, desired: desired, secrets: snapshot, owner: "owner", receipts: []ciskov1.NetworkControllerConfigurationCredentialApplication{{Section: "wireless", Resource: "ssid-credential:global:CVK", ConfigUID: "owner", TargetID: "ssid-id", OperationID: strings.Repeat("a", 32), Versions: prior}}}
	write, err := e.next(context.Background())
	if err != nil || write == nil {
		t.Fatal("plan", err)
	}
	if _, err = write.submit(context.Background()); err != nil || writes != 1 || reads != 2 {
		t.Fatal("response reorder refused", err, writes, reads)
	}
}
