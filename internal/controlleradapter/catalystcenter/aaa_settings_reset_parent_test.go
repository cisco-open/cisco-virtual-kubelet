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
)

func TestOwnedAAAResetParentPreconditions(t *testing.T) {
	for _, mode := range []string{"configured", "missing-parent-policy", "unset-parent-policy", "ancestor-policy", "parent-replaced", "parent-drift", "global-target"} {
		t.Run(mode, func(t *testing.T) {
			config, want, receipt := aaaResetFixture()
			if mode == "ancestor-policy" {
				want.site = "Global/Lab/Building"
				receipt.TargetID = "building"
				receipt.Resource = want.site + "#aaa"
			}
			if mode == "global-target" {
				want.site = "Global"
				receipt.TargetID = "root"
				receipt.Resource = "Global#aaa"
			}
			root, policy := "root", aaaResponse
			if mode == "missing-parent-policy" {
				policy = `{"aaaNetwork":null,"aaaClient":null}`
			}
			if mode == "unset-parent-policy" {
				policy = `{"aaaNetwork":{},"aaaClient":{}}`
			}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == sitesPath:
					sites := sampleSites()
					sites[0].ID = root
					sites[1].ParentID = root
					json.NewEncoder(w).Encode(map[string]any{"response": sites})
				case r.Method == http.MethodGet && r.URL.Path == sitesPath+"/"+root+"/aaaSettings":
					fmt.Fprint(w, `{"response":`+policy+`}`)
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/aaaSettings"):
					value := aaaResponse
					if mode == "ancestor-policy" && r.URL.Path == sitesPath+"/area/aaaSettings" {
						value = `{"aaaNetwork":null,"aaaClient":null}`
					}
					fmt.Fprint(w, `{"response":`+value+`}`)
				case r.Method == http.MethodPut:
					writes++
					fmt.Fprint(w, `{"response":{"taskId":"reset-task"}}`)
				default:
					t.Error("unexpected reset API")
					w.WriteHeader(400)
				}
			})
			write, err := c.planOwnedAAAReset(context.Background(), config, want, receipt, func(context.Context) error { return nil })
			if mode == "missing-parent-policy" || mode == "unset-parent-policy" || mode == "global-target" {
				if err == nil || write != nil || writes != 0 {
					t.Fatal("inheritance lacked configured parent evidence")
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal("configured inheritance blocked", err)
			}
			if mode == "parent-replaced" {
				root = "new-root"
			}
			if mode == "parent-drift" {
				policy = strings.Replace(policy, "192.0.2.10", "192.0.2.11", 1)
			}
			_, err = write.submit(context.Background())
			valid := mode == "configured" || mode == "ancestor-policy"
			if (err == nil) != valid {
				t.Fatal("parent revalidation", err)
			}
			expected := 0
			if valid {
				expected = 1
			}
			if writes != expected {
				t.Fatal("parent replacement/drift was not fenced")
			}
		})
	}
}
