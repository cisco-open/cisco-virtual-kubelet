// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"os"
	"testing"
	"time"
)

func qualifyLiveTemplateCommit(t *testing.T, c *client, ctx context.Context, projectID string, settled *bool) {
	t.Helper()
	name := "CVK-Commit-Qualification"
	id := ""
	wait := func(ctx context.Context, task Task, submitted time.Time) error {
		for {
			done, err := c.configurationTaskComplete(ctx, task.ID, submitted, time.Now())
			if err != nil || done {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	submit := func(ctx context.Context, method, path string, payload any) {
		t.Helper()
		*settled = false
		var body []byte
		if payload != nil {
			var err error
			body, err = json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
		}
		submitted := time.Now()
		raw, err := c.mutate(ctx, method, path, body)
		if err != nil {
			t.Fatal(err)
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Fatal("template mutation receipt missing; no replay")
		}
		t.Log("template mutation receipt", method, receipt.Response.ID)
		if err = wait(ctx, receipt.Response, submitted); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if !*settled || id == "" {
			t.Log("retained uncertain template fixture", id)
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		definitions, err := c.listTemplateDefinitions(cleanup)
		if err != nil {
			t.Error(err)
			*settled = false
			return
		}
		current, exists := definitions[projectID+"#"+name]
		if !exists {
			return
		}
		if current.ID != id {
			t.Error("template identity changed")
			*settled = false
			return
		}
		submit(cleanup, http.MethodDelete, templatesPath+"/"+id, nil)
		definitions, err = c.listTemplateDefinitions(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		if _, exists := definitions[projectID+"#"+name]; exists {
			t.Error("template remains")
			return
		}
		*settled = true
		t.Log("template fixture cleanup verified", id)
	})
	projects, err := c.listTemplateProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectName := ""
	for _, p := range projects {
		if p.ID == projectID {
			projectName = p.Name
		}
	}
	if projectName == "" {
		t.Fatal("isolated parent unavailable")
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": map[string]any{"projects": []any{map[string]any{"name": projectName, "dayn_templates": []any{map[string]any{"name": name, "description": "CVK isolated commit qualification", "language": "JINJA", "software_type": "IOS-XE", "device_types": []any{map[string]any{"product_family": "Switches and Hubs"}}, "variables": []any{}}}}}}}})
	for _, content := range []string{"! CVK isolated qualification\n", "! CVK isolated qualification updated\n"} {
		files := map[string]string{name + ".j2": content}
		for step := 0; step < 3; step++ {
			executor, err := c.compileConfigurationExecutor(raw, "templates", files)
			if err != nil {
				t.Fatal(err)
			}
			write, err := executor.next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if step == 2 {
				if write != nil {
					t.Fatal("template did not converge after commit")
				}
				break
			}
			if write == nil || write.identity.parent != projectID {
				t.Fatal("template operation escaped isolated project or completed early")
			}
			staleExecutor, err := c.compileConfigurationExecutor(raw, "templates", files)
			if err != nil {
				t.Fatal(err)
			}
			stale, err := staleExecutor.next(ctx)
			if err != nil || stale == nil {
				t.Fatal("missing independent template plan", err)
			}
			*settled = false
			submitted := time.Now()
			task, err := write.submit(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Log("native template operation", write.action, write.resource, task.ID)
			if err = wait(ctx, task, submitted); err != nil {
				t.Fatal(err)
			}
			// Rebuild between submission and verification, as after a worker restart.
			executor, err = c.compileConfigurationExecutor(raw, "templates", files)
			if err != nil {
				t.Fatal(err)
			}
			op := &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent}
			if ok, err := executor.verify(ctx, op); err != nil || !ok {
				t.Fatal("template operation read-back failed", err)
			}
			definitions, err := c.listTemplateDefinitions(ctx)
			if err != nil {
				t.Fatal(err)
			}
			id = definitions[projectID+"#"+name].ID
			*settled = true
			if _, err := stale.submit(ctx); !errors.Is(err, errConfigurationStale) {
				t.Fatal("old template observation was not refused", err)
			}
			for _, spent := range []*configurationWrite{write, stale} {
				if _, err := spent.submit(ctx); !errors.Is(err, errConfigurationTransition) {
					t.Fatal("template submission replay was not refused", err)
				}
			}
		}
	}
	t.Log("native draft create/update and two committed versions verified", id)
	if os.Getenv("CATC_TEMPLATE_SOFTWARE_VERSION") == "isolated-defaults" {
		qualifyLiveTemplateSoftwareVersions(t, c, ctx, projectName, projectID, id, name, settled)
	}
	if os.Getenv("CATC_TEMPLATE_VARIABLES") == "isolated-metadata" {
		qualifyLiveTemplateVariableMetadata(t, c, ctx, projectName, projectID, id, name, settled)
	}
	if os.Getenv("CATC_TEMPLATE_PARAMETER_TYPES") == "isolated-metadata" {
		qualifyLiveTemplateParameterTypes(t, c, ctx, projectName, projectID, id, name, settled)
	}
	if os.Getenv("CATC_TEMPLATE_BINDINGS") == "isolated-metadata" {
		qualifyLiveTemplateBindingMetadata(t, c, ctx, projectName, projectID, id, name, settled)
	}
	if os.Getenv("CATC_TEMPLATE_DIVIDER_CONTRACT") == "isolated-research" {
		qualifyLiveTemplateDividerContract(t, c, ctx, projectName, projectID, id, name, settled)
	}
	if os.Getenv("CATC_TEMPLATE_JOURNAL_REFERENCES") == "isolated-readback" {
		qualifyLiveTemplateJournalReferences(t, c, ctx, templateSummary{ID: id, Name: name, ProjectID: projectID})
	}
	if os.Getenv("CATC_TEMPLATE_PNP_REFERENCES") == "isolated-unassigned-workflows" {
		qualifyLivePnPTemplateReferences(t, c, ctx, templateSummary{ID: id, Name: name, ProjectID: projectID}, settled)
	}
	if os.Getenv("CATC_TEMPLATE_TAG") == "isolated-association" {
		qualifyLiveTemplateTag(t, c, ctx, projectName, id, name, settled, submit)
	}
	if os.Getenv("CATC_NETWORK_PROFILE_EXECUTION") == "isolated-profile" {
		qualifyLiveNetworkProfile(t, c, ctx, projectName, name, id, settled, submit)
	}
	if os.Getenv("CATC_COMPOSITE_EXECUTION") == "isolated-composite" {
		qualifyLiveCompositeContract(t, c, ctx, projectID, projectName, id, name, settled, submit)
	}
}
