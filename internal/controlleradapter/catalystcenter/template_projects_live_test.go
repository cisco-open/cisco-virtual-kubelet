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

func TestLiveCatalystCenterTemplateProject(t *testing.T) {
	if os.Getenv("CATC_PROJECT_EXECUTION") != "isolated-project" {
		t.Skip("isolated project qualification not enabled")
	}
	c := liveClient(t)
	deadline := 3 * time.Minute
	if os.Getenv("CATC_PROFILE_SITE_RECOVERY") == "isolated-public" || os.Getenv("CATC_SWITCHING_PROFILE_RECOVERY") == "isolated-public" {
		deadline = 12 * time.Minute
	}
	if os.Getenv("CATC_TEMPLATE_BINDINGS") == "isolated-metadata" || os.Getenv("CATC_TEMPLATE_DIVIDER_CONTRACT") == "isolated-research" || os.Getenv("CATC_TEMPLATE_PARAMETER_TYPES") == "isolated-metadata" || os.Getenv("CATC_TEMPLATE_VARIABLES") == "isolated-metadata" || os.Getenv("CATC_TEMPLATE_SOFTWARE_VERSION") == "isolated-defaults" || os.Getenv("CATC_TEMPLATE_JOURNAL_REFERENCES") == "isolated-readback" || os.Getenv("CATC_TEMPLATE_PROFILE_REFERENCE_PROBE") == "isolated-readback" || os.Getenv("CATC_TEMPLATE_PROFILE_REFERENCES") == "isolated-lifecycle" || os.Getenv("CATC_TEMPLATE_COMPOSITE_REFERENCES") == "isolated-history" || os.Getenv("CATC_TEMPLATE_PNP_REFERENCES") == "isolated-unassigned-workflows" {
		deadline = 12 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	name := "CVK-Q-Project-" + time.Now().UTC().Format("20060102-150405")
	initial, err := c.listTemplateProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := initial[name]; exists {
		t.Fatal("fixture identity exists")
	}
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
	settled := true
	id := ""
	t.Cleanup(func() {
		if !settled || id == "" {
			t.Log("retained uncertain project", name, id)
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		raw, err := c.get(cleanup, templateProjectsPath+"/"+id, nil, 200)
		if err != nil {
			t.Error(err)
			return
		}
		var detail struct {
			ID        string            `json:"id"`
			Name      string            `json:"name"`
			Templates []json.RawMessage `json:"templates"`
		}
		if json.Unmarshal(raw, &detail) != nil || detail.ID != id || detail.Name != name || len(detail.Templates) != 0 {
			t.Error("cleanup refused changed or populated project")
			return
		}
		// This appliance can omit project.templates even when children exist.
		// Bind absence to the independent authoritative template inventory too.
		definitions, err := c.listTemplateDefinitions(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		for _, definition := range definitions {
			if definition.ProjectID == id {
				t.Error("cleanup refused project with a remaining template")
				return
			}
		}
		submitted := time.Now()
		raw, err = c.mutate(cleanup, http.MethodDelete, templateProjectsPath+"/"+id, nil)
		if err != nil {
			t.Error(err)
			return
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Error("cleanup receipt missing; no replay")
			return
		}
		if err = wait(cleanup, receipt.Response, submitted); err != nil {
			t.Error(err)
			return
		}
		projects, err := c.listTemplateProjects(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		if _, exists := projects[name]; exists {
			t.Error("project remains")
			return
		}
		t.Log("isolated project cleanup verified", id)
	})
	for _, description := range []string{"CVK isolated qualification", "CVK isolated qualification updated"} {
		raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": map[string]any{"projects": []any{map[string]any{"name": name, "description": description}}}}})
		executor, err := c.compileConfigurationExecutor(raw, "templates")
		if err != nil {
			t.Fatal(err)
		}
		write, err := executor.next(ctx)
		if err != nil || write == nil {
			t.Fatal("missing plan", err)
		}
		staleExecutor, err := c.compileConfigurationExecutor(raw, "templates")
		if err != nil {
			t.Fatal(err)
		}
		stale, err := staleExecutor.next(ctx)
		if err != nil || stale == nil {
			t.Fatal("missing second independent plan", err)
		}
		settled = false
		submitted := time.Now()
		task, err := write.submit(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Log("submitted", write.action, name, task.ID)
		if err = wait(ctx, task, submitted); err != nil {
			t.Fatal(err)
		}
		ok, err := executor.verify(ctx, &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target})
		if err != nil || !ok {
			t.Fatal("verification failed", err)
		}
		projects, err := c.listTemplateProjects(ctx)
		if err != nil {
			t.Fatal(err)
		}
		id = projects[name].ID
		settled = true
		if _, err := stale.submit(ctx); !errors.Is(err, errConfigurationStale) {
			t.Fatal("old project observation was not refused", err)
		}
		for _, spent := range []*configurationWrite{write, stale} {
			if _, err := spent.submit(ctx); !errors.Is(err, errConfigurationTransition) {
				t.Fatal("project submission replay was not refused", err)
			}
		}
		if next, err := executor.next(ctx); err != nil || next != nil {
			t.Fatal("did not converge", err)
		}
	}
	t.Log("project create and description update verified")
	if os.Getenv("CATC_TEMPLATE_COMMIT") == "isolated-template" {
		qualifyLiveTemplateCommit(t, c, ctx, id, &settled)
	}
}
