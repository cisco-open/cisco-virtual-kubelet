// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPnPRegistrationRemoval(t *testing.T) {
	for _, mode := range []string{"valid", "dayzero-content", "preview-content", "credential-content", "header-content", "late-content", "empty-content", "duplicate-pnp-serial", "postdelete-audit", "postdelete-state", "wrong-ack-serial", "missing-ack-info", "metadata-audit", "task-history", "error-history", "repeated-history", "missing-proof", "missing-authority", "wrong-resource", "incomplete-inventory", "first-contact", "last-contact", "reload-requested", "inventory-requested", "previous-run", "denied", "late-denied", "late-workflow", "late-contact", "late-managed", "managed-stack-member", "replaced", "parameter-reference", "pending", "lost", "async", "wrong-ack", "replacement-after", "managed-after", "frozen-input"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			want := pnpDeviceIntent{Name: "edge", State: "PNP", Serial: "FOC12345678", Site: "Global/Lab/Building", PID: "C9300-48P"}
			body, _ := pnpRegistrationPayload(want)
			at := metav1.NewTime(time.Now().Add(-time.Second))
			created := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "owner", ConfigUID: "owner-uid", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Resource: fmt.Sprintf("pnp-register:%x", sha256.Sum256(body)), Action: "Create", Phase: "Verified", CreatedResourceID: "created-id", SubmittedAt: &at}
			if mode == "missing-proof" {
				created = nil
			}
			if mode == "wrong-resource" {
				created.Resource = "pnp-register:" + strings.Repeat("b", 64)
			}
			planned, removed := false, false
			writes, guards := 0, 0
			row := cleanPnPRemovalFixture("created-id", "FOC12345678")
			delete(row, "workflow")
			info := row["deviceInfo"].(map[string]any)
			info["hostname"] = "edge"
			info["onbState"] = "Not Contacted"
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "GET" && r.URL.Path == devicesPath {
					rows := []Device{}
					if mode == "incomplete-inventory" {
						rows = append(rows, Device{ID: "managed"})
					}
					if mode == "late-managed" && planned || mode == "managed-after" && removed {
						rows = append(rows, Device{ID: "managed", Serial: "FOC12345678"})
					}
					if mode == "managed-stack-member" {
						rows = append(rows, Device{ID: "managed", Serial: "OTHER,foc12345678"})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
					return
				}
				if r.Method == "GET" && r.URL.Path == pnpDevicesPath {
					switch mode {
					case "dayzero-content":
						row["dayZeroConfig"] = map[string]any{"config": "private-value"}
					case "preview-content":
						row["dayZeroConfigPreview"] = map[string]any{"rendered": "private-value"}
					case "credential-content":
						info["aaaCredentials"] = map[string]any{"username": "private-value"}
					case "header-content":
						info["httpHeaders"] = []any{map[string]any{"value": "private-value"}}
					case "late-content":
						if planned {
							row["dayZeroConfig"] = map[string]any{"config": "private-value"}
						}
					case "empty-content":
						row["dayZeroConfig"] = map[string]any{"config": ""}
						row["dayZeroConfigPreview"] = map[string]any{}
						info["aaaCredentials"] = map[string]any{"username": "", "password": ""}
						info["httpHeaders"] = []any{}
					case "metadata-audit", "task-history", "error-history", "repeated-history":
						audit := map[string]any{"errorFlag": mode == "error-history", "timestamp": 123.0, "details": "private configuration output"}
						if mode == "task-history" {
							audit["historyTaskInfo"] = map[string]any{"name": "private task output"}
						}
						row["runSummaryList"] = []any{audit}
						if mode == "repeated-history" {
							row["runSummaryList"] = []any{audit, audit}
						}
					case "first-contact":
						info["firstContact"] = 123.0
					case "last-contact":
						info["lastContact"] = 123.0
					case "reload-requested":
						info["reloadRequested"] = true
					case "inventory-requested":
						info["populateInventory"] = true
					case "previous-run":
						row["runSummaryList"] = []any{map[string]any{"details": "private configuration output"}}
					}
					if mode == "late-workflow" && planned {
						row["workflow"] = map[string]any{"id": "workflow", "state": "running"}
					}
					if mode == "late-contact" && planned {
						info["onbState"] = "Contacted"
					}
					if mode == "replaced" && planned || mode == "replacement-after" && removed {
						row["id"] = "replacement"
					}
					if mode == "parameter-reference" {
						row["workflowParameters"] = map[string]any{"configList": []any{map[string]any{"configId": "template-id"}}}
					}
					rows := []any{}
					if !removed || mode == "pending" || mode == "replacement-after" {
						rows = append(rows, row)
					}
					if mode == "duplicate-pnp-serial" {
						rows = append(rows, pnpFixture("other", "foc12345678"))
					}
					json.NewEncoder(w).Encode(rows)
					return
				}
				if r.Method != "DELETE" || r.URL.Path != pnpDevicesPath+"/created-id" {
					t.Error("unexpected request", r.Method, r.URL.Path)
					w.WriteHeader(403)
					return
				}
				writes++
				removed = true
				switch mode {
				case "postdelete-audit":
					row["runSummaryList"] = []any{map[string]any{"timestamp": 123.0, "errorFlag": false}, map[string]any{"timestamp": 124.0, "errorFlag": false}}
				case "postdelete-state":
					info["state"] = "Deleted"
				case "wrong-ack-serial":
					info["serialNumber"] = "OTHER"
				case "missing-ack-info":
					delete(row, "deviceInfo")
				case "lost":
					fmt.Fprint(w, `{`)
					return
				case "async":
					w.WriteHeader(202)
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					return
				case "wrong-ack":
					json.NewEncoder(w).Encode(map[string]string{"id": "foreign"})
					return
				}
				json.NewEncoder(w).Encode(row)
			})
			authority := func(context.Context) error {
				guards++
				if mode == "denied" || mode == "late-denied" && planned {
					return errors.New("authority refused")
				}
				return nil
			}
			if mode == "missing-authority" {
				authority = nil
			}
			plan, err := c.planPnPRegistrationRemoval(ctx, want, created, authority)
			if mode == "dayzero-content" || mode == "preview-content" || mode == "credential-content" || mode == "header-content" || mode == "duplicate-pnp-serial" || mode == "missing-authority" || mode == "missing-proof" || mode == "wrong-resource" || mode == "incomplete-inventory" || mode == "denied" || mode == "managed-stack-member" || mode == "parameter-reference" || mode == "first-contact" || mode == "last-contact" || mode == "reload-requested" || mode == "inventory-requested" || mode == "previous-run" || mode == "task-history" || mode == "error-history" || mode == "repeated-history" {
				if err == nil || writes != 0 {
					t.Fatal("unsafe plan admitted", err, writes)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if ok, err := plan.observeAbsent(ctx); ok || err == nil {
				t.Fatal("absence without acknowledged delete accepted")
			}
			planned = true
			if mode == "frozen-input" {
				created.CreatedResourceID = "foreign"
				want.Name = "different"
				want.Serial = "OTHER"
			}
			err = plan.submit(ctx)
			refused := mode == "late-content" || mode == "late-denied" || mode == "late-workflow" || mode == "late-contact" || mode == "late-managed" || mode == "replaced"
			uncertain := mode == "lost" || mode == "async" || mode == "wrong-ack" || mode == "wrong-ack-serial"
			if (err != nil) != (refused || uncertain) {
				t.Fatal("unexpected submission", err)
			}
			if err := plan.submit(ctx); err == nil {
				t.Fatal("replayed removal")
			}
			expected := 1
			if refused {
				expected = 0
			}
			if writes != expected {
				t.Fatal("wrong Delete count", writes)
			}
			done, err := plan.observeAbsent(ctx)
			good := mode == "empty-content" || mode == "missing-ack-info" || mode == "valid" || mode == "metadata-audit" || mode == "postdelete-audit" || mode == "postdelete-state" || mode == "frozen-input"
			if done != good || good && err != nil {
				t.Fatal("invalid absence proof", done, err)
			}
			if guards == 0 {
				t.Fatal("authority not checked")
			}
		})
	}
}

// Eligibility fixtures must not accidentally inherit the inventory reader's
// deliberately secret-bearing fields. Keep that privacy fixture unchanged.
func cleanPnPRemovalFixture(id, serial string) map[string]any {
	row := pnpFixture(id, serial)
	delete(row, "dayZeroConfig")
	info := row["deviceInfo"].(map[string]any)
	delete(info, "aaaCredentials")
	delete(info, "httpHeaders")
	return row
}
