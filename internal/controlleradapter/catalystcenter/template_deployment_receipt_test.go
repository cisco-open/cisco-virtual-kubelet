// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

const deploymentFixtureID = "11111111-1111-4111-8111-111111111111"

func TestTemplateDeploymentReceipt(t *testing.T) {
	for _, tt := range []struct {
		name, raw string
		valid     bool
	}{
		{"direct", `{"deploymentId":"` + deploymentFixtureID + `"}`, true},
		{"nested", `{"response":{"deploymentId":"` + deploymentFixtureID + `"}}`, true},
		{"provider-progress", `{"response":{"progress":"Template Deployemnt Id: ` + deploymentFixtureID + `"}}`, true},
		{"corrected-progress", `{"response":{"progress":"Template Deployment Id: ` + deploymentFixtureID + `"}}`, true},
		{"consistent", `{"deploymentId":"` + deploymentFixtureID + `","response":{"progress":"Template Deployment Id: ` + deploymentFixtureID + `"}}`, true},
		{"progress-suffix", `{"response":{"progress":"Template Deployment Id: ` + deploymentFixtureID + `xyz"}}`, false},
		{"progress-path", `{"response":{"progress":"Template Deployment Id: ` + deploymentFixtureID + `/other"}}`, false},
		{"progress-trailing-text", `{"response":{"progress":"Template Deployment Id: ` + deploymentFixtureID + ` queued"}}`, true},
		{"missing", `{}`, false},
		{"task-only", `{"response":{"taskId":"task"}}`, false},
		{"optimistic-message", `{"response":{"progress":"Success, secret-output"}}`, false},
		{"conflicting", `{"deploymentId":"` + deploymentFixtureID + `","response":{"deploymentId":"22222222-2222-4222-8222-222222222222"}}`, false},
		{"duplicate-progress", `{"response":{"progress":"Template Deployment Id: ` + deploymentFixtureID + ` Template Deployment Id: ` + deploymentFixtureID + `"}}`, false},
		{"nil-uuid", `{"deploymentId":"00000000-0000-0000-0000-000000000000"}`, false},
		{"path", `{"deploymentId":"../secret"}`, false},
		{"malformed", `{"response":`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			id, err := templateDeploymentReceipt([]byte(tt.raw))
			if (err == nil) != tt.valid {
				t.Fatal(id, err)
			}
			if tt.valid && id != deploymentFixtureID {
				t.Fatal(id)
			}
			if !tt.valid && id != "" {
				t.Fatal("partial receipt escaped")
			}
		})
	}
}
func TestTemplateDeploymentCompletion(t *testing.T) {
	for _, mode := range []string{"success", "rfc3339", "init", "running", "device-running", "failure", "device-failure", "unknown", "wrong-id", "wrong-device", "wrong-identifier", "wrong-type", "wrong-project", "wrong-template", "wrong-version", "missing-devices", "duplicate-devices", "missing-start", "old-start", "future-end", "reverse-time", "device-old-start", "missing-status", "unqueryable"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			submitted := now.Add(-time.Minute)
			start, end := strconv.FormatInt(submitted.UnixMilli(), 10), strconv.FormatInt(now.UnixMilli(), 10)
			got := templateDeploymentStatus{ID: deploymentFixtureID, Project: "Lab", Name: "Baseline", Version: "2", Status: "SUCCESS", Start: start, End: end, Devices: []templateDeploymentDeviceStatus{{DeviceID: "device", Identifier: "device", Type: "MANAGED_DEVICE_UUID", Status: "SUCCESS", Start: start, End: end}}}
			switch mode {
			case "rfc3339":
				got.Start = submitted.Format(time.RFC3339Nano)
				got.End = now.Format(time.RFC3339Nano)
				got.Devices[0].Start = got.Start
				got.Devices[0].End = got.End
			case "init":
				got.Status = "INIT"
				got.Devices = nil
			case "running":
				got.Status = "IN_PROGRESS"
			case "device-running":
				got.Devices[0].Status = "IN_PROGRESS"
			case "failure":
				got.Status = "FAILURE"
			case "device-failure":
				got.Devices[0].Status = "FAILURE"
			case "unknown":
				got.Status = "UNKNOWN"
			case "wrong-id":
				got.ID = "22222222-2222-4222-8222-222222222222"
			case "wrong-device":
				got.Devices[0].DeviceID = "other"
			case "wrong-identifier":
				got.Devices[0].Identifier = "other"
			case "wrong-type":
				got.Devices[0].Type = "PRE_PROVISIONED_SERIAL"
			case "wrong-project":
				got.Project = "Other"
			case "wrong-template":
				got.Name = "Other"
			case "wrong-version":
				got.Version = "1"
			case "missing-devices":
				got.Devices = nil
			case "duplicate-devices":
				got.Devices = append(got.Devices, got.Devices[0])
			case "missing-start":
				got.Start = ""
			case "old-start":
				got.Start = strconv.FormatInt(submitted.Add(-time.Hour).UnixMilli(), 10)
			case "future-end":
				got.End = strconv.FormatInt(now.Add(time.Hour).UnixMilli(), 10)
			case "reverse-time":
				got.End = strconv.FormatInt(submitted.Add(-time.Second).UnixMilli(), 10)
			case "device-old-start":
				got.Devices[0].Start = strconv.FormatInt(submitted.Add(-time.Hour).UnixMilli(), 10)
			case "missing-status":
				got.Devices[0].Status = ""
			}
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				calls++
				if r.Method != "GET" || r.URL.Path != templateDeployStatusPath+deploymentFixtureID {
					t.Error("unexpected mutation or path")
					w.WriteHeader(500)
					return
				}
				if mode == "unqueryable" {
					w.WriteHeader(404)
					fmt.Fprint(w, "secret-output")
					return
				}
				raw, _ := json.Marshal(got)
				var body map[string]any
				json.Unmarshal(raw, &body)
				body["statusMessage"] = "secret-output"
				body["detailedStatusMessage"] = "secret-output"
				json.NewEncoder(w).Encode(body)
			})
			ok, err := c.templateDeploymentComplete(context.Background(), templateDeploymentIdentity{deploymentID: deploymentFixtureID, deviceID: "device", project: "Lab", name: "Baseline", version: 2}, submitted, now)
			if calls != 1 {
				t.Fatal("status observation retried", calls)
			}
			success := mode == "success" || mode == "rfc3339"
			pending := mode == "init" || mode == "running" || mode == "device-running"
			if ok != success || (err == nil) != (success || pending) {
				t.Fatal(mode, ok, err)
			}
			if err != nil && strings.Contains(err.Error(), "secret-output") {
				t.Fatal("deployment output leaked")
			}
		})
	}
}
