// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestEmptyFabricTaskObservationGrace(t *testing.T) {
	for _, mode := range []string{"transient-empty", "failed", "foreign-root"} {
		t.Run(mode, func(t *testing.T) {
			reads, writes := 0, 0
			now := time.Now()
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != taskPath+"owned-task/tree" {
					writes++
					t.Error("observation issued an unexpected request")
					w.WriteHeader(400)
					return
				}
				reads++
				rows := []map[string]any{{"id": "owned-task", "startTime": now.UnixMilli(), "endTime": now.UnixMilli(), "isError": false}}
				if mode == "transient-empty" && reads == 1 {
					rows = []map[string]any{}
				}
				if mode == "failed" {
					rows[0]["isError"], rows[0]["errorCode"] = true, "NCSP11008"
				}
				if mode == "foreign-root" {
					rows[0]["id"] = "foreign-task"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			deadline := 3 * time.Second
			if mode == "foreign-root" {
				deadline = 200 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), deadline)
			defer cancel()
			err := waitLiveEmptyFabricAuthTask(ctx, c, Task{ID: "owned-task"}, now)
			if writes != 0 {
				t.Fatal("read-only task observation mutated")
			}
			switch mode {
			case "transient-empty":
				if err != nil || reads != 2 {
					t.Fatal("transient acknowledged tree did not recover", err, reads)
				}
			case "failed":
				var failure *configurationTaskFailure
				if !errors.As(err, &failure) || reads != 1 {
					t.Fatal("known task failure was retried or hidden", err, reads)
				}
			case "foreign-root":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("uncorrelated task accepted", err)
				}
			}
		})
	}
}
