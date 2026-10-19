// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

func TestRMAInventoryRead(t *testing.T) {
	for _, mode := range []string{"empty", "record", "failed-step", "invalid-failed-step", "unknown-status", "paged", "repeated-page", "duplicate-device", "duplicate-serial", "self-replacement", "missing-id", "missing-status", "malformed-serial", "null", "partial", "no-content"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				calls++
				if r.Method != "GET" || r.URL.Path != deviceReplacementPath || r.URL.Query().Get("limit") != "500" {
					t.Error("unexpected request", r.Method, r.URL)
					w.WriteHeader(400)
					return
				}
				if mode == "partial" {
					w.WriteHeader(206)
					fmt.Fprint(w, `{"response":[]}`)
					return
				}
				if mode == "no-content" {
					w.WriteHeader(204)
					return
				}
				if mode == "null" {
					fmt.Fprint(w, `{"response":null}`)
					return
				}
				row := deviceReplacementRecord{ID: "record", FaultyID: "old-id", FaultySerial: "OLD-SERIAL", ReplacementSerial: "NEW-SERIAL", Status: "READY-FOR-REPLACEMENT", WorkflowID: "workflow", CreatedAt: 1}
				rows := []deviceReplacementRecord{row}
				switch mode {
				case "failed-step":
					rows[0].WorkflowFailedStep = "CONFIGURE"
				case "invalid-failed-step":
					rows[0].WorkflowFailedStep = "bad\nstep"
				case "empty":
					rows = []deviceReplacementRecord{}
				case "unknown-status":
					rows[0].Status = "NEW-NATIVE-STATE"
				case "missing-id":
					rows[0].ID = ""
				case "missing-status":
					rows[0].Status = ""
				case "malformed-serial":
					rows[0].FaultySerial = "old,second"
				case "self-replacement":
					rows[0].ReplacementSerial = "old-serial"
				case "duplicate-device", "duplicate-serial":
					other := row
					other.ID = "second-record"
					if mode == "duplicate-serial" {
						other.FaultyID = "second-id"
						other.FaultySerial = "old-serial"
					}
					rows = append(rows, other)
				case "paged", "repeated-page":
					offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
					if err != nil {
						t.Error(err)
					}
					if mode == "repeated-page" {
						offset = 1
					}
					rows = nil
					end := offset + 500
					if end > 502 {
						end = 502
					}
					for i := offset; i < end; i++ {
						rows = append(rows, deviceReplacementRecord{ID: fmt.Sprint("record-", i), FaultyID: fmt.Sprint("device-", i), FaultySerial: fmt.Sprint("SERIAL-", i), Status: "MARKED-FOR-REPLACEMENT"})
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			rows, err := c.deviceReplacements(context.Background())
			valid := mode == "failed-step" || mode == "empty" || mode == "record" || mode == "unknown-status" || mode == "paged"
			if valid != (err == nil) {
				t.Fatal("unexpected reader result", err)
			}
			if mode == "failed-step" && rows[0].WorkflowFailedStep != "CONFIGURE" {
				t.Fatal("workflow failure lost")
			}
			if mode == "paged" && (len(rows) != 501 || calls != 2) {
				t.Fatal("incomplete pagination", len(rows), calls)
			}
			if mode == "record" && (len(rows) != 1 || rows[0].FaultySerial != "OLD-SERIAL" || rows[0].WorkflowID != "workflow") {
				t.Fatal("lost identity")
			}
			if mode == "unknown-status" && rows[0].Status != "NEW-NATIVE-STATE" {
				t.Fatal("invented status semantics")
			}
			if !valid && rows != nil {
				t.Fatal("returned partial inventory")
			}
		})
	}
}
