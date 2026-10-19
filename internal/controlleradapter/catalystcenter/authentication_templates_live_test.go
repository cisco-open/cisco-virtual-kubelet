// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestLiveCatalystCenterAuthTemplatesReport(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	observed, err := c.listAuthTemplates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(observed) != 3 {
		t.Fatal("expected three built-in global profiles")
	}
	models := []any{}
	for _, p := range observed {
		models = append(models, authTemplateModel(p))
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"authentication_templates": models}})
	plans, err := c.compileConfigReports(raw, []string{"authentication_templates"})
	if err != nil {
		t.Fatal(err)
	}
	drift, err := plans[0].observe(ctx)
	if err != nil || len(drift) != 0 {
		t.Fatalf("round trip err=%v drift=%d", err, len(drift))
	}
	executor, err := c.compileConfigurationExecutor(raw, "authentication_templates")
	if err != nil {
		t.Fatal(err)
	}
	if write, err := executor.next(ctx); err != nil || write != nil {
		t.Fatalf("native executor no-op qualification failed: %v", err)
	}
	profile := models[0].(map[string]any)
	profile["wake_on_lan"] = !profile["wake_on_lan"].(bool)
	raw, _ = json.Marshal(map[string]any{"catalyst_center": map[string]any{"authentication_templates": models}})
	plans, err = c.compileConfigReports(raw, []string{"authentication_templates"})
	if err != nil {
		t.Fatal(err)
	}
	drift, err = plans[0].observe(ctx)
	if err != nil || len(drift) != 1 || drift[0].Path != "/wake_on_lan" {
		t.Fatal("synthetic difference failed")
	}
	t.Logf("PASS: global authentication templates=%d; resolved model round trip and synthetic drift and native executor no-op; no writes", len(observed))
}
