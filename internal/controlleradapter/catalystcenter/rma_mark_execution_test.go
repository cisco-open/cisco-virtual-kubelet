// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

func TestRMAMarkNativePlan(t *testing.T) {
	for _, mode := range []string{"submit", "existing", "late-record", "late-identity", "lost-receipt", "object-receipt", "missing-serial"} {
		t.Run(mode, func(t *testing.T) {
			ref, want, devices, fixtureRecords := rmaBindingFixture()
			records := []deviceReplacementRecord{}
			if mode == "existing" {
				records = fixtureRecords
			}
			if mode == "missing-serial" {
				ref.Serial = ""
			}
			posts := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "POST" && r.URL.Path == deviceReplacementPath {
					posts++
					body, err := io.ReadAll(r.Body)
					if err != nil || string(body) != `[{"faultyDeviceId":"old-id","replacementStatus":"MARKED-FOR-REPLACEMENT"}]` {
						t.Error("invalid native mark payload", string(body), err)
					}
					records = fixtureRecords
					if mode == "lost-receipt" {
						fmt.Fprint(w, `{"response":{}}`)
						return
					}
					if mode == "object-receipt" {
						fmt.Fprint(w, `{"response":{"taskId":{"id":"ambiguous"}}}`)
						return
					}
					fmt.Fprint(w, `{"response":{"taskId":"mark-task"}}`)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected mutation")
					w.WriteHeader(500)
					return
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					rows = devices
				case deviceReplacementPath:
					rows = records
				default:
					t.Error("unexpected endpoint")
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			ctx := context.Background()
			write, err := c.planDeviceReplacementMark(ctx, ref, want)
			if mode == "existing" || mode == "missing-serial" {
				if err == nil || write != nil || posts != 0 {
					t.Fatal("unowned or unbound mark accepted", err)
				}
				return
			}
			if err != nil || write == nil || posts != 0 {
				t.Fatal("invalid planning", err)
			}
			if write.identity.target != "old-id" || write.identity.serial != "old-serial" {
				t.Fatal("lost physical identity")
			}
			if mode == "late-record" {
				records = fixtureRecords
			}
			if mode == "late-identity" {
				devices[0].Serial = "changed"
			}
			receipt, err := write.submit(ctx)
			expectedPosts := 1
			if mode == "late-record" || mode == "late-identity" {
				expectedPosts = 0
			}
			if posts != expectedPosts || (err == nil) != (mode == "submit") {
				t.Fatal("unexpected native effect", posts, err)
			}
			if mode == "submit" && receipt.ID != "mark-task" {
				t.Fatal("lost task")
			}
			if mode == "lost-receipt" || mode == "object-receipt" {
				if next, err := c.planDeviceReplacementMark(ctx, ref, want); err == nil || next != nil || posts != 1 {
					t.Fatal("ambiguous mark adopted/replayed")
				}
			}
		})
	}
}
