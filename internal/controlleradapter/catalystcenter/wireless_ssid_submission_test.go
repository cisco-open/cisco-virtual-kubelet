// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
)

func TestWirelessSSIDSubmissionReobservesSelectedObject(t *testing.T) {
	for _, mode := range []string{"unchanged", "unrelated", "appeared", "replaced", "disappeared", "public-field", "sensor", "extension", "main-credential", "site", "companion-reference"} {
		t.Run(mode, func(t *testing.T) {
			row := ssidTestRow("CVK")
			row["enabled"] = false
			if mode == "companion-reference" {
				row["auth_type"] = "WPA2_ENTERPRISE"
				row["auth_key8021x"] = true
				row["rsn_cipher_suite_ccmp128"] = true
			}
			desired, err := parseWirelessSSIDs(ssidTestModel(row))
			if err != nil {
				t.Fatal(err)
			}
			original := map[string]any{"id": "ssid-id", "ssid": "CVK", "authType": "OPEN", "wlanType": "Enterprise", "isEnabled": true}
			rows := []map[string]any{original}
			if mode == "appeared" {
				rows = []map[string]any{}
			}
			site := "global"
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: site, Name: "Global", NameHierarchy: "Global", Type: "global"}}})
				case r.Method == "GET" && r.URL.Path == wirelessSSIDPath(site):
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case r.Method == "PUT" || r.Method == "POST":
					writes++
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			})
			e := &wirelessSSIDExecutor{client: c, desired: desired, owner: "owner"}
			write, err := e.next(context.Background())
			if err != nil || write == nil {
				t.Fatal("initial plan", err)
			}
			switch mode {
			case "unrelated":
				rows = append(rows, map[string]any{"id": "other-id", "ssid": "Other", "authType": "OPEN", "wlanType": "Enterprise"})
			case "appeared":
				rows = []map[string]any{original}
			case "replaced":
				original["id"] = "replacement"
			case "disappeared":
				rows = []map[string]any{}
			case "public-field":
				original["clientRateLimit"] = 99
			case "sensor":
				original["isSensorPnp"] = true
			case "extension":
				original["unknownPolicy"] = true
			case "main-credential":
				original["passphrase"] = "never-copy-returned-password"
			case "site":
				site = "replacement-site"
			case "companion-reference":
				rows = append(rows, map[string]any{"id": "other-id", "ssid": "Other", "authType": "OPEN-SECURED", "openSsid": "CVK"})
			}
			_, err = write.submit(context.Background())
			allowed := mode == "unchanged" || mode == "unrelated"
			if allowed && (err != nil || writes != 1) {
				t.Fatal("valid write refused", err, writes)
			}
			if !allowed && (err == nil || writes != 0) {
				t.Fatal("stale plan submitted", err, writes)
			}
			if _, err = write.submit(context.Background()); !errors.Is(err, errConfigurationTransition) {
				t.Fatal("consumed plan reusable", err)
			}
			if allowed && writes != 1 || !allowed && writes != 0 {
				t.Fatal("replayed mutation", writes)
			}
		})
	}
}

func TestWirelessSSIDSubmissionSingleUseAfterAmbiguousResult(t *testing.T) {
	desired, err := parseWirelessSSIDs(ssidTestModel(ssidTestRow("CVK")))
	if err != nil {
		t.Fatal(err)
	}
	var writes atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}}})
		case r.Method == "GET":
			fmt.Fprint(w, `{"response":[]}`)
		case r.Method == "POST":
			writes.Add(1)
			fmt.Fprint(w, `{"response":{}}`)
		default:
			t.Errorf("unexpected request %s", r.Method)
		}
	})
	e := &wirelessSSIDExecutor{client: c, desired: desired}
	write, err := e.next(context.Background())
	if err != nil || write == nil {
		t.Fatal("plan", err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := write.submit(context.Background()); err == nil {
				t.Error("ambiguous acknowledgement accepted")
			}
		}()
	}
	wg.Wait()
	if writes.Load() != 1 {
		t.Fatal("ambiguous mutation replayed", writes.Load())
	}
}
