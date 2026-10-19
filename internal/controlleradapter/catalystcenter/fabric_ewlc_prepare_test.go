// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"strings"
	"testing"
	"time"
)

func ewlcJournalFixture(t *testing.T, mode string, post ...func(http.ResponseWriter, *http.Request)) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, []configurationDispatchFixtureMember, *bool) {
	t.Helper()
	a, config, _, members, _ := portChannelJournalFixture(t, "valid")
	revised := false
	c, _ := ewlcComposedFixture(t, "valid", func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "PUT" && len(post) > 0 {
			post[0](w, r)
			return true
		}
		if revised && r.URL.Path == "/dna/intent/api/v1/sda/fabrics/fabric/switchWirelessSetting" {
			json.NewEncoder(w).Encode(map[string]any{"response": []fabricEWLCSetting{{ID: "edge-id", Enabled: true, Rolling: fabricEWLCRolling{Percentage: 15}}}})
			return true
		}
		return false
	})
	a.client = c
	config.Spec.ManagedSections = []string{"inventory"}
	raw := strings.Replace(fabricEdgeIntentModel, `["EDGE_NODE"]`, `["EDGE_NODE","EMBEDDED_WIRELESS_CONTROLLER_NODE"]`, 1)
	if mode != "existing" {
		raw = strings.Replace(raw, `"name":"edge"`, `"name":"edge","enable_rolling_ap_upgrade":true`, 1)
	}
	config.Spec.Source.Inline.Raw = []byte(raw)
	if err := a.statusWriter.Update(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(context.Background(), a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	return a, config, source, members, &revised
}
func TestFabricEWLCPrepare(t *testing.T) {
	for _, mode := range []string{"valid", "existing", "undeclared", "lost-ack", "pruned", "preflight-source", "preflight-owner", "preflight-node", "preflight-group", "preflight-phase", "preflight-settings"} {
		t.Run(mode, func(t *testing.T) {
			nativeMode := mode
			if mode == "preflight-settings" {
				nativeMode = "valid"
			}
			a, config, source, members, revised := ewlcJournalFixture(t, nativeMode)
			ctx := context.Background()
			device := "edge"
			if mode == "undeclared" {
				device = "undeclared"
			}
			if mode == "lost-ack" {
				a.statusWriter = lostJournalAckClient{a.statusWriter}
			}
			if mode == "pruned" {
				a.statusWriter = prunePreparedGroupClient{a.statusWriter}
			}
			op, err := a.prepareFabricEWLC(ctx, config, source, device)
			successful := mode == "valid" || mode == "existing" || strings.HasPrefix(mode, "preflight-")
			if (err == nil) != successful {
				t.Fatal("preparation outcome", err)
			}
			state := journalState(t, a)
			if mode == "existing" || mode == "undeclared" {
				if op != nil || state != nil && state.Operation != nil {
					t.Fatal("unowned compliance or undeclared action journaled")
				}
				return
			}
			if state == nil || state.Operation == nil || state.Operation.Phase != "Prepared" || state.Operation.TaskID != "" || state.Operation.SubmittedAt != nil {
				t.Fatal("missing retained preparation")
			}
			if mode != "pruned" && (contract.ValidateGroupJournalIdentity(state.Operation) != nil || len(state.Operation.PhysicalGroup.Targets) != 2) {
				t.Fatal("incomplete physical footprint")
			}
			var rows ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &rows); err != nil || len(rows.Items) != 0 {
				t.Fatal("preparation created member reservations", err)
			}
			if !successful {
				return
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			checked := nc.Status.Configuration.Operation.DeepCopy()
			switch mode {
			case "preflight-source":
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case "preflight-owner":
				nc.Status.Configuration.Owners = nil
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "preflight-node":
				if err := a.statusWriter.Delete(ctx, members[1].node); err != nil {
					t.Fatal(err)
				}
			case "preflight-group":
				checked.PhysicalGroup.Targets[0].Node.UID = "replacement"
			case "preflight-phase":
				checked.Phase = "Unknown"
			case "preflight-settings":
				*revised = true
			}
			err = a.revalidateFabricEWLCOperation(ctx, config, source, "edge", checked, "Prepared")
			if (err == nil) != (mode == "valid") {
				t.Fatal("preflight outcome", err)
			}
			if mode == "valid" {
				nc.Status.Configuration.Operation.Phase = "Submitting"
				at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
				nc.Status.Configuration.Operation.SubmittedAt = &at
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				if err := a.revalidateFabricEWLCOperation(ctx, config, source, "edge", nc.Status.Configuration.Operation, "Submitting"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := a.prepareFabricEWLC(ctx, config, source, "edge"); err == nil {
				t.Fatal("restarted prepare replaced retained operation")
			}
		})
	}
}
