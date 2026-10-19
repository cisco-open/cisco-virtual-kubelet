// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"os"
	"testing"
	"time"
)

func qualifyLiveCompositeContract(t *testing.T, c *client, ctx context.Context, projectID, projectName, memberID, memberName string, settled *bool, submit func(context.Context, string, string, any)) {
	t.Helper()
	name := "CVK-Composite-Qualification"
	id := ""
	var afterCompositeCleanup func(context.Context) error
	memberVersion, err := c.latestTemplateVersion(ctx, memberID)
	if err != nil || memberVersion == nil {
		t.Fatal("member version unavailable", err)
	}
	t.Cleanup(func() {
		if !*settled || id == "" {
			t.Log("retained uncertain composite", id)
			return
		}
		deadline := time.Minute
		if afterCompositeCleanup != nil {
			deadline = 2 * time.Minute
		}
		cleanup, cancel := context.WithTimeout(context.Background(), deadline)
		defer cancel()
		items, err := c.listTemplateDefinitions(cleanup)
		if err != nil {
			t.Error(err)
			*settled = false
			return
		}
		current, exists := items[projectID+"#"+name]
		if !exists {
			return
		}
		if current.ID != id {
			t.Error("composite identity changed")
			*settled = false
			return
		}
		submit(cleanup, http.MethodDelete, templatesPath+"/"+id, nil)
		items, err = c.listTemplateDefinitions(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		if _, exists := items[projectID+"#"+name]; exists {
			t.Error("composite remains")
			return
		}
		if afterCompositeCleanup != nil {
			if err := afterCompositeCleanup(cleanup); err != nil {
				*settled = false
				t.Error("alternative member cleanup failed", err)
				return
			}
		}
		*settled = true
		t.Log("composite cleanup verified", id)
	})
	common := func(templateName string) map[string]any {
		return map[string]any{"name": templateName, "language": "JINJA", "software_type": "IOS-XE", "device_types": []any{map[string]any{"product_family": "Switches and Hubs"}}}
	}
	composite := common(name)
	composite["composite"] = true
	composite["containing_templates"] = []string{memberName}
	member := common(memberName)
	member["description"] = "CVK isolated commit qualification"
	member["variables"] = []any{}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": map[string]any{"projects": []any{map[string]any{"name": projectName, "dayn_templates": []any{composite, member}}}}}})
	for iteration, content := range []string{"! CVK isolated qualification updated\n", "! CVK isolated composite member change\n"} {
		files := map[string]string{memberName + ".j2": content}
		operations := 0
		converged := false
		for step := 0; step < 5; step++ {
			executor, err := c.compileConfigurationExecutor(raw, "templates", files)
			if err != nil {
				t.Fatal(err)
			}
			write, err := executor.next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if write == nil {
				converged = true
				break
			}
			if write.identity.parent != projectID {
				t.Fatal("operation escaped isolated project")
			}
			*settled = false
			submitted := time.Now()
			task, err := write.submit(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Log("native composite dependency operation", write.resource, task.ID)
			for {
				done, err := c.configurationTaskComplete(ctx, task.ID, submitted, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if done {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(time.Second):
				}
			}
			executor, err = c.compileConfigurationExecutor(raw, "templates", files)
			if err != nil {
				t.Fatal(err)
			}
			op := &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent}
			if ok, err := executor.verify(ctx, op); err != nil || !ok {
				t.Fatal("composite dependency verification failed", err)
			}
			definitions, err := c.listTemplateDefinitions(ctx)
			if err != nil {
				t.Fatal(err)
			}
			id = definitions[projectID+"#"+name].ID
			*settled = true
			operations++
		}
		expected := 2
		if iteration == 1 {
			expected = 3
		}
		if !converged || operations != expected {
			t.Fatalf("composite convergence=%v operations=%d expected=%d", converged, operations, expected)
		}
	}
	t.Log("native composite create/commit and member-change recommit verified", id)
	if os.Getenv("CATC_TEMPLATE_COMPOSITE_REFERENCES") == "isolated-history" {
		afterCompositeCleanup = qualifyLiveCompositeReferences(t, c, ctx, projectID, projectName, memberID, memberName, id, name, settled, submit)
	}
}
