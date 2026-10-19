// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestWirelessDefinitionDispatchSnapshots(t *testing.T) {
	for _, entry := range definitionRecoveryCases {
		if entry.name == "anchor_groups" {
			continue
		} // Anchor has inventory-resolution guards.
		for _, mode := range []string{"stable", "replaced", "missing", "native-edit", "unrelated", "desired-edit", "failed-read", "lost-ack", "concurrent", "create-appeared"} {
			t.Run(entry.name+"/"+mode, func(t *testing.T) {
				ctx := context.Background()
				rows := []map[string]any{}
				writes := 0
				failRead, lostAck := false, false
				var mu sync.Mutex
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					switch {
					case r.URL.Path == authPath:
						fmt.Fprint(w, `{"Token":"test"}`)
					case r.URL.Path == entry.path && r.Method == http.MethodGet:
						if failRead {
							w.WriteHeader(503)
							return
						}
						json.NewEncoder(w).Encode(map[string]any{"response": rows})
					case r.URL.Path == entry.path && r.Method == http.MethodPost || r.URL.Path == entry.path+"/native-id" && r.Method == http.MethodPut:
						writes++
						var body map[string]any
						if json.NewDecoder(r.Body).Decode(&body) != nil {
							t.Error("invalid payload")
							w.WriteHeader(400)
							return
						}
						body["id"] = "native-id"
						rows = []map[string]any{body}
						if lostAck {
							fmt.Fprint(w, `{"response":{}}`)
						} else {
							fmt.Fprint(w, `{"response":{"taskId":"task-id"}}`)
						}
					default:
						t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
						w.WriteHeader(404)
					}
				})
				initial, err := c.compileWirelessConfiguration([]byte(entry.model))
				if err != nil {
					t.Fatal(err)
				}
				seed, err := initial.next(ctx)
				if err != nil || seed == nil {
					t.Fatal("seed plan", err)
				}
				if _, err = seed.submit(ctx); err != nil {
					t.Fatal("seed", err)
				}
				mu.Lock()
				encoded, _ := json.Marshal(rows[0])
				var original map[string]any
				json.Unmarshal(encoded, &original)
				writes = 0
				if mode == "create-appeared" {
					rows = []map[string]any{}
				}
				mu.Unlock()
				model := strings.Replace(entry.model, entry.from, entry.to, 1)
				e, err := c.compileWirelessConfiguration([]byte(model))
				if err != nil {
					t.Fatal(err)
				}
				write, err := e.next(ctx)
				if err != nil || write == nil {
					t.Fatal("plan", err)
				}
				mu.Lock()
				switch mode {
				case "replaced":
					rows[0]["id"] = "other-id"
				case "missing":
					rows = []map[string]any{}
				case "native-edit":
					switch entry.name {
					case "interfaces":
						rows[0]["vlanId"] = 3996
					case "power_profiles":
						rows[0]["description"] = "external description"
					case "dot11be_profiles":
						rows[0]["mloGroup"] = map[string]any{"retained": true}
					case "security_settings":
						rows[0]["walledGardenUrls"] = []string{"external.example"}
					case "rf_profiles":
						rows[0]["radioTypeAProperties"].(map[string]any)["maxRadioClients"] = 150
					}
				case "unrelated":
					data, _ := json.Marshal(original)
					var other map[string]any
					json.Unmarshal(data, &other)
					other["id"] = "unrelated-id"
					field := map[string]string{"interfaces": "interfaceName", "power_profiles": "profileName", "dot11be_profiles": "profileName", "security_settings": "aclListName", "rf_profiles": "rfProfileName"}[entry.name]
					other[field] = "UNRELATED"
					rows = append(rows, other)
				case "desired-edit":
					switch d := e.configurationExecutor.(type) {
					case *wirelessInterfaceExecutor:
						d.desired[0].Name = "CHANGED"
					case *wirelessPowerExecutor:
						d.desired[0].Name = "CHANGED"
					case *wirelessDot11beExecutor:
						d.desired[0].Name = "CHANGED"
					case *wirelessACLExecutor:
						d.desired[0].URLs = []string{} // nil/empty ownership differs.
					case *wirelessRFExecutor:
						setWirelessAPField(d.desired[0].fields, "radioTypeAProperties.maxRadioClients", int64(300))
					default:
						t.Fatal("unexpected executor")
					}
				case "failed-read":
					failRead = true
				case "lost-ack":
					lostAck = true
				case "create-appeared":
					rows = []map[string]any{original}
				}
				mu.Unlock()
				allowed := mode == "stable" || mode == "concurrent" || mode == "unrelated" && entry.name != "rf_profiles"
				if mode == "concurrent" {
					var wg sync.WaitGroup
					results := make(chan error, 2)
					for range 2 {
						wg.Add(1)
						go func() { defer wg.Done(); _, err := write.submit(ctx); results <- err }()
					}
					wg.Wait()
					close(results)
					succeeded := 0
					for err := range results {
						if err == nil {
							succeeded++
						}
					}
					if succeeded != 1 {
						t.Fatal("concurrent dispatch count", succeeded)
					}
				} else {
					_, err = write.submit(ctx)
					if (err == nil) != allowed {
						t.Fatal("unexpected dispatch result", err)
					}
				}
				if _, err = write.submit(ctx); err == nil {
					t.Fatal("consumed closure replayed")
				}
				mu.Lock()
				count := writes
				mu.Unlock()
				expected := 0
				if allowed || mode == "lost-ack" {
					expected = 1
				}
				if count != expected {
					t.Fatal("unexpected mutation attempts", count, expected)
				}
			})
		}
	}
}
