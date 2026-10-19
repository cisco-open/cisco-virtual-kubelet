// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestLiveCatalystCenterFabricAuthenticationReadiness(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	fabrics, err := c.fabricSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fabrics) != 0 {
		t.Skip("this read-only qualification requires the known empty fabric baseline")
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := ""
	for _, site := range sites {
		if site.Type == "area" && validSitePath(site.NameHierarchy) && site.NameHierarchy != "Global" {
			path = site.NameHierarchy
			break
		}
	}
	if path == "" {
		t.Fatal("no existing area available for read-only missing-fabric qualification")
	}
	profiles, err := c.listAuthTemplates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var p authProfile
	for _, profile := range profiles {
		if profile.Name == "Closed Authentication" {
			p = profile
		}
	}
	if !validAuthProfile(p) {
		t.Fatal("native global model baseline unavailable")
	}
	model := authTemplateModel(p)
	model["bpdu_guard"] = false
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"fabric": map[string]any{"fabric_sites": []any{map[string]any{"name": path, "pub_sub_enabled": false, "authentication_template": model}}}}})
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	// With no expected payload, this existing trace refuses every PUT before
	// dispatch and refuses every other mutation except token authentication.
	trace := &liveAuthTemplateTrace{client: c, base: base}
	rest.HTTPClient.Transport = trace
	e, err := c.compileFabricConfiguration(raw)
	if err != nil {
		t.Fatal(err)
	}
	gated, ok := e.configurationExecutor.(*fabricAuthConfiguration)
	if !ok {
		t.Fatal("fabric authentication public admission gate missing")
	}
	drift, err := e.report(ctx)
	if err != nil || len(drift) != 2 {
		t.Fatal("missing fabric and override did not report distinct additions", err)
	}
	if w, err := e.next(ctx); w != nil || !errors.Is(err, errConfigurationDeviceAdmission) {
		t.Fatal("public execution bypassed physical admission", err)
	}
	if w, err := c.planFabricAuthentication(ctx, gated.desired, nil); w != nil || !errors.Is(err, errConfigurationDeviceAdmission) {
		t.Fatal("private execution allowed missing authority", err)
	}
	if w, err := c.planFabricAuthentication(ctx, gated.desired, func(context.Context) error { return nil }); w != nil || !errors.Is(err, errConfigurationBusy) {
		t.Fatal("private execution planned without a native fabric", err)
	}
	after, err := c.fabricSites(ctx)
	if err != nil || len(after) != 0 || trace.writes != 0 {
		t.Fatal("read-only readiness changed native fabric state", err)
	}
	t.Log("native global templates", len(profiles), "fabric sites", len(after), "report additions", len(drift), "writes", trace.writes)
	t.Log("generic executor admission refusal and missing-fabric private readiness qualified; no accepted fabric update claimed")
}
