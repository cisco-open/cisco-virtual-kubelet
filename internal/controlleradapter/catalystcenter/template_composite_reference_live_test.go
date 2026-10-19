// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func qualifyLiveCompositeReferences(t *testing.T, c *client, ctx context.Context, projectID, projectName, memberID, memberName, compositeID, compositeName string, settled *bool, submit func(context.Context, string, string, any)) func(context.Context) error {
	t.Helper()
	target := templateSummary{ID: memberID, Name: memberName, ProjectID: projectID}
	before, err := c.stableTemplateCompositeReferences(ctx, target)
	if err != nil || len(before) != 3 {
		*settled = false
		t.Fatal("native draft/two-version reference collection incomplete", err, len(before))
	}
	older := map[templateCompositeReference]bool{}
	for _, ref := range before {
		if ref.TemplateID != compositeID {
			*settled = false
			t.Fatal("foreign consumer of generated member")
		}
		if ref.VersionID != "" {
			older[ref] = true
		}
	}
	if len(older) != 2 {
		*settled = false
		t.Fatal("two committed consumer references missing")
	}
	t.Log("native composite draft and both committed references verified")
	alternateName := "CVK-Reference-Alternative"
	definitions, err := c.listTemplateDefinitions(ctx)
	if err != nil {
		*settled = false
		t.Fatal(err)
	}
	if _, exists := definitions[projectID+"#"+alternateName]; exists {
		*settled = false
		t.Fatal("alternative fixture already exists")
	}
	common := func(name string) map[string]any {
		return map[string]any{"name": name, "language": "JINJA", "software_type": "IOS-XE", "device_types": []any{map[string]any{"product_family": "Switches and Hubs"}}}
	}
	member, composite, alternate := common(memberName), common(compositeName), common(alternateName)
	member["description"] = "CVK isolated commit qualification"
	member["variables"] = []any{}
	composite["composite"] = true
	composite["containing_templates"] = []string{alternateName}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": map[string]any{"projects": []any{map[string]any{"name": projectName, "dayn_templates": []any{member, composite, alternate}}}}}})
	files := map[string]string{memberName + ".j2": "! CVK isolated composite member change\n", alternateName + ".j2": "! CVK isolated alternate member\n"}
	operations := 0
	converged := false
	alternateID := ""
	for step := 0; step < 6; step++ {
		executor, err := c.compileConfigurationExecutor(raw, "templates", files)
		if err != nil {
			*settled = false
			t.Fatal(err)
		}
		write, err := executor.next(ctx)
		if err != nil {
			*settled = false
			t.Fatal(err)
		}
		if write == nil {
			converged = true
			break
		}
		if write.identity.parent != projectID {
			*settled = false
			t.Fatal("reference fixture escaped project")
		}
		*settled = false
		submitted := time.Now()
		task, err := write.submit(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Log("native alternate consumer operation", write.action, write.resource, task.ID)
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
		op := &cvk.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent}
		if ok, err := executor.verify(ctx, op); err != nil || !ok {
			t.Fatal("alternate consumer verification failed", err)
		}
		definitions, err = c.listTemplateDefinitions(ctx)
		if err != nil {
			t.Fatal(err)
		}
		alternateID = definitions[projectID+"#"+alternateName].ID
		*settled = true
		operations++
	}
	if !converged || operations != 4 || !validAPIID(alternateID) {
		*settled = false
		t.Fatal("alternate consumer flow did not converge", operations)
	}
	after, err := c.stableTemplateCompositeReferences(ctx, target)
	if err != nil || len(after) != 2 {
		*settled = false
		t.Fatal("older committed-only references were lost", err, len(after))
	}
	for _, ref := range after {
		if !older[ref] {
			*settled = false
			t.Fatal("unexpected historical consumer identity")
		}
	}
	alternative := templateSummary{ID: alternateID, Name: alternateName, ProjectID: projectID}
	refs, err := c.stableTemplateCompositeReferences(ctx, alternative)
	if err != nil || len(refs) != 2 {
		*settled = false
		t.Fatal("new draft/latest consumer references missing", err, len(refs))
	}
	t.Log("native older versions retain original member references after draft/latest move to alternate member")
	return func(cleanup context.Context) error {
		current, err := c.listTemplateDefinitions(cleanup)
		if err != nil {
			return err
		}
		item, exists := current[projectID+"#"+alternateName]
		if !exists {
			return errConfigurationStale
		}
		if item.ID != alternateID {
			return errConfigurationOwned
		}
		submit(cleanup, http.MethodDelete, templatesPath+"/"+alternateID, nil)
		current, err = c.listTemplateDefinitions(cleanup)
		if err != nil {
			return err
		}
		if _, exists := current[projectID+"#"+alternateName]; exists {
			return errConfigurationStale
		}
		t.Log("alternative member cleanup verified", alternateID)
		return nil
	}
}
