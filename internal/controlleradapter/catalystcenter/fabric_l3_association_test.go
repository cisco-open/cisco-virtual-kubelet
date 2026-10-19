// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"reflect"
	"testing"
)

func TestFabricL3AssociationPreservesMemberships(t *testing.T) {
	for _, mode := range []string{"attach", "anchored", "already-present", "missing", "stale-before-submit", "replaced-before-submit", "lost-receipt", "unauthorized", "server-error", "lost-membership", "changed-anchor", "replaced-after-submit", "wrong-operation", "missing-memberships", "null-memberships", "empty-memberships"} {
		t.Run(mode, func(t *testing.T) {
			current := fabricL3Network{ID: "network", Name: "Corp", Fabrics: []string{"outside-b", "outside-a"}}
			if mode == "anchored" || mode == "changed-anchor" {
				current.Anchor = "outside-a"
			}
			if mode == "already-present" {
				current.Fabrics = append(current.Fabrics, "target")
			}
			if mode == "empty-memberships" {
				current.Fabrics = []string{}
			}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != fabricL3NetworksPath {
					t.Error("unexpected API")
					w.WriteHeader(404)
					return
				}
				if r.Method == "GET" {
					if mode == "missing-memberships" {
						fmt.Fprint(w, `{"response":[{"id":"network","virtualNetworkName":"Corp"}]}`)
						return
					}
					if mode == "null-memberships" {
						fmt.Fprint(w, `{"response":[{"id":"network","virtualNetworkName":"Corp","fabricIds":null}]}`)
						return
					}
					rows := []fabricL3Network{current}
					if mode == "missing" {
						rows = nil
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
					return
				}
				if r.Method != "PUT" {
					t.Error("wrong mutation verb")
					w.WriteHeader(400)
					return
				}
				writes++
				var body []map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 {
					t.Error("bad payload")
					w.WriteHeader(400)
					return
				}
				want := map[string]any{"id": "network", "virtualNetworkName": "Corp", "fabricIds": []any{"outside-a", "outside-b", "target"}}
				if mode == "empty-memberships" {
					want["fabricIds"] = []any{"target"}
				}
				if current.Anchor != "" {
					want["anchoredSiteId"] = current.Anchor
				}
				if !reflect.DeepEqual(body[0], want) {
					t.Error("changed unrelated membership or anchor", body)
				}
				if mode == "unauthorized" {
					w.WriteHeader(401)
					return
				}
				if mode == "server-error" {
					w.WriteHeader(500)
					return
				}
				current.Fabrics = []string{"target", "outside-b", "outside-a"}
				if mode == "empty-memberships" {
					current.Fabrics = []string{"target"}
				}
				if mode == "lost-membership" {
					current.Fabrics = []string{"target", "outside-a"}
				}
				if mode == "changed-anchor" {
					current.Anchor = "outside-b"
				}
				if mode == "replaced-after-submit" {
					current.ID = "replacement"
				}
				if mode == "lost-receipt" {
					fmt.Fprint(w, `{"response":{}}`)
				} else {
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
				}
			})
			ctx := context.Background()
			write, err := c.planFabricL3Association(ctx, "Corp", "target")
			if mode == "missing" || mode == "missing-memberships" || mode == "null-memberships" {
				if err == nil || write != nil {
					t.Fatal("missing VN treated as ready")
				}
				return
			}
			if mode == "already-present" {
				if err != nil || write != nil || writes != 0 {
					t.Fatal("existing membership replayed")
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal(err)
			}
			if mode == "stale-before-submit" {
				current.Fabrics = append(current.Fabrics, "external-new")
			}
			if mode == "replaced-before-submit" {
				current.ID = "replacement"
			}
			task, err := write.submit(ctx)
			if mode == "stale-before-submit" || mode == "replaced-before-submit" {
				if err == nil || writes != 0 {
					t.Fatal("stale snapshot submitted")
				}
				return
			}
			if mode == "lost-receipt" || mode == "unauthorized" || mode == "server-error" {
				if err == nil || writes != 1 {
					t.Fatal("ambiguous/rejected write accepted or replayed")
				}
				return
			}
			if err != nil || task.ID != "task" || writes != 1 {
				t.Fatal("submission failed", err)
			}
			op := &cvk.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent}
			if mode == "wrong-operation" {
				op.ParentID = "other"
			}
			verified, err := c.verifyFabricL3Association(ctx, "Corp", "target", op)
			valid := mode == "attach" || mode == "anchored" || mode == "empty-memberships"
			if verified != valid || valid && err != nil {
				t.Fatal("incorrect full membership verification", err)
			}
			if valid {
				next, err := c.planFabricL3Association(ctx, "Corp", "target")
				if err != nil || next != nil {
					t.Fatal("no-op failed", err)
				}
			}
		})
	}
}
