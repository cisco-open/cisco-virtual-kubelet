// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestPnPRemovalProjectionPreservesSharedEvidence(t *testing.T) {
	extended := false
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" || r.URL.Path != pnpDevicesPath {
			t.Error("unexpected API", r.Method, r.URL.Path)
			w.WriteHeader(403)
			return
		}
		row := pnpFixture("device", "FOC12345678")
		if extended {
			row["deviceInfo"].(map[string]any)["firstContact"] = 123.0
			row["runSummaryList"] = []any{map[string]any{"errorFlag": false, "timestamp": 124.0, "details": "private-value", "historyTaskInfo": map[string]any{"command": "private-value", "outputStr": "private-value"}}}
		}
		json.NewEncoder(w).Encode([]any{row})
	})
	ctx := context.Background()
	before, err := c.pnpDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	extended = true
	after, err := c.pnpDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || featureIdentityDigest(before) != featureIdentityDigest(after) {
		t.Fatal("cleanup metadata changed shared claim/reference/recovery evidence")
	}
	rows, err := c.pnpRemovalDevices(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatal("private projection", err)
	}
	if !rows[0].ContentBlocked {
		t.Fatal("attached private content lost through safe paging")
	}
	if !reflect.DeepEqual(rows[0].Device, after[0]) || rows[0].Contact.FirstContact == nil || *rows[0].Contact.FirstContact != 123 || len(rows[0].History) != 1 || rows[0].History[0].HistoryTaskInfo == nil {
		t.Fatal("private reader lost shared identity or cleanup facts")
	}
	raw, err := json.Marshal(rows)
	if err != nil || strings.Contains(string(raw), "private-value") {
		t.Fatal("private projection retained secret/free-form output", err)
	}
}
