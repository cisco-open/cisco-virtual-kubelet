// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestLiveCatalystCenterExternalAuthenticationAttribute(t *testing.T) {
	if os.Getenv("CATC_EXTERNAL_AUTH_EXECUTION") != "disabled-auth-attribute" {
		t.Skip("disabled-auth attribute qualification not enabled")
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	enabled, err := c.externalAuthentication(ctx)
	if err != nil {
		t.Fatal(err)
	}
	attribute, err := c.externalAAAAttribute(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if enabled || attribute != "" {
		t.Fatal("qualification requires disabled external authentication and no custom attribute")
	}
	present, err := c.hasAuthenticationPolicyServer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Skip("AAA/ISE server registration is required for both flag and custom attribute mutations; baseline observed disabled and empty")
	}
	// Flag mutation requires an authentication policy server, even for a
	// no-op false request on the lab release. Qualify the attribute separately.
	t.Log("external authentication is disabled; flag mutation requires an AAA/ISE server and is not qualified here")
	settled := true
	last := ""
	t.Cleanup(func() {
		if !settled {
			t.Log("retained uncertain custom attribute; no cleanup replay")
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		enabled, err := c.externalAuthentication(cleanup)
		if err != nil || enabled {
			t.Error("cleanup refused changed external authentication", err)
			return
		}
		current, err := c.externalAAAAttribute(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		if current == "" {
			return
		}
		if current != last {
			t.Error("cleanup refused foreign custom attribute")
			return
		}
		raw, err := c.mutateQueryStatus(cleanup, http.MethodDelete, externalAAAAttributePath, nil, nil, 200)
		if err != nil {
			t.Error(err)
			return
		}
		var receipt struct {
			Response struct {
				Message string `json:"message"`
			} `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || receipt.Response.Message == "" {
			t.Error("cleanup receipt unavailable")
			return
		}
		restored, err := c.externalAAAAttribute(cleanup)
		if err != nil || restored != "" {
			t.Error("custom attribute not restored", err)
			return
		}
		enabled, err = c.externalAuthentication(cleanup)
		if err != nil || enabled {
			t.Error("authentication baseline changed", err)
			return
		}
		t.Log("empty custom attribute and disabled authentication baseline restored")
	})
	for _, name := range []string{"CVK-Qualification-Role", "CVK-Qualification-Role-Updated"} {
		raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"system_settings": map[string]any{"external_authentication": map[string]any{"enabled": false, "aaa_attribute": name}}}})
		executor, err := c.compileConfigurationExecutor(raw, "system_settings")
		if err != nil {
			t.Fatal(err)
		}
		write, err := executor.next(ctx)
		if err != nil || write == nil || write.submitCompleted == nil || write.submit != nil {
			t.Fatal("invalid synchronous plan", err)
		}
		settled = false
		if err = write.submitCompleted(ctx); err != nil {
			t.Fatal(err)
		}
		executor, err = c.compileConfigurationExecutor(raw, "system_settings")
		if err != nil {
			t.Fatal(err)
		}
		op := &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent}
		if ok, err := executor.verify(ctx, op); err != nil || !ok {
			t.Fatal("custom attribute readback failed", err)
		}
		last = name
		settled = true
		if next, err := executor.next(ctx); err != nil || next != nil {
			t.Fatal("custom attribute did not converge", err)
		}
		plans, err := c.compileConfigReports(raw, []string{"system_settings"})
		if err != nil {
			t.Fatal(err)
		}
		if drift, err := plans[0].observe(ctx); err != nil || len(drift) != 0 {
			t.Fatal("unexpected system settings drift", err)
		}
		t.Log("custom attribute change, reconstructed executor verification and zero drift passed")
	}
}

func TestLiveCatalystCenterExternalAuthenticationReadOnly(t *testing.T) {
	if os.Getenv("CATC_EXTERNAL_AUTH_EXECUTION") != "disabled-auth-attribute" {
		t.Skip("lab system-settings observation not enabled")
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	enabled, err := c.externalAuthentication(ctx)
	if err != nil {
		t.Fatal(err)
	}
	attribute, err := c.externalAAAAttribute(ctx)
	if err != nil {
		t.Fatal(err)
	}
	present, err := c.hasAuthenticationPolicyServer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"system_settings": map[string]any{"external_authentication": map[string]any{"enabled": enabled}}}})
	plans, err := c.compileConfigReports(raw, []string{"system_settings"})
	if err != nil {
		t.Fatal(err)
	}
	if drift, err := plans[0].observe(ctx); err != nil || len(drift) != 0 {
		t.Fatal("current flag report did not converge", err)
	}
	executor, err := c.compileConfigurationExecutor(raw, "system_settings")
	if err != nil {
		t.Fatal(err)
	}
	if write, err := executor.next(ctx); err != nil || write != nil {
		t.Fatal("no-op planned a mutation", err)
	}
	t.Logf("system settings read/no-op passed: enabled=%t customAttributePresent=%t policyServerPresent=%t", enabled, attribute != "", present)
}
