// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestSiteRetirementTransport(t *testing.T) {
	for _, kind := range []string{"area", "building", "floor"} {
		for _, mode := range []string{"valid", "foreign-endpoint", "body", "target", "parent", "kind", "method", "method-with-body"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				writes := 0
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == authPath {
						fmt.Fprint(w, `{"Token":"test"}`)
						return
					}
					writes++
					if r.Method != http.MethodDelete || r.URL.Path != siteDesignMutationEndpoint(kind)+"/"+siteOwnedTestUUID || r.ContentLength > 0 {
						t.Error("wrong native retirement request")
					}
					fmt.Fprint(w, `{"response":{"taskId":"original-task","url":"https://untrusted.example/task"}}`)
				})
				mutation := &siteMutation{desired: desiredSite{kind: kind}, id: siteOwnedTestUUID, parentID: "root", action: "Delete", method: http.MethodDelete, endpoint: siteDesignMutationEndpoint(kind) + "/" + siteOwnedTestUUID}
				switch mode {
				case "foreign-endpoint":
					mutation.endpoint = "/dna/intent/api/v1/areas/foreign"
				case "body":
					mutation.body = []byte(`{}`)
				case "target":
					mutation.id = "invalid"
				case "parent":
					mutation.parentID = mutation.id
				case "kind":
					mutation.desired.kind = "unrecognized"
				case "method":
					mutation.method = http.MethodPost
				case "method-with-body":
					mutation.method = http.MethodPost
					mutation.body = []byte(`{}`)
				}
				task, err := c.submitSiteMutation(context.Background(), mutation)
				if mode == "valid" {
					if err != nil || task.ID != "original-task" || writes != 1 {
						t.Fatal("valid retirement transport failed", err)
					}
				} else if err == nil || writes != 0 {
					t.Fatal("invalid retirement transport dispatched")
				}
			})
		}
	}
}
