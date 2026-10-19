// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
)

func TestWirelessAnchorDispatchSnapshot(t *testing.T) {
	for _, mode := range []string{"unchanged", "unrelated", "replaced", "peer-edit", "omitted-option", "removed", "create-appeared", "desired-edit", "device-replaced", "device-appeared", "during-resolution", "lost-ack", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			desired, err := parseWirelessAnchors([]byte(wirelessAnchorModel))
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(desired[0])
			var actual wirelessAnchorGroup
			json.Unmarshal(encoded, &actual)
			actual.ID = "anchor-id"
			actual.Peers[0].Priority = "SECONDARY"
			rows := []wirelessAnchorGroup{actual}
			devices := []Device{}
			if mode == "device-replaced" {
				devices = []Device{{ID: "wlc-id", Hostname: "external-wlc", Serial: "serial-1"}}
			}
			if mode == "create-appeared" {
				rows = []wirelessAnchorGroup{}
			}
			var mu sync.Mutex
			writes := 0
			deviceReads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == devicesPath:
					deviceReads++
					if mode == "during-resolution" && deviceReads == 2 {
						rows[0].Peers[0].Priority = "TERTIARY"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": devices})
				case r.URL.Path == wirelessAnchorPath && r.Method == http.MethodGet:
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case r.Method == http.MethodPut || r.Method == http.MethodPost:
					writes++
					if mode == "lost-ack" {
						fmt.Fprint(w, `{"response":{}}`)
						return
					}
					fmt.Fprint(w, `{"response":{"taskId":"anchor-task"}}`)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			})
			e := &wirelessAnchorExecutor{client: c, desired: desired}
			write, err := e.next(ctx)
			if err != nil || write == nil {
				t.Fatal("plan", err)
			}
			mu.Lock()
			switch mode {
			case "replaced":
				rows[0].ID = "different-id"
			case "peer-edit":
				rows[0].Peers[0].Priority = "TERTIARY"
			case "omitted-option":
				v := "192.0.2.201"
				rows[0].Peers[0].PrivateIP = &v
			case "removed":
				rows = []wirelessAnchorGroup{}
			case "create-appeared":
				rows = []wirelessAnchorGroup{actual}
			case "desired-edit":
				e.desired[0].Peers[0].Priority = "TERTIARY"
			case "device-replaced":
				devices[0].ID = "replacement"
			case "device-appeared":
				devices = []Device{{ID: "new-id", Hostname: "external-wlc"}}
			case "unrelated":
				b, _ := json.Marshal(actual)
				var other wirelessAnchorGroup
				json.Unmarshal(b, &other)
				other.Name = "unrelated"
				other.ID = "other-id"
				rows = append(rows, other)
			}
			mu.Unlock()
			allow := mode == "unchanged" || mode == "unrelated" || mode == "concurrent"
			if mode == "concurrent" {
				var wg sync.WaitGroup
				results := make(chan error, 2)
				for range 2 {
					wg.Add(1)
					go func() { defer wg.Done(); _, err := write.submit(ctx); results <- err }()
				}
				wg.Wait()
				close(results)
				successes := 0
				for err := range results {
					if err == nil {
						successes++
					}
				}
				if successes != 1 {
					t.Fatal("expected one dispatch", successes)
				}
			} else {
				_, err = write.submit(ctx)
				if (err == nil) != allow {
					t.Fatal("incorrect dispatch outcome", err)
				}
			}
			if _, err := write.submit(ctx); err == nil {
				t.Fatal("consumed plan replayed")
			}
			mu.Lock()
			count := writes
			mu.Unlock()
			expected := 0
			if allow || mode == "lost-ack" {
				expected = 1
			}
			if count != expected {
				t.Fatal("unexpected mutation count", count, expected)
			}
		})
	}
}
