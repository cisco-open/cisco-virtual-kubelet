// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestPnPRegistrationReuse(t *testing.T) {
	for _, mode := range []string{"valid", "late-footprint", "later-revision", "absent", "foreign-owner", "missing-owner", "duplicate-owner", "duplicate-receipt", "pruned-id", "foreign-config", "future-generation", "changed-controller", "active-operation", "missing-native", "replaced-native", "changed-native", "late-owner", "late-source"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline.Raw = []byte(pnpIntentFixture())
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			wants, err := parsePnPInventory(config.Spec.Source.Inline.Raw)
			if err != nil {
				t.Fatal(err)
			}
			readMode := ""
			physicalReads := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "GET" && r.URL.Path == devicesPath {
					physicalReads++
					fmt.Fprint(w, `{"response":[]}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != pnpDevicesPath {
					t.Error("unexpected write/API")
					w.WriteHeader(400)
					return
				}
				row := pnpFixture("owned-id", "FOC12345678")
				row["deviceInfo"].(map[string]any)["hostname"] = "edge"
				if readMode == "late-footprint" && physicalReads > 0 {
					row["deviceInfo"].(map[string]any)["state"] = "changed"
				}
				switch readMode {
				case "missing-native":
					fmt.Fprint(w, `[]`)
					return
				case "replaced-native":
					row["id"] = "other-id"
				case "changed-native":
					row["deviceInfo"].(map[string]any)["pid"] = "other-pid"
				case "late-owner":
					nc, _ := a.currentController(ctx)
					nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Error(err)
					}
				case "late-source":
					updated := config.DeepCopy()
					updated.Generation++
					if err := a.statusWriter.Update(ctx, updated); err != nil {
						t.Error(err)
					}
				}
				json.NewEncoder(w).Encode([]any{row})
			})
			body, err := pnpRegistrationPayload(wants[0])
			if err != nil {
				t.Fatal(err)
			}
			write := &configurationWrite{resource: fmt.Sprintf("pnp-register:%x", sha256.Sum256(body)), action: "Create", submitCreated: func(context.Context) (string, error) { return "owned-id", nil }}
			if err := a.submitConfigurationWrite(ctx, config, nil, "inventory", write); err != nil {
				t.Fatal(err)
			}
			op := journalState(t, a).Operation.DeepCopy()
			if err := a.recordOwnedPnPRegistration(ctx, config, nil, wants[0], op.ID); err != nil {
				t.Fatal(err)
			}
			if err := a.finishConfiguration(ctx, op.ID); err != nil {
				t.Fatal(err)
			}
			nc, _ := a.currentController(ctx)
			state := nc.Status.Configuration
			switch mode {
			case "later-revision":
				config.Generation++
				config.Spec.Source.Inline.Raw = []byte(strings.Replace(pnpIntentFixture(), "image.bin", "next-image.bin", 1))
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				wants, err = parsePnPInventory(config.Spec.Source.Inline.Raw)
				if err != nil {
					t.Fatal(err)
				}
			case "absent":
				state.ActionApplications = nil
			case "foreign-owner":
				state.Owners[0].ConfigUID = "foreign"
			case "missing-owner":
				state.Owners = nil
			case "duplicate-owner":
				state.Owners = append(state.Owners, state.Owners[0])
			case "duplicate-receipt":
				state.ActionApplications = append(state.ActionApplications, state.ActionApplications[0])
			case "pruned-id":
				state.ActionApplications[0].Operation.CreatedResourceID = ""
			case "foreign-config":
				state.ActionApplications[0].Operation.ConfigUID = "foreign"
			case "future-generation":
				state.ActionApplications[0].Operation.ConfigGeneration = config.Generation + 1
			case "changed-controller":
				state.ActionApplications[0].Operation.ControllerGeneration = nc.Generation + 1
			case "active-operation":
				state.Operation = &cvk.NetworkControllerConfigurationOperation{ID: op.ID, Phase: "Prepared"}
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			readMode = mode
			id, err := a.resolveOwnedPnPRegistration(ctx, config, nil, wants[0])
			if mode == "valid" || mode == "later-revision" || mode == "late-footprint" {
				if err != nil || id != "owned-id" {
					t.Fatal("owned UUID not reused", err)
				}
				write, id, err := a.planPnPRegistrationStage(ctx, config, nil, wants[0])
				if err != nil || write != nil || id != "owned-id" {
					t.Fatal("reapply planned a new registration", err)
				}
				target, err := a.observeOwnedPnPClaimTarget(ctx, config, nil, wants[0])
				if mode == "late-footprint" {
					if err == nil || target != nil {
						t.Fatal("changed native footprint accepted")
					}
				} else if err != nil || target == nil || target.device.ID != "owned-id" || len(target.serials) != 1 {
					t.Fatal("owned physical target", err)
				}
			} else if mode == "absent" {
				if err != nil || id != "" {
					t.Fatal("unowned record adopted", err)
				}
				write, id, err := a.planPnPRegistrationStage(ctx, config, nil, wants[0])
				if err == nil || write != nil || id != "" {
					t.Fatal("existing unowned native record adopted")
				}
			} else if err == nil || id != "" {
				t.Fatal("invalid ownership reused", mode)
			}
		})
	}
}
