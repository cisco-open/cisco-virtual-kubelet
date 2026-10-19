// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestFabricPendingEvents(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "paged", "foreign", "missing-id", "duplicate", "repeat-page", "partial", "missing-response"} {
		t.Run(mode, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != fabricPendingEventsPath || r.URL.Query().Get("fabricId") != "fabric-id" || r.URL.Query().Get("limit") != "500" {
					t.Error("unscoped event request")
					w.WriteHeader(400)
					return
				}
				offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
				if err != nil || offset < 1 {
					t.Error("invalid offset")
					return
				}
				rows := []map[string]any{{"id": "event-id", "fabricId": "fabric-id", "detail": "private-detail"}}
				switch mode {
				case "empty":
					rows = []map[string]any{}
				case "foreign":
					rows[0]["fabricId"] = "foreign-id"
				case "missing-id":
					delete(rows[0], "id")
				case "duplicate":
					rows = append(rows, rows[0])
				case "partial":
					w.WriteHeader(206)
				case "missing-response":
					fmt.Fprint(w, `{}`)
					return
				case "paged", "repeat-page":
					if mode == "repeat-page" {
						offset = 1
					}
					rows = []map[string]any{}
					for i := offset; i < offset+500 && i <= 501; i++ {
						rows = append(rows, map[string]any{"id": fmt.Sprintf("event-%04d", i), "fabricId": "fabric-id"})
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			rows, err := c.fabricPendingEvents(context.Background(), "fabric-id")
			valid := mode == "valid" || mode == "empty" || mode == "paged"
			if (err == nil) != valid {
				t.Fatal("event inventory result", err)
			}
			if !valid {
				return
			}
			want := 1
			if mode == "empty" {
				want = 0
			}
			if mode == "paged" {
				want = 501
			}
			if len(rows) != want {
				t.Fatal("incomplete event inventory", len(rows))
			}
			encoded, _ := json.Marshal(rows)
			if strings.Contains(string(encoded), "private-detail") {
				t.Fatal("native details retained")
			}
			for i := 1; i < len(rows); i++ {
				if rows[i-1].ID >= rows[i].ID {
					t.Fatal("unstable event ordering")
				}
			}
		})
	}
}

func TestFabricPendingEventPayload(t *testing.T) {
	for _, event := range []fabricPendingEvent{{ID: "event-id", FabricID: "fabric-id"}, {}, {ID: "event-id"}, {ID: "bad/id", FabricID: "fabric-id"}} {
		body, err := fabricPendingEventPayload(event)
		valid := event.ID == "event-id" && event.FabricID == "fabric-id"
		if (err == nil) != valid {
			t.Fatal("payload validation", err)
		}
		if !valid {
			continue
		}
		var rows []map[string]string
		if json.Unmarshal(body, &rows) != nil || len(rows) != 1 || len(rows[0]) != 2 || rows[0]["id"] != event.ID || rows[0]["fabricId"] != event.FabricID {
			t.Fatal("wrong native payload")
		}
	}
	if _, err := (&client{}).fabricPendingEvents(context.Background(), "bad/id"); err == nil {
		t.Fatal("invalid scope accepted")
	}
}
