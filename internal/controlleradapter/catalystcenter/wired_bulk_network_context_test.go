// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

func wiredBulkNetworkSource(t *testing.T, source *configSourceSnapshot) []byte {
	t.Helper()
	raw := wiredBulkSourceWithSites(t, source, wiredBulkSitesFixture)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	root := doc["catalyst_center"].(map[string]any)
	root["sites"].(map[string]any)["global"] = map[string]any{"network_settings": map[string]any{"network": "base"}}
	root["network_settings"] = map[string]any{"network": []any{map[string]any{"name": "base", "ntp_servers": []any{"192.0.2.1"}}}}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestWiredBulkNetworkContextUsesPinnedModel(t *testing.T) {
	for _, mode := range []string{"inherited", "nearest", "per-kind", "undefined", "bad-profile", "bad-geometry", "missing-context", "unknown-section", "credential-profile"} {
		t.Run(mode, func(t *testing.T) {
			source := &configSourceSnapshot{raw: []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"a","site":"Global/Lab/Building","state":"REPROVISION"},{"name":"b","site":"Global/Lab/Building","state":"REPROVISION"}]}}}`)}
			source.raw = wiredBulkNetworkSource(t, source)
			var doc map[string]any
			if err := json.Unmarshal(source.raw, &doc); err != nil {
				t.Fatal(err)
			}
			root := doc["catalyst_center"].(map[string]any)
			profiles := root["network_settings"].(map[string]any)
			building := root["sites"].(map[string]any)["buildings"].([]any)[0].(map[string]any)
			switch mode {
			case "nearest":
				profiles["network"] = append(profiles["network"].([]any), map[string]any{"name": "child", "ntp_servers": []any{"192.0.2.2"}})
				building["network_settings"] = map[string]any{"network": "child"}
			case "per-kind":
				profiles["network"] = append(profiles["network"].([]any), map[string]any{"name": "child", "domain_name": "example.test", "dns_servers": []any{"192.0.2.53"}})
				building["network_settings"] = map[string]any{"network": "child"}
			case "undefined":
				building["network_settings"] = map[string]any{"network": "missing"}
			case "bad-profile":
				profiles["network"].([]any)[0].(map[string]any)["unknown"] = true
			case "bad-geometry":
				building["unknown"] = true
			case "missing-context":
				delete(root, "network_settings")
			case "unknown-section":
				root["fabric"] = map[string]any{}
			case "credential-profile":
				profiles["device_credentials"] = map[string]any{"unknown": true}
			}
			source.raw, _ = json.Marshal(doc)
			before := bytes.Clone(source.raw)
			model, err := compileWiredBulkSource(source)
			valid := mode == "inherited" || mode == "nearest" || mode == "per-kind"
			if (err == nil) != valid || !bytes.Equal(before, source.raw) {
				t.Fatal("context compiler widened or changed intent", err)
			}
			if !valid {
				return
			}
			batches, err := groupWiredBulkMembers(model.inventory, model.states, model.sites, wiredBulkGrouping{root: "Global/Lab"})
			if err != nil || len(batches) != 1 {
				t.Fatal("context changed source grouping", err)
			}
			expected, err := compileWiredBulkNetworkContext(source, batches[0])
			if err != nil {
				t.Fatal(err)
			}
			server := "192.0.2.1"
			if mode == "nearest" {
				server = "192.0.2.2"
			}
			for _, name := range []string{"a", "b"} {
				settings := expected[name]
				if mode == "per-kind" {
					if len(settings) != 2 || settings[0].kind != "dns" || settings[0].domain != "example.test" || !reflect.DeepEqual(settings[0].servers, []string{"192.0.2.53"}) {
						t.Fatal("child DNS profile changed or lost parent NTP")
					}
					settings = settings[1:]
				}
				if len(settings) != 1 || settings[0].kind != "ntp" || !reflect.DeepEqual(settings[0].servers, []string{server}) {
					t.Fatal("site inheritance did not preserve declared intent")
				}
			}
			for _, format := range []string{"%v", "%+v", "%#v"} {
				if strings.Contains(fmt.Sprintf(format, expected), server) {
					t.Fatal("intent formatter leaked policy")
				}
			}
		})
	}
}

func wiredBulkReservationMembers(t *testing.T, a *adapter) []*ops.ControllerConfigurationReservation {
	t.Helper()
	var list ops.ControllerConfigurationReservationList
	if err := a.statusReader.List(context.Background(), &list); err != nil {
		t.Fatal(err)
	}
	members := make([]*ops.ControllerConfigurationReservation, len(list.Items))
	for i := range list.Items {
		members[i] = &list.Items[i]
	}
	return members
}

// Existing native/managed bulk fixtures with a typed inherited site setting.
// Source policy is deliberately distinct from the current native policy state.
func TestWiredBulkNetworkContextFencesPreparationAndRecovery(t *testing.T) {
	for _, mode := range []string{"matching", "changed-before-prepare", "changed-after-prepare", "changed-during-dispatch", "changed-after-acceptance", "changed-source", "changed-during-device-proof"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, fixtureMembers, f := wiredBulkJournalFixture(t, false, false)
			source.raw = wiredBulkNetworkSource(t, source)
			config.Spec.Source.Inline.Raw = source.raw
			config.Annotations = map[string]string{wiredBulkAnnotation: "true", wiredBulkRootAnnotation: "Global/Lab"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			server := "192.0.2.1"
			f.networkReply = func(w http.ResponseWriter, r *http.Request) bool {
				if !strings.HasSuffix(r.URL.Path, "/ntpSettings") {
					return false
				}
				if r.Method != "GET" || r.URL.Query().Get("_inherited") != "false" {
					t.Error("network context attempted non-read operation")
				}
				if strings.Contains(r.URL.Path, "/root/") {
					fmt.Fprintf(w, `{"response":{"ntp":{"servers":[%q]}}}`, server)
				} else {
					fmt.Fprint(w, `{"response":{"ntp":null}}`)
				}
				return true
			}
			batches, err := configuredWiredBulkBatches(config, source, nil)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "changed-before-prepare" {
				server = "192.0.2.99"
			}
			op, err := a.prepareOwnedWiredBulkSequence(ctx, config, source, batches, time.Now())
			if mode == "changed-before-prepare" {
				if err == nil || op != nil || journalState(t, a).Operation != nil || f.calls != 0 {
					t.Fatal("policy drift admitted a prepared group", err)
				}
				return
			}
			if err != nil || op == nil {
				t.Fatal("matching original policy failed preflight", err)
			}
			if mode == "changed-after-prepare" {
				server = "192.0.2.99"
			}
			if mode == "changed-source" {
				changed := config.DeepCopy()
				changed.Spec.Source.Inline.Raw = []byte(strings.ReplaceAll(string(source.raw), "192.0.2.1", "192.0.2.99"))
				if err := a.statusWriter.Update(ctx, changed); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "changed-during-dispatch" {
				f.beforeInventory = func() {
					if state := journalState(t, a); state.Operation.Phase == "Submitting" {
						server = "192.0.2.99"
					}
				}
			}
			permit, _ := configurationGroupDispatchPermitFixture(t, a, fixtureMembers)
			_, err = a.dispatchWiredBulkProvision(ctx, config, source, permit)
			if mode == "changed-after-prepare" || mode == "changed-source" || mode == "changed-during-dispatch" {
				if err == nil || f.calls != 0 {
					t.Fatal("policy/source drift submitted a native write", err)
				}
				return
			}
			if err != nil || f.calls != 1 {
				t.Fatal("matching policy did not dispatch once", err)
			}
			if mode == "changed-after-acceptance" {
				server = "192.0.2.99"
			}
			current := journalState(t, a).Operation.DeepCopy()
			_, err = a.retainedWiredBulkBatch(ctx, source, current)
			if mode == "changed-after-acceptance" {
				if err == nil || journalState(t, a).Operation.Phase != "Accepted" || f.calls != 1 {
					t.Fatal("recovery silently used newer controller policy", err)
				}
				return
			}
			if err != nil {
				t.Fatal("retained original intent unavailable", err)
			}
			if mode == "matching" {
				return
			}
			// The final source/native intent guard also brackets an independent
			// device proof. Changing desired controller policy cannot publish a receipt.
			members := wiredBulkReservationMembers(t, a)
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
			if err := a.beginWiredBulkVerification(ctx, config, source, members, authorize, time.Now()); err != nil {
				t.Fatal(err)
			}
			proof := func(context.Context, *cvk.NetworkControllerConfigurationOperation, []contract.GroupTargetIdentity, time.Time) error {
				server = "192.0.2.99"
				return nil
			}
			if err := a.recordWiredBulkApplication(ctx, config, op.ID, batches[0], members, authorize, proof, time.Now()); err == nil || len(journalState(t, a).ActionApplications) != 0 {
				t.Fatal("changed policy published convergence", err)
			}
		})
	}
}
