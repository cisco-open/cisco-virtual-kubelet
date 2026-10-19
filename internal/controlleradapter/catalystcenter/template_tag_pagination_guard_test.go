// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// The catalog is not an atomic snapshot. In particular, reading a later page
// must not allow a changed earlier selected row to authorize an update.
func TestTagSubmissionRechecksPaginatedSelection(t *testing.T) {
	for _, mode := range []string{"unchanged", "protection", "replacement", "preserved-description", "later-selected", "create-race", "unrelated"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			falseValue, trueValue := false, true
			old, desired := "old", "desired"
			tags := make([]observedTag, 501)
			for i := range tags {
				tags[i] = observedTag{ID: fmt.Sprintf("tag-%d", i), Name: fmt.Sprintf("tag-%d", i), Description: &old, SystemTag: &falseValue}
			}
			wanted := []desiredTag{{name: "tag-0", payload: map[string]any{"name": "tag-0", "description": desired}}, {name: "tag-1", payload: map[string]any{"name": "tag-1", "description": desired}}}
			if mode == "preserved-description" {
				wanted[0].payload = map[string]any{"name": "tag-0", "dynamicRules": []any{map[string]any{"memberType": "networkdevice", "rules": map[string]any{"operation": "EQ", "name": "hostname", "value": "nonmatching"}}}}
			}
			if mode == "create-race" {
				wanted[0] = desiredTag{name: "new-tag", payload: map[string]any{"name": "new-tag"}}
			}
			passes, writes := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != tagsPath {
					t.Error("unexpected endpoint")
					w.WriteHeader(400)
					return
				}
				if r.Method != http.MethodGet {
					writes++
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					return
				}
				if r.URL.Query().Get("offset") == "1" {
					passes++
					json.NewEncoder(w).Encode(map[string]any{"response": tags[:500]})
					return
				}
				if r.URL.Query().Get("offset") != "501" {
					t.Error("unexpected page offset")
					w.WriteHeader(400)
					return
				}
				// Plan observation is pass one. Change a row whose encoded
				// first-page response has already been returned on pass two.
				if passes == 2 {
					switch mode {
					case "protection":
						tags[0].SystemTag = &trueValue
					case "replacement":
						tags[0].ID = "replacement"
					case "preserved-description":
						tags[0].Description = &desired
					case "later-selected":
						tags[1].SystemTag = &trueValue
					case "create-race":
						tags[2].Name = "new-tag"
					case "unrelated":
						tags[2].SystemTag = &trueValue
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"response": tags[500:]})
			})
			write, err := (&tagExecutor{client: c, desired: wanted}).next(ctx)
			if err != nil || write == nil {
				t.Fatalf("plan: %v", err)
			}
			_, err = write.submit(ctx)
			valid := mode == "unchanged" || mode == "unrelated"
			if (err == nil) != valid {
				t.Fatalf("submission result: %v; writes=%d", err, writes)
			}
			expected := 0
			if valid {
				expected = 1
			}
			if writes != expected {
				t.Fatalf("writes=%d, want %d", writes, expected)
			}
			reads := passes
			if _, err := write.submit(ctx); err == nil || writes != expected || passes != reads {
				t.Fatal("consumed plan was observed or submitted again")
			}
		})
	}
}
