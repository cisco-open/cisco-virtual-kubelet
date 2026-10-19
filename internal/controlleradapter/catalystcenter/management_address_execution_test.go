// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestManagementAddressGuardedExecutionAndNativeVerification(t *testing.T) {
	for _, mode := range []string{"complete", "denied", "changed-serial", "changed-uuid", "changed-address", "collision", "guard-race", "verify-race", "lost-receipt", "unreachable-new-endpoint", "not-converged"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			ref := configurationDeviceReference{Name: "switch", IP: "198.51.100.101"}
			current := Device{ID: "native-switch", Hostname: "switch", Serial: "SERIAL-A", ManagementIP: "198.51.100.100", PlatformID: "C9300"}
			reads, writes, guards := 0, 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case devicesPath:
					reads++
					if reads == 2 {
						switch mode {
						case "changed-serial":
							current.Serial = "SERIAL-B"
						case "changed-uuid":
							current.ID = "replacement"
						case "changed-address":
							current.ManagementIP = "198.51.100.103"
						}
					}
					rows := []Device{current}
					if mode == "collision" && reads > 1 {
						rows = append(rows, Device{ID: "other", Hostname: "other", Serial: "OTHER", ManagementIP: ref.IP})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case devicesPath + "/native-switch/management-address":
					writes++
					var body map[string]string
					if r.Method != http.MethodPut || json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["newIP"] != ref.IP {
						t.Error("wrong management-address payload")
					}
					if mode != "not-converged" {
						current.ManagementIP = ref.IP
					}
					if mode == "lost-receipt" {
						fmt.Fprint(w, `{}`)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"address-task","url":"https://foreign.invalid"}}`)
					}
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
				}
			})
			if write, err := c.planGuardedManagementAddressChange(ctx, ref, nil); err == nil || write != nil {
				t.Fatal("missing admission/endpoint guard accepted")
			}
			write, err := c.planGuardedManagementAddressChange(ctx, ref, func(_ context.Context, target Device, requested string) error {
				guards++
				if target.ID != "native-switch" || requested != ref.IP {
					t.Error("guard lost target/new-address evidence")
				}
				if mode == "denied" {
					return errors.New("not admitted")
				}
				if mode == "guard-race" {
					current.Serial = "REPLACED-DURING-PROOF"
				}
				return nil
			})
			if err != nil || write == nil {
				t.Fatal(err)
			}
			task, err := write.submit(ctx)
			expectsWrite := mode == "complete" || mode == "verify-race" || mode == "lost-receipt" || mode == "unreachable-new-endpoint" || mode == "not-converged"
			if (writes == 1) != expectsWrite || (err == nil) != (expectsWrite && mode != "lost-receipt") {
				t.Fatalf("writes=%d err=%v mode=%s", writes, err, mode)
			}
			if mode == "changed-serial" || mode == "changed-uuid" || mode == "changed-address" || mode == "collision" {
				if guards != 0 {
					t.Fatal("authority requested after native binding changed")
				}
			}
			if _, err := write.submit(ctx); err == nil {
				t.Fatal("used submission closure replayed")
			}
			if !expectsWrite || mode == "lost-receipt" {
				return
			}
			stamp := metav1.Now()
			op := &cvk.NetworkControllerConfigurationOperation{Section: "inventory", Resource: write.resource, Action: "Update", Phase: "Verifying", TargetID: write.identity.target, TargetSerial: write.identity.serial, TaskID: task.ID, SubmittedAt: &stamp}
			if verified, err := c.verifyManagementAddressChange(ctx, ref, op, nil); err == nil || verified {
				t.Fatal("inventory alone authorized completed handoff")
			}
			verified, err := c.verifyManagementAddressChange(ctx, ref, op, func(context.Context, Device, string) error {
				if mode == "unreachable-new-endpoint" {
					return errors.New("new endpoint not proven")
				}
				if mode == "verify-race" {
					current.Serial = "REPLACED-DURING-VERIFICATION"
				}
				return nil
			})
			if verified != (mode == "complete") || (err != nil) != (mode == "unreachable-new-endpoint" || mode == "verify-race") {
				t.Fatalf("verification=%v error=%v mode=%s", verified, err, mode)
			}
			changedRef := ref
			changedRef.IP = "198.51.100.104"
			if verified, err := c.verifyManagementAddressChange(ctx, changedRef, op, func(context.Context, Device, string) error { return nil }); err == nil || verified {
				t.Fatal("changed source selection accepted")
			}
		})
	}
}
