// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"testing"
)

func TestDeviceMembershipPreservesOtherMembers(t *testing.T) {
	members := []map[string]string{{"instanceUuid": "foreign"}}
	writes := 0
	tagID := "tag"
	deviceID := "device"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		switch r.URL.Path {
		case tagsPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"id": tagID, "name": "lab", "systemTag": false}}})
		case devicesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []Device{{ID: deviceID, Hostname: "switch", ManagementIP: "198.51.100.101", Serial: "serial"}}})
		case tagsPath + "/" + tagID + "/member":
			if r.Method == "GET" {
				if r.URL.Query().Get("memberType") != "networkdevice" {
					t.Error("member type filter missing")
				}
				json.NewEncoder(w).Encode(map[string]any{"response": members})
				return
			}
			if r.Method != "POST" {
				t.Fatal("unexpected removal or replacement")
			}
			var body struct {
				Devices []string `json:"networkdevice"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if len(body.Devices) != 1 || body.Devices[0] != deviceID {
				t.Fatal("unrelated member in mutation")
			}
			members = append(members, map[string]string{"instanceUuid": deviceID})
			writes++
			fmt.Fprint(w, `{"response":{"taskId":"membership-task"}}`)
		default:
			t.Error("unexpected API")
			w.WriteHeader(404)
		}
	})
	executor := &deviceMembershipExecutor{client: c, devices: []desiredDeviceMembership{{device: configurationDeviceReference{Name: "switch", IP: "198.51.100.101", Serial: "serial"}, tags: []string{"lab"}}}}
	write, err := executor.next(context.Background())
	if err != nil || write == nil {
		t.Fatal("missing association", err)
	}
	if _, err = write.submit(context.Background()); err != nil {
		t.Fatal(err)
	}
	op := &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent}
	if ok, err := executor.verify(context.Background(), op); err != nil || !ok {
		t.Fatal("membership verification failed", err)
	}
	if next, err := executor.next(context.Background()); err != nil || next != nil {
		t.Fatal("association replayed", err)
	}
	if writes != 1 || len(members) != 2 || members[0]["instanceUuid"] != "foreign" {
		t.Fatal("foreign association changed")
	}
	deviceID = "replacement-device"
	if ok, err := executor.verify(context.Background(), op); err == nil || ok {
		t.Fatal("replacement device accepted")
	}
	deviceID = "device"
	tagID = "replacement"
	if ok, err := executor.verify(context.Background(), op); err == nil || ok {
		t.Fatal("replacement tag accepted")
	}
}

func TestDeviceMembershipPreflightsEveryTarget(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("unexpected mutation")
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case tagsPath:
			fmt.Fprint(w, `{"response":[{"id":"tag","name":"lab","systemTag":false}]}`)
		case devicesPath:
			fmt.Fprint(w, `{"response":[{"id":"device","hostname":"present","managementIpAddress":"198.51.100.101","serialNumber":"serial"}]}`)
		case tagsPath + "/tag/member":
			fmt.Fprint(w, `{"response":[]}`)
		default:
			t.Error("unexpected API")
			w.WriteHeader(404)
		}
	})
	e := &deviceMembershipExecutor{client: c, devices: []desiredDeviceMembership{
		{device: configurationDeviceReference{Name: "present"}, tags: []string{"lab"}},
		{device: configurationDeviceReference{Name: "absent"}, tags: []string{"lab"}},
	}}
	if write, err := e.next(context.Background()); err == nil || write != nil {
		t.Fatal("released first association despite missing later device")
	}
	e.devices[1].device = configurationDeviceReference{Name: "alias", IP: "198.51.100.101"}
	if write, err := e.next(context.Background()); err == nil || write != nil {
		t.Fatal("accepted duplicate device through alias")
	}
}
