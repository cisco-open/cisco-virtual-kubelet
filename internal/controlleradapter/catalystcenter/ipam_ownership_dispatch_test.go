// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestIPAMOwnedDispatchRechecksDurableAuthority(t *testing.T) {
	for _, mode := range []string{"stable", "owner-pruned", "capacity-filled", "source-changed", "task-present", "resource-changed", "paused"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"network_settings"}
			config.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(poolModel)}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			writes := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					writes++
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					return
				}
				var rows any
				switch r.URL.Path {
				case globalPoolsPath:
					rows = []ipPool{}
				case sitePoolsPath:
					rows = []ipReservation{}
				case sitesPath:
					rows = sampleSites()
				default:
					t.Error("unexpected read", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := a.client.compileConfigurationApply(source.raw, config.Spec.ManagedSections, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.bindIPAMOwnership(nc, config, source, &plan); err != nil {
				t.Fatal(err)
			}
			write, err := plan.executor.next(ctx)
			if err != nil || write == nil {
				t.Fatal("plan", err)
			}
			op, err := a.prepareConfiguration(ctx, config, source, plan.section, write.resource, write.action, write.identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.startConfiguration(ctx, config, source, op.ID, write.identity); err != nil {
				t.Fatal(err)
			}
			current, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "owner-pruned":
				current.Status.Configuration.Owners = nil
			case "capacity-filled":
				for range 256 {
					current.Status.Configuration.ActionApplications = append(current.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: "unrelated"})
				}
			case "task-present":
				current.Status.Configuration.Operation.TaskID = "unexpected-task"
			case "resource-changed":
				current.Status.Configuration.Operation.Resource = ipamOwnedResource(ipamPoolIdentity("another"))
			case "source-changed":
				fresh := config.DeepCopy()
				fresh.Generation++
				if err := a.statusWriter.Update(ctx, fresh); err != nil {
					t.Fatal(err)
				}
			case "paused":
				current.Spec.Paused = true
				if err := a.statusWriter.Update(ctx, current); err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(current), current); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.statusWriter.Status().Update(ctx, current); err != nil {
				t.Fatal(err)
			}
			_, err = write.submit(ctx)
			expected := 0
			if mode == "stable" {
				expected = 1
			}
			if writes != expected || (err == nil) != (mode == "stable") {
				t.Fatal("late durable authority change reached mutation", writes, err)
			}
			if _, err := write.submit(ctx); err == nil || writes != expected {
				t.Fatal("consumed submission replayed")
			}
		})
	}
}
