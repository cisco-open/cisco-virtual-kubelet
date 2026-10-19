// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestLiveCatalystCenterTelemetryPartialProbe(t *testing.T) {
	if os.Getenv("CATC_TELEMETRY_PARTIAL_PROBE") != "isolated-profile" {
		t.Skip("isolated telemetry probe disabled")
	}
	qualifyLiveSiteExecution(t, false)
}

func qualifyLiveTelemetryPartialProbe(t *testing.T, c *client, ctx context.Context, root string, waitTask func(context.Context, Task, time.Time) error, settled *bool) {
	t.Helper()
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target := ""
	for _, site := range sites {
		if site.NameHierarchy == root && site.Type == "area" {
			target = site.ID
		}
	}
	if !validAPIID(target) {
		t.Fatal("generated area missing")
	}
	bindings, err := parseNetworkSettings(liveNetworkSettingsModel(t, root))
	if err != nil {
		t.Fatal(err)
	}
	var seed []byte
	for _, binding := range bindings {
		for _, s := range binding.settings {
			if s.kind == "telemetry" {
				seed, err = networkSettingPayload(s)
			}
		}
	}
	if err != nil || len(seed) == 0 {
		t.Fatal("seed unavailable", err)
	}
	var before networkObservation
	for index, body := range [][]byte{seed, []byte(`{"wiredDataCollection":{"enableWiredDataCollection":true}}`), []byte(`{"syslogs":{"useBuiltinSyslogServer":true}}`)} {
		*settled = false
		started := time.Now()
		raw, err := c.mutate(ctx, http.MethodPut, sitesPath+"/"+target+"/telemetrySettings", body)
		if err != nil {
			t.Fatal("probe mutation outcome unqualified", err)
		}
		var ack struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &ack) != nil || !validAPIID(ack.Response.ID) {
			t.Fatal("original task acknowledgement missing")
		}
		if err := waitTask(ctx, ack.Response, started); err != nil {
			t.Fatal(err)
		}
		after, err := c.readNetworkSetting(ctx, target, "telemetry")
		if err != nil {
			t.Fatal("partial native readback unavailable", err)
		}
		if index > 0 {
			changed := []string{}
			for _, g := range telemetryGroups {
				if !reflect.DeepEqual(before.telemetry[g], after.telemetry[g]) {
					changed = append(changed, g)
				}
			}
			t.Logf("partial telemetry step=%d task=%s changed groups=%v", index, ack.Response.ID, changed)
		}
		before = after
		*settled = true
	}
}
