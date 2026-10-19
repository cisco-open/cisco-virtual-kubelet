// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestConfigurationTaskTreeCompletion(t *testing.T) {
	for _, mode := range []string{"valid", "running", "failed-child", "failed-orphan", "failed-missing-root", "failed-future-end", "duplicate", "orphan", "cycle", "foreign-root", "missing-root", "missing-error", "old-child", "future-end", "partial", "empty", "root-without-root-id"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			row := func(id, parent string) map[string]any {
				return map[string]any{"id": id, "parentId": parent, "rootId": "root", "isError": false, "startTime": now.UnixMilli(), "endTime": now.UnixMilli(), "failureReason": "secret-native-text", "data": "secret-native-data"}
			}
			rows := []map[string]any{row("root", ""), row("child", "root"), row("nested", "child")}
			switch mode {
			case "running":
				rows[1]["endTime"] = 0
			case "failed-child":
				rows[1]["isError"] = true
			case "failed-orphan":
				rows[1]["isError"], rows[1]["errorCode"], rows[1]["parentId"] = true, "NCSP11008", "missing"
			case "failed-missing-root":
				rows[1]["isError"], rows[1]["errorCode"] = true, "NCSP11008"
				rows = rows[1:]
			case "failed-future-end":
				rows[1]["isError"], rows[1]["errorCode"], rows[1]["endTime"] = true, "NCSP11008", now.Add(24*time.Hour).UnixMilli()
			case "duplicate":
				rows = append(rows, rows[1])
			case "orphan":
				rows[1]["parentId"] = "missing"
			case "cycle":
				rows[1]["parentId"] = "nested"
			case "foreign-root":
				rows[1]["rootId"] = "foreign"
			case "missing-root":
				rows = rows[1:]
			case "missing-error":
				delete(rows[1], "isError")
			case "old-child":
				rows[1]["startTime"] = now.Add(-24 * time.Hour).UnixMilli()
			case "future-end":
				rows[1]["endTime"] = now.Add(24 * time.Hour).UnixMilli()
			case "empty":
				rows = nil
			case "root-without-root-id":
				delete(rows[0], "rootId")
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != taskPath+"root/tree" {
					t.Error("unexpected request")
				}
				if mode == "partial" {
					w.WriteHeader(http.StatusPartialContent)
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			done, err := c.configurationTaskTreeComplete(context.Background(), "root", now, now)
			valid := mode == "valid" || mode == "root-without-root-id" || mode == "running"
			if (err == nil) != valid || done != (valid && mode != "running") {
				t.Fatal("unexpected task tree outcome", done, err)
			}
			if err != nil && strings.Contains(err.Error(), "secret-native") {
				t.Fatal("native text leaked")
			}
			if strings.HasPrefix(mode, "failed-") && mode != "failed-child" {
				var failure *configurationTaskFailure
				if errors.As(err, &failure) {
					t.Fatal("uncorrelated or invalid tree classified as this operation's failure")
				}
			}
		})
	}
}

func TestConfigurationTaskTreeCompletionSuppliesLatestChildBoundary(t *testing.T) {
	for _, mode := range []string{"complete", "running", "failed", "foreign", "future-within-skew"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Millisecond)
			rootEnd, latest := now.Add(-3*time.Second), now.Add(-time.Second)
			if mode == "future-within-skew" {
				latest = now.Add(time.Second)
			}
			row := func(id, parent string, end int64) map[string]any {
				return map[string]any{"id": id, "parentId": parent, "rootId": "root", "isError": false, "startTime": now.Add(-5 * time.Second).UnixMilli(), "endTime": end}
			}
			rows := []map[string]any{row("root", "", rootEnd.UnixMilli()), row("nested", "child", now.Add(-2*time.Second).UnixMilli()), row("child", "root", latest.UnixMilli())}
			if mode == "running" {
				rows[1]["endTime"] = 0
			}
			if mode == "failed" {
				rows[1]["isError"] = true
			}
			if mode == "foreign" {
				rows[1]["rootId"] = "other"
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != taskPath+"root/tree" {
					t.Error("unexpected request")
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			completed, err := c.configurationTaskTreeCompletion(context.Background(), "root", now.Add(-5*time.Second), now)
			if mode == "failed" || mode == "foreign" {
				if err == nil || !completed.IsZero() {
					t.Fatal("invalid tree exposed a completion boundary")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "running" {
				if !completed.IsZero() {
					t.Fatal("partial tree exposed a terminal boundary")
				}
				return
			}
			if !completed.Equal(latest) {
				t.Fatal("latest descendant end was discarded", completed)
			}
			evidence := map[string]complianceEvidence{"NETWORK_SETTINGS": {"device", "NETWORK_SETTINGS", "COMPLIANT", "SUCCESS", rootEnd}}
			if err := freshIntentCompliance(evidence, "device", []string{"NETWORK_SETTINGS"}, completed, now); err == nil {
				t.Fatal("root-only report passed a later child completion boundary")
			}
		})
	}
}
