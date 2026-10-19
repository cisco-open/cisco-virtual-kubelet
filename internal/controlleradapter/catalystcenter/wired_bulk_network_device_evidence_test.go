// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

// Original source, native policy and task results use the existing local HTTPS
// service. Independent manager/device responses are explicitly simulated.
func TestWiredBulkDeclaredNetworkWorkerComparison(t *testing.T) {
	exerciseWiredBulkNetworkWorkerComparison(t, false)
}
func TestWiredBulkDeclaredNetworkRecoveryEvidence(t *testing.T) {
	exerciseWiredBulkNetworkWorkerComparison(t, true)
}
func exerciseWiredBulkNetworkWorkerComparison(t *testing.T, recovery bool) {
	for _, mode := range []string{"matching", "different-device-policy", "nonsemantic-output", "mixed-matching", "banner-drift", "banner-incomplete", "banner-redacted", "timezone-drift", "timezone-dst", "dependency-only"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, fixtureMembers, f := wiredBulkJournalFixture(t, false, false)
			source.raw = wiredBulkNetworkSource(t, source)
			mixed := mode != "matching" && mode != "different-device-policy" && mode != "nonsemantic-output" && mode != "dependency-only"
			if mixed {
				var doc map[string]any
				if err := json.Unmarshal(source.raw, &doc); err != nil {
					t.Fatal(err)
				}
				profile := doc["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)["network"].([]any)[0].(map[string]any)
				profile["banner"], profile["timezone"] = "\nAuthorized access\n", "Etc/UTC"
				source.raw, _ = json.Marshal(doc)
			}
			if mode == "dependency-only" {
				source.raw, _ = json.Marshal(wiredBulkDependencyOnlyDocument(t, "combined"))
			}
			config.Spec.Source.Inline.Raw = source.raw
			config.Annotations = map[string]string{wiredBulkAnnotation: "true", wiredBulkRootAnnotation: "Global/Lab"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			f.networkReply = func(w http.ResponseWriter, r *http.Request) bool {
				kind := ""
				for _, candidate := range []string{"ntp", "banner", "timeZone"} {
					if strings.HasSuffix(r.URL.Path, "/"+candidate+"Settings") {
						kind = candidate
					}
				}
				if kind == "" {
					return false
				}
				if r.Method != "GET" {
					t.Error("policy proof attempted a native mutation")
					w.WriteHeader(http.StatusForbidden)
					return true
				}
				var value any
				if strings.Contains(r.URL.Path, "/root/") {
					switch kind {
					case "ntp":
						value = map[string]any{"servers": []string{"192.0.2.1"}}
					case "banner":
						value = map[string]any{"type": "Custom", "message": "\nAuthorized access\n"}
					case "timeZone":
						value = map[string]any{"identifier": "Etc/UTC"}
					}
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{kind: value}}); err != nil {
					t.Error(err)
				}
				return true
			}
			if mode == "dependency-only" {
				installWiredBulkIPAMFixture(t, f)
				previous := f.networkReply
				credentials := newWiredBulkCredentialFixture()
				f.networkReply = func(w http.ResponseWriter, r *http.Request) bool { return credentials.reply(t, w, r) || previous(w, r) }
			}
			batches, err := configuredWiredBulkBatches(config, source, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.prepareOwnedWiredBulkSequence(ctx, config, source, batches, time.Now()); err != nil {
				t.Fatal(err)
			}
			permit, _ := configurationGroupDispatchPermitFixture(t, a, fixtureMembers)
			if _, err := a.dispatchWiredBulkProvision(ctx, config, source, permit); err != nil {
				t.Fatal(err)
			}
			members := wiredBulkReservationMembers(t, a)
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
			if err := a.beginWiredBulkVerification(ctx, config, source, members, authorize, time.Now()); err != nil {
				t.Fatal(err)
			}
			op := journalState(t, a).Operation.DeepCopy()
			now := f.taskEnd.Add(5 * time.Second)
			if recovery {
				installFabricGroupAuthorityFixture(t, a)
				f.taskEnd = op.SubmittedAt.Add(-3 * time.Second)
				now = time.Now().UTC()
			}
			observe := func() error {
				if recovery {
					nc, err := a.currentController(ctx)
					if err != nil {
						return err
					}
					handled, err := (&configReconciler{a: a}).recoverAcknowledgedFabricGroup(ctx, nc, config, source)
					if !handled {
						t.Fatal("public wired recovery not selected")
					}
					if errors.Is(err, errWiredBulkConvergenceUnavailable) && mode != "dependency-only" {
						return nil
					}
					return err
				}
				return a.observeWiredBulkDeclaredNetwork(ctx, config, source, batches[0], op, members, authorize, f.taskEnd, now)
			}
			if mode == "dependency-only" {
				expected := errWiredBulkNetworkContextUnavailable
				if recovery {
					expected = errWiredBulkConvergenceUnavailable
				}
				if err := observe(); !errors.Is(err, expected) {
					t.Fatal("empty scalar context became worker proof", err)
				}
				var leaves ops.DeviceOperationList
				if err := a.statusReader.List(ctx, &leaves); err != nil || len(leaves.Items) != 0 {
					t.Fatal("empty scalar proof created worker requests", err)
				}
				state := journalState(t, a)
				if state.Operation == nil || state.Operation.Phase != "Verifying" || len(state.ActionApplications) != 0 || f.calls != 1 {
					t.Fatal("empty scalar proof lost the operation fence or published a receipt")
				}
				return
			}
			if err := observe(); !errors.Is(err, errWiredBulkDeviceEvidencePending) {
				t.Fatal("worker reads did not remain pending", err)
			}
			if recovery {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				var status configapi.NetworkControllerConfigStatus
				_, valid, reason, _ := (&configReconciler{a: a}).evaluateConfigurationApplySource(ctx, nc, config, &status, source)
				if !valid || reason != "GroupDeviceEvidencePending" || status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
					t.Fatal("missing public worker pending status", reason)
				}
			}
			var leaves ops.DeviceOperationList
			if err := a.statusReader.List(ctx, &leaves); err != nil || len(leaves.Items) != 2 {
				t.Fatal("missing scoped queries", err)
			}
			for i := range leaves.Items {
				leaf := &leaves.Items[i]
				var member *ops.ControllerConfigurationReservation
				for _, candidate := range members {
					if candidate.Spec.Device.Name == leaf.Spec.DeviceRef.Name {
						member = candidate
					}
				}
				if member == nil {
					t.Fatal("read escaped admitted batch")
				}
				stampLANProofEnvelope(leaf, member, now.Add(-2*time.Second), now.Add(-time.Second), now)
				commands := []string{"show version", "show running-config | include ^ntp server"}
				if mixed {
					commands = []string{"show version", "show running-config | section ^banner motd", "show running-config | include ^ntp server", "show running-config all | include ^clock"}
				}
				if !reflect.DeepEqual(leaf.Spec.Operation.Commands, commands) {
					t.Fatal("query plan did not derive from original scoped intent")
				}
				for _, command := range commands {
					output := ""
					switch command {
					case "show version":
						output = "Processor board ID " + member.Spec.PhysicalIdentity + "\n"
					case "show running-config | include ^ntp server":
						output = "ntp server 192.0.2.1\n"
						if i == 0 && mode == "different-device-policy" {
							output = "ntp server 192.0.2.99\n"
						}
						if i == 0 && mode == "nonsemantic-output" {
							output = "COMPLIANT SUCCESS"
						}
					case "show running-config | section ^banner motd":
						output = "banner motd ^C\nAuthorized access\n^C\n"
						if i == 0 && mode == "banner-drift" {
							output = "banner motd ^C\nDifferent policy\n^C\n"
						}
						if i == 0 && mode == "banner-incomplete" {
							output = "banner motd ^C\nAuthorized access\n"
						}
					case "show running-config all | include ^clock":
						output = "clock timezone UTC 0\n"
						if i == 0 && mode == "timezone-drift" {
							output = "clock timezone UTC 1\n"
						}
						if i == 0 && mode == "timezone-dst" {
							output += "clock summer-time SUMMER recurring\n"
						}
					default:
						t.Fatal("unexpected scoped query")
					}
					redacted := i == 0 && mode == "banner-redacted" && command == "show running-config | section ^banner motd"
					leaf.Status.Outputs = append(leaf.Status.Outputs, ops.DeviceOperationOutput{Command: command, Output: output, Redacted: redacted})
				}
				if err := a.statusWriter.Update(ctx, leaf); err != nil {
					t.Fatal(err)
				}
			}
			if err := observe(); (err == nil) != (mode == "matching" || mode == "mixed-matching") {
				t.Fatal("scoped device comparison did not enforce original intent", err)
			}
			if recovery {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				var status configapi.NetworkControllerConfigStatus
				_, valid, reason, _ := (&configReconciler{a: a}).evaluateConfigurationApplySource(ctx, nc, config, &status, source)
				expected := "GroupVerificationPending"
				if mode == "matching" || mode == "mixed-matching" {
					expected = "GroupConvergenceVerificationUnavailable"
				}
				if !valid || reason != expected || status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
					t.Fatal("public scalar evidence released complete-proof fence", reason)
				}
			}
			state := journalState(t, a)
			if state.Operation.Phase != "Verifying" || len(state.ActionApplications) != 0 || f.calls != 1 {
				t.Fatal("scoped comparison published full provisioning convergence")
			}
			if err := a.recordWiredBulkApplication(ctx, config, op.ID, batches[0], members, authorize, nil, now); err == nil {
				t.Fatal("scoped observation replaced missing whole-device proof")
			}
		})
	}
}
