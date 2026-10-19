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

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func apSnapshot(version int64) *configurationSecretSnapshot {
	raw, _ := json.Marshal(map[string]any{"version": version, "value": "fixture-password"})
	return &configurationSecretSnapshot{uid: "owner", values: []configurationSecretValue{{ref: configapi.NetworkControllerSecretRef{Section: "wireless", Path: "/ap_profiles/0/management_password", Source: "ap"}, source: ciskov1.NetworkControllerIntentSecretSource{Name: "projected", Key: "password"}, value: raw}}}
}
func TestWirelessAPCredentialCreateRotationAndReplacement(t *testing.T) {
	ctx := context.Background()
	raw := strings.Replace(wirelessAPModel, `"name":"AP-LAB"`, `"name":"AP-LAB","management_password_version":1`, 1)
	desired, err := parseWirelessAPProfiles([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{}
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.Method == "GET" && r.URL.Path == wirelessAPPath:
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.Method == "POST" && r.URL.Path == wirelessAPPath || r.Method == "PUT" && r.URL.Path == wirelessAPPath+"/ap-id":
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid payload")
				return
			}
			value, ok := getWirelessAPField(body, "managementSetting.managementPassword")
			if !ok || value != "fixture-password" {
				t.Error("projected credential missing")
			}
			body["id"] = "ap-id"
			rows = []map[string]any{body}
			writes++
			fmt.Fprint(w, `{"response":{"taskId":"ap-task"}}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	e := &wirelessAPExecutor{client: c, desired: desired, secrets: apSnapshot(1), owner: "owner"}
	for _, version := range []int64{1, 2} {
		e.desired[0].credentials[0].version = version
		e.secrets = apSnapshot(version)
		write, err := e.next(ctx)
		if err != nil || write == nil || len(write.identity.credentials) != 1 {
			t.Fatal("rotation plan", err)
		}
		if _, err = write.submit(ctx); err != nil {
			t.Fatal(err)
		}
		op := &ciskov1.NetworkControllerConfigurationOperation{Section: "wireless", ConfigUID: "owner", Action: write.action, Resource: write.resource, TargetID: write.identity.target, CredentialVersions: write.identity.credentials}
		restarted := *e
		if id, ok, err := restarted.verifyCredential(ctx, op); err != nil || !ok || id != "ap-id" {
			t.Fatal("credential verification", err)
		}
		if _, err := restarted.verify(ctx, op); err == nil {
			t.Fatal("credential bypassed receipt verification")
		}
		e.receipts = []ciskov1.NetworkControllerConfigurationCredentialApplication{{ConfigUID: "owner", Section: "wireless", Resource: write.resource, TargetID: "ap-id", OperationID: strings.Repeat("a", 32), Versions: write.identity.credentials}}
		if write, err = e.next(ctx); err != nil || write != nil {
			t.Fatal("rotation repeated", err)
		}
	}
	if writes != 2 {
		t.Fatal("request replayed")
	}
	e.secrets = apSnapshot(1)
	if write, err := e.next(ctx); err == nil || write != nil {
		t.Fatal("stale projection accepted")
	}
	e.secrets = apSnapshot(2)
	rows[0]["id"] = "replacement"
	if write, err := e.next(ctx); err == nil || write != nil {
		t.Fatal("foreign credential resource adopted")
	}
}
func TestWirelessAPPreflightAndPreservation(t *testing.T) {
	for _, test := range []string{"built-in", "dependency", "preserve", "calendar-drift"} {
		t.Run(test, func(t *testing.T) {
			ctx := context.Background()
			desired, _ := parseWirelessAPProfiles([]byte(wirelessAPModel))
			actual := cloneWirelessAPFields(desired[0].metadata)
			actual["id"] = "ap-id"
			actual["description"] = "retained"
			actual["clientLimit"] = int64(20)
			if test == "built-in" {
				desired[0].name = "default-ap-profile"
				desired[0].metadata["apProfileName"] = desired[0].name
				actual["apProfileName"] = desired[0].name
			}
			if test == "dependency" {
				desired[0].metadata["apPowerProfileName"] = "absent"
			}
			if test == "calendar-drift" {
				var cal any
				json.Unmarshal([]byte(`[{"power_profile":"POWER","scheduler_type":"MONTHLY","scheduler_start_time":"12:00","scheduler_end_time":"13:00","scheduler_date":["3"]}]`), &cal)
				values, _ := parseWirelessAPCalendar(cal)
				desired[0].metadata["calendarPowerProfiles"] = values
				actual["calendarPowerProfiles"] = []any{}
			}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == wirelessAPPath && r.Method == "GET":
					json.NewEncoder(w).Encode(map[string]any{"response": []any{actual}})
				case r.URL.Path == wirelessPowerPath:
					fmt.Fprint(w, `{"response":[]}`)
				case r.URL.Path == wirelessAPPath+"/ap-id" && r.Method == "PUT":
					writes++
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					if body["description"] != "retained" || body["id"] != nil {
						t.Error("optional field or identity mishandled")
					}
					fmt.Fprint(w, `{"response":{"taskId":"ap-task"}}`)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			})
			e := &wirelessAPExecutor{client: c, desired: desired}
			if test == "calendar-drift" {
				ok, err := e.verify(ctx, &ciskov1.NetworkControllerConfigurationOperation{Resource: desired[0].resource(), Action: "Update", TargetID: "ap-id"})
				if err != nil || ok {
					t.Fatal("ignored calendar update reported verified", err)
				}
				return
			}
			write, err := e.next(ctx)
			if test != "preserve" {
				if err == nil || write != nil || writes != 0 {
					t.Fatal("unsafe preflight permitted mutation")
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal(err)
			}
			if _, err = write.submit(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWirelessAPRejectsIncompleteAuthenticationBeforeSubmission(t *testing.T) {
	for _, field := range []string{`"auth_type":"EAP-PEAP"`, `"ssh_enabled":true`, `"awips_forensic_enabled":true`, `"remote_worker_enabled":true,"rogue_detection":true`} {
		profiles, err := parseWirelessAPProfiles([]byte(strings.Replace(wirelessAPModel, `"name":"AP-LAB"`, `"name":"AP-LAB",`+field, 1)))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := profiles[0].payload(nil, "", nil); err == nil {
			t.Fatal("invalid feature combination accepted")
		}
	}
}

func TestWirelessAPMetadataNoopDoesNotRequirePasswordReadback(t *testing.T) {
	profiles, err := parseWirelessAPProfiles([]byte(strings.Replace(wirelessAPModel, `"name":"AP-LAB"`, `"name":"AP-LAB","auth_type":"EAP-PEAP"`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	actual := cloneWirelessAPFields(profiles[0].metadata)
	actual["id"] = "existing"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" || r.URL.Path != wirelessAPPath {
			t.Error("no-op attempted a mutation")
		}
		json.NewEncoder(w).Encode(map[string]any{"response": []any{actual}})
	})
	e := &wirelessAPExecutor{client: c, desired: profiles}
	if write, err := e.next(context.Background()); err != nil || write != nil {
		t.Fatal("exact public no-op demanded password", err)
	}
}
