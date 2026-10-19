// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type pruningPnPClaimClient struct{ ctrlclient.Client }
type pruningPnPClaimWriter struct{ ctrlclient.SubResourceWriter }

func (c pruningPnPClaimClient) Status() ctrlclient.SubResourceWriter {
	return pruningPnPClaimWriter{c.Client.Status()}
}
func (w pruningPnPClaimWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if nc, ok := obj.(*cvk.NetworkController); ok && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		op := nc.Status.Configuration.Operation
		if isPnPSiteClaimOperation(op.Section, op.Resource) && op.Phase == "Verifying" {
			copy := nc.DeepCopy()
			copy.Status.Configuration.Operation.TargetSerial = ""
			return w.SubResourceWriter.Update(ctx, copy, opts...)
		}
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func TestPnPSiteClaimJournal(t *testing.T) {
	for _, mode := range []string{"observed", "pruned-receipt", "lost", "auth", "nil-admission", "denied-permit", "stack"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			want := pnpDeviceIntent{Name: "edge", State: "PNP", Serial: "FOC12345678", Site: "Global/Lab/Building", PID: "C9300-48P"}
			if mode == "stack" {
				want.Serial += ",FOC87654321"
			}
			raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": []pnpDeviceIntent{want}}}})
			if err != nil {
				t.Fatal(err)
			}
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline.Raw = raw
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			writes, claims, checks := 0, 0, 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "POST" && r.URL.Path == pnpSiteClaimPath {
					writes++
					if journalState(t, a).Operation.Phase != "Submitting" || checks != 1 {
						t.Error("native claim preceded journal/admission")
					}
					if mode == "lost" {
						fmt.Fprint(w, `{`)
						return
					}
					if mode == "auth" {
						w.WriteHeader(401)
						return
					}
					fmt.Fprint(w, `{"response":"acknowledgement"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected mutation")
					w.WriteHeader(400)
					return
				}
				switch r.URL.Path {
				case pnpDevicesPath:
					row := pnpFixture("owned-id", "FOC12345678")
					info := row["deviceInfo"].(map[string]any)
					info["hostname"] = "edge"
					if mode == "stack" {
						info["stack"] = true
						info["stackInfo"] = map[string]any{"stackMemberList": []any{map[string]any{"serialNumber": "FOC87654321", "pid": "C9300-48P"}}}
					}
					json.NewEncoder(w).Encode([]any{row})
				case devicesPath:
					fmt.Fprint(w, `{"response":[]}`)
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
				default:
					t.Error("unexpected API", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			body, err := pnpRegistrationPayload(want)
			if err != nil {
				t.Fatal(err)
			}
			registration := &configurationWrite{resource: fmt.Sprintf("pnp-register:%x", sha256.Sum256(body)), action: "Create", submitCreated: func(context.Context) (string, error) { return "owned-id", nil }}
			if err := a.submitConfigurationWrite(ctx, config, nil, "inventory", registration); err != nil {
				t.Fatal(err)
			}
			id := journalState(t, a).Operation.ID
			if err := a.recordOwnedPnPRegistration(ctx, config, nil, want, id); err != nil {
				t.Fatal(err)
			}
			if err := a.finishConfiguration(ctx, id); err != nil {
				t.Fatal(err)
			}
			admit := configurationAdmissionStep(func(context.Context) (configurationDispatchCheck, error) {
				claims++
				if journalState(t, a).Operation.Phase != "Prepared" {
					t.Error("admission without prepared journal")
				}
				return journalTestPermit(func(context.Context, time.Time) error {
					checks++
					if journalState(t, a).Operation.Phase != "Submitting" {
						t.Error("permit without write-ahead state")
					}
					if mode == "denied-permit" {
						return errConfigurationDeviceAdmission
					}
					return nil
				}), nil
			})
			if mode == "nil-admission" {
				admit = nil
			}
			if mode == "pruned-receipt" {
				a.statusWriter = pruningPnPClaimClient{a.statusWriter}
			}
			err = a.submitOwnedPnPSiteClaim(ctx, config, nil, want, nil, admit)
			if (err == nil) != (mode == "observed") {
				t.Fatal("wrong journal result", err)
			}
			state := journalState(t, a)
			if mode == "nil-admission" || mode == "stack" {
				if writes != 0 || claims != 0 || state.Operation != nil {
					t.Fatal("unadmitted target reached journal/POST")
				}
				return
			}
			phase := "Verifying"
			if mode == "lost" || mode == "auth" {
				phase = "Unknown"
			}
			if mode == "denied-permit" {
				phase = "Submitting"
			}
			op := state.Operation
			serial := "foc12345678"
			if mode == "pruned-receipt" {
				serial = ""
			}
			if op == nil || op.Phase != phase || op.TargetID != "owned-id" || op.TargetSerial != serial || op.ParentID != "building" || op.TaskID != "" || op.SubmittedAt == nil {
				t.Fatalf("wrong persisted claim identity: %#v", op)
			}
			if err := a.finishConfiguration(ctx, op.ID); err == nil {
				t.Fatal("unverified claim released")
			}
			if phase == "Verifying" {
				if err := a.advanceConfiguration(ctx, op.ID, "Verified", ""); err == nil {
					t.Fatal("acknowledgement treated as completion")
				}
			}
			if mode == "pruned-receipt" {
				nc, _ := a.currentController(ctx)
				nc.Status.Configuration.Operation.Phase = "Verified"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				if err := a.finishConfiguration(ctx, op.ID); err == nil {
					t.Fatal("pruned claim released without independent completion")
				}
			}
			// A new worker shares only Kubernetes state, not the native plan.
			restarted := *a
			if err := restarted.submitOwnedPnPSiteClaim(ctx, config, nil, want, nil, admit); err == nil {
				t.Fatal("active claim replayed")
			}
			count := 1
			if mode == "denied-permit" {
				count = 0
			}
			if writes != count || claims != 1 || checks != 1 {
				t.Fatal("claim/permit replay", writes, claims, checks)
			}
		})
	}
}
