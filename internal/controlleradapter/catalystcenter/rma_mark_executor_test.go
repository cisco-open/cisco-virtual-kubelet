// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRMAMarkExecutorPreparedRecovery(t *testing.T) {
	for _, mode := range []string{"prepared", "Accepted", "Verifying", "Verified", "Unknown", "task", "submitted", "owner", "resource", "target", "serial", "dependency", "native-record", "native-device", "late-source"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline.Raw = []byte(strings.Replace(rmaModel(`{"action":"MARK_FOR_REPLACEMENT"}`), "FOC12345678", "OLD-SERIAL", 1))
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			ref, _, devices, existing := rmaBindingFixture()
			want := deviceReplacementIntent{action: rmaMark}
			snapshot, err := bindDeviceReplacement(ref, want, devices, nil)
			if err != nil {
				t.Fatal(err)
			}
			resource, err := deviceReplacementMarkResource(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.prepareConfiguration(ctx, config, nil, "inventory", resource, "Create", configurationResourceIdentity{target: "old-id", serial: "old-serial"}); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			op := nc.Status.Configuration.Operation
			records := []deviceReplacementRecord{}
			switch mode {
			case "Accepted", "Verifying", "Verified", "Unknown":
				op.Phase = mode
			case "task":
				op.TaskID = "already-submitted"
			case "submitted":
				op.SubmittedAt = &metav1.Time{Time: time.Now().Add(-time.Minute)}
			case "owner":
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			case "resource":
				op.Resource = "rma-mark:" + strings.Repeat("a", 64)
			case "target":
				op.TargetID = "other"
			case "serial":
				op.TargetSerial = "other"
			case "dependency":
				op.DependencyVersions = dependencyJournalIdentity().dependencies
			case "native-record":
				records = existing
			case "native-device":
				devices[0].Serial = "other"
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			changed := false
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("resume submitted native write")
					w.WriteHeader(500)
					return
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					rows = devices
				case deviceReplacementPath:
					if mode == "late-source" && !changed {
						changed = true
						copy := config.DeepCopy()
						copy.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"devices":[]}}}`)
						if err := a.statusWriter.Update(ctx, copy); err != nil {
							t.Error(err)
						}
					}
					rows = records
				default:
					t.Error("unexpected endpoint")
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			executor := &deviceReplacementMarkExecutor{adapter: a, config: config, reference: ref, desired: want}
			next, err := executor.next(ctx)
			if (err == nil) != (mode == "prepared") || (next != nil) != (mode == "prepared") {
				t.Fatal("incorrect retained-operation recovery", next, err)
			}
			if next != nil && (next.resource != resource || next.identity.target != "old-id" || next.identity.serial != "old-serial") {
				t.Fatal("replanned different operation")
			}
			if executor.count() != 1 {
				t.Fatal("wrong action count")
			}
		})
	}
}
