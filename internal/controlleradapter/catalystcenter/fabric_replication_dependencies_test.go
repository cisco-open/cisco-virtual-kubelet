// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFabricReplicationGroupPlanning(t *testing.T) {
	for _, mode := range []string{"valid", "missing-managed", "missing-l3", "changed-pool", "changed-multicast", "unknown-native"} {
		t.Run(mode, func(t *testing.T) {
			var revised atomic.Bool
			var activated atomic.Bool
			var writes atomic.Int32
			a, config, _, members := multicastVNJournalFixture(t, "existing", func(w http.ResponseWriter, r *http.Request) bool {
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				if r.URL.Path == fabricMulticastPath {
					if r.Method == "GET" {
						current := "HEADEND_REPLICATION"
						if activated.Load() {
							current = "NATIVE_MULTICAST"
						}
						reply([]fabricMulticastReplication{{FabricID: "fabric", Mode: current}})
						return true
					}
					var body []fabricMulticastReplication
					if r.Method != "PUT" || json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body[0].FabricID != "fabric" || body[0].Mode != "NATIVE_MULTICAST" {
						t.Error("invalid replication request")
						w.WriteHeader(400)
						return true
					}
					writes.Add(1)
					activated.Store(true)
					reply(map[string]string{"taskId": "group-task"})
					return true
				}
				if r.URL.Path == fabricL3NetworksPath && mode == "missing-l3" {
					reply([]any{})
					return true
				}
				if r.URL.Path == fabricMulticastVNsPath && mode == "unknown-native" {
					w.WriteHeader(404)
					return true
				}
				if revised.Load() && r.URL.Path == sitePoolsPath && mode == "changed-pool" {
					rows := fixtureReservations()
					rows[0].ID = "replacement"
					reply(rows)
					return true
				}
				if revised.Load() && r.URL.Path == fabricMulticastVNsPath && mode == "changed-multicast" {
					row := multicastSiteRow(t, "Corp", "access", "edge-id").fields
					row["ipv4SsmRanges"] = []any{"232.0.0.0/8", "233.0.0.0/8"}
					reply([]map[string]any{row})
					return true
				}
				return false
			})
			ctx := context.Background()
			if mode == "missing-managed" {
				if err := a.statusWriter.Delete(ctx, members[1].device); err != nil {
					t.Fatal(err)
				}
			}
			write, targets, err := a.client.planManagedFabricReplication(ctx, a.statusReader, config.Namespace, "Global/Lab", "NATIVE_MULTICAST")
			invalid := mode == "missing-managed" || mode == "missing-l3" || mode == "unknown-native"
			if invalid {
				if err == nil || write != nil || targets != nil || writes.Load() != 0 {
					t.Fatal("incomplete prerequisites admitted", err)
				}
				return
			}
			if err != nil || write == nil || len(targets) != 2 {
				t.Fatal("managed planning", err, targets)
			}
			if _, err := write.submit(ctx); !errors.Is(err, errConfigurationDeviceAdmission) || writes.Load() != 0 {
				t.Fatal("preparation acquired authority", err)
			}
			before, err := a.client.replicationDependencies(ctx, "Global/Lab")
			if err != nil {
				t.Fatal(err)
			}
			guardCalls := 0
			guarded, err := a.client.planGuardedFabricReplicationGroup(ctx, "Global/Lab", "NATIVE_MULTICAST", func(context.Context) error { guardCalls++; return nil })
			if err != nil || guarded == nil {
				t.Fatal(err)
			}
			revised.Store(true)
			task, err := guarded.submit(ctx)
			if mode != "valid" {
				if !errors.Is(err, errConfigurationStale) || writes.Load() != 0 || guardCalls != 0 {
					t.Fatal("changed prerequisites submitted", err, writes.Load())
				}
				return
			}
			if err != nil || task.ID != "group-task" || writes.Load() != 1 || guardCalls != 1 {
				t.Fatal("guarded submission", err, task)
			}
			if _, err := guarded.submit(ctx); !errors.Is(err, errConfigurationTransition) || writes.Load() != 1 {
				t.Fatal("replayed write", err)
			}
			after, err := a.client.replicationDependencies(ctx, "Global/Lab")
			if err != nil {
				t.Fatal(err)
			}
			if before.digest == after.digest || before.resultDigest != after.resultDigest {
				t.Fatal("mode change altered retained dependencies")
			}
		})
	}
}

func TestFabricReplicationGroupResource(t *testing.T) {
	good := strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
	for _, suffix := range []string{good, "", "pruned", strings.Repeat("a", 64), strings.ToUpper(good), good + ":extra"} {
		resource := "fabric-replication-update:" + suffix
		if !isReservedConfigurationGroupResource("fabric", resource) {
			t.Fatal("group prefix lost")
		}
		if validConfigurationGroupAction("fabric", resource, "Update") != (suffix == good) || validConfigurationGroupAction("fabric", resource, "Create") || validConfigurationGroupAction("inventory", resource, "Update") {
			t.Fatal("invalid replication protocol identity", resource)
		}
	}
}
