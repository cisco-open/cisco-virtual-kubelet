// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"testing"
)

func TestFabricReplicationLifecycle(t *testing.T) {
	for _, mode := range []string{"configure", "update", "noop", "stale-mode", "replaced-before", "replaced-after", "lost-receipt", "wrong-readback", "missing-readback", "invalid-mode", "missing-fabric", "denied"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			b := false
			fabricID := "fabric"
			rows := []fabricMulticastReplication{}
			if mode == "update" || mode == "stale-mode" {
				rows = append(rows, fabricMulticastReplication{fabricID, "HEADEND_REPLICATION"})
			}
			if mode == "noop" {
				rows = append(rows, fabricMulticastReplication{fabricID, "NATIVE_MULTICAST"})
			}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case sitesPath:
					reply(sampleSites())
				case fabricSitesPath:
					fabrics := []fabricSite{{ID: fabricID, SiteID: "area", Authentication: "No Authentication", PubSub: &b}}
					if mode == "missing-fabric" {
						fabrics = nil
					}
					reply(fabrics)
				case fabricMulticastPath:
					if r.Method == "GET" {
						reply(rows)
						return
					}
					if r.Method != "PUT" {
						t.Error("unexpected mutation verb")
						w.WriteHeader(400)
						return
					}
					writes++
					var body []fabricMulticastReplication
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body[0].FabricID != "fabric" || body[0].Mode != "NATIVE_MULTICAST" {
						t.Error("incorrect payload")
						w.WriteHeader(400)
						return
					}
					rows = body
					if mode == "wrong-readback" {
						rows[0].Mode = "HEADEND_REPLICATION"
					}
					if mode == "missing-readback" {
						rows = []fabricMulticastReplication{}
					}
					if mode == "replaced-after" {
						fabricID = "replacement"
						rows[0].FabricID = fabricID
					}
					if mode == "lost-receipt" {
						reply(map[string]string{})
						return
					}
					reply(map[string]string{"taskId": "task"})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			desired := "NATIVE_MULTICAST"
			if mode == "invalid-mode" {
				desired = "native"
			}
			write, err := c.planGuardedFabricReplication(ctx, "Global/Lab", desired, func(context.Context) error {
				if mode == "denied" {
					return errConfigurationDeviceAdmission
				}
				return nil
			})
			if mode == "invalid-mode" || mode == "missing-fabric" {
				if err == nil || write != nil || writes != 0 {
					t.Fatal("invalid target admitted")
				}
				return
			}
			if mode == "noop" {
				if err != nil || write != nil || writes != 0 {
					t.Fatal("noop wrote", err)
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal("planning", err)
			}
			if mode == "stale-mode" {
				rows[0].Mode = "NATIVE_MULTICAST"
			}
			if mode == "replaced-before" {
				fabricID = "replacement"
			}
			receipt, err := write.submit(ctx)
			if _, again := write.submit(ctx); !errors.Is(again, errConfigurationTransition) {
				t.Fatal("replayed one-use replication write", again)
			}
			if mode == "denied" {
				if !errors.Is(err, errConfigurationDeviceAdmission) || writes != 0 {
					t.Fatal("denied write submitted", err, writes)
				}
				return
			}
			if mode == "stale-mode" || mode == "replaced-before" {
				if err == nil || writes != 0 {
					t.Fatal("stale submission", err, writes)
				}
				return
			}
			if mode == "lost-receipt" {
				if err == nil || writes != 1 {
					t.Fatal("ambiguous write accepted or retried")
				}
				return
			}
			if err != nil || receipt.ID != "task" || writes != 1 {
				t.Fatal("submit", err)
			}
			op := &cvk.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent}
			verified, err := c.verifyFabricReplication(ctx, "Global/Lab", desired, op)
			valid := mode == "configure" || mode == "update"
			if verified != valid || valid && err != nil {
				t.Fatal("readback", verified, err)
			}
			if valid {
				again, err := c.planGuardedFabricReplication(ctx, "Global/Lab", desired, func(context.Context) error {
					if mode == "denied" {
						return errConfigurationDeviceAdmission
					}
					return nil
				})
				if err != nil || again != nil || writes != 1 {
					t.Fatal("replayed converged operation", err)
				}
			}
		})
	}
}

func TestFabricReplicationInventory(t *testing.T) {
	for _, mode := range []string{"paged", "duplicate", "invalid-enum", "missing-id", "missing-mode", "missing-response", "late-error"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != fabricMulticastPath || r.Method != "GET" {
					t.Error("unexpected request")
					w.WriteHeader(400)
					return
				}
				reads++
				if mode == "missing-response" {
					fmt.Fprint(w, `{"version":"1"}`)
					return
				}
				if r.URL.Query().Get("limit") != "500" {
					t.Error("page limit")
				}
				rows := []fabricMulticastReplication{}
				if mode == "paged" || mode == "late-error" {
					if reads == 1 {
						if r.URL.Query().Get("offset") != "1" {
							t.Error("initial offset")
						}
						for i := 0; i < 500; i++ {
							rows = append(rows, fabricMulticastReplication{fmt.Sprintf("fabric-%d", i), "HEADEND_REPLICATION"})
						}
					} else {
						if r.URL.Query().Get("offset") != "501" {
							t.Error("next offset")
						}
						if mode == "late-error" {
							w.WriteHeader(500)
							return
						}
						rows = append(rows, fabricMulticastReplication{"fabric-last", "NATIVE_MULTICAST"})
					}
				} else {
					rows = append(rows, fabricMulticastReplication{"fabric", "NATIVE_MULTICAST"})
					switch mode {
					case "duplicate":
						rows = append(rows, rows[0])
					case "invalid-enum":
						rows[0].Mode = "UNKNOWN"
					case "missing-id":
						rows[0].FabricID = ""
					case "missing-mode":
						rows[0].Mode = ""
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			got, err := c.fabricMulticastReplicationModes(context.Background())
			if mode == "paged" {
				if err != nil || len(got) != 501 || reads != 2 {
					t.Fatal("pagination", err, len(got), reads)
				}
				return
			}
			if err == nil || got != nil {
				t.Fatal("invalid partial inventory returned", err)
			}
		})
	}
}

func TestFabricReplicationRequiresGuard(t *testing.T) {
	if _, err := (&client{}).planGuardedFabricReplication(context.Background(), "Global/Lab", "NATIVE_MULTICAST", nil); !errors.Is(err, errConfigurationDeviceAdmission) {
		t.Fatal("missing guard accepted", err)
	}
}
