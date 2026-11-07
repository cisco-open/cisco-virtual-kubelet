// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestDeviceAttachmentModel(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-device-attachment.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resources, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	last := resources[len(resources)-1]
	if len(resources) != 4 || last.Kind != "device-attachment" || last.Name != "CVK-DEVICE-TEMPLATE" || len(last.AttachDevices) != 1 {
		t.Fatal("device attachment must follow its template")
	}
	bad := strings.Replace(string(raw), "device_template: CVK-DEVICE-TEMPLATE", "device_template: MISSING", 1)
	if _, err := Compile([]byte(bad)); err == nil {
		t.Fatal("undeclared device template accepted")
	}
}

func TestDeviceAttachmentLifecycle(t *testing.T) {
	attached := false
	hostname := ""
	edited := false
	c := testClient(t, func(w http.ResponseWriter, request *http.Request) {
		if auth(w, request) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/dataservice/template/device/":
			fmt.Fprint(w, `{"data":[{"templateId":"template1","templateName":"fixture","configType":"template"}]}`)
		case "/dataservice/template/device/config/attached/template1":
			if attached {
				fmt.Fprint(w, `{"data":[{"uuid":"router1","deviceIP":"192.0.2.11"}]}`)
			} else {
				fmt.Fprint(w, `{"data":[]}`)
			}
		case "/dataservice/template/device/config/input":
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload["templateId"] != "template1" {
				t.Error("invalid config input request", err)
			}
			fmt.Fprintf(w, `{"header":{"columns":[{"property":"//system/host-name","title":"Host Name (cvkHostname)","editable":true}]},"data":[{"csv-deviceId":"router1","//system/host-name":%q}]}`, hostname)
		case "/dataservice/template/device/config/attachfeature":
			var payload struct {
				DeviceTemplateList []struct {
					Edited bool             `json:"isEdited"`
					Device []map[string]any `json:"device"`
				} `json:"deviceTemplateList"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || len(payload.DeviceTemplateList) != 1 || len(payload.DeviceTemplateList[0].Device) != 1 {
				t.Error("invalid attachment request", err)
			} else {
				edited = payload.DeviceTemplateList[0].Edited
				hostname, _ = payload.DeviceTemplateList[0].Device[0]["//system/host-name"].(string)
				attached = true
			}
			fmt.Fprint(w, `{"id":"task3"}`)
		case "/dataservice/device/action/status/task3":
			fmt.Fprint(w, `{"summary":{"status":"done"},"validation":{"status":"success"},"data":[]}`)
		default:
			t.Errorf("unexpected device attachment path: %s", request.URL.Path)
		}
	})
	r := Resource{Key: "sites/device_attachments/fixture", Name: "fixture", Section: "sites", Kind: "device-attachment", Type: "attach_feature_device_template", AttachDevices: []DeviceAttachmentTarget{{ID: "router1", Variables: map[string]string{"cvkHostname": "cvk-router-1"}}}, Body: map[string]any{"devices": 1}}
	ctx := context.Background()
	if observed, err := c.Observe(ctx, r); err != nil || observed != nil {
		t.Fatal("unattached observation", err)
	}
	id, err := c.Create(ctx, r)
	if err != nil || id != "template1" || hostname != "cvk-router-1" || edited {
		t.Fatal("initial device attachment", id, err)
	}
	observed, err := c.Observe(ctx, r)
	if err != nil || observed == nil {
		t.Fatal("attached observation", err)
	}
	same, err := Equal(r, observed.Body)
	if err != nil || !same {
		t.Fatal("attached readback", err)
	}
	r.AttachDevices[0].Variables["cvkHostname"] = "cvk-router-updated"
	if err := c.Update(ctx, r, observed); err != nil || !edited || hostname != "cvk-router-updated" {
		t.Fatal("attachment variable update", err)
	}
}
