// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Shared qualification of changes to one captured generated template. Uses the
// existing compiler/one-use sender and full native task observer; never deploys.
func qualifyLiveTemplateMetadataTransition(t *testing.T, c *client, ctx context.Context, raw []byte, files map[string]string, target templateSummary, label string, wantWrites int, settled *bool, matches func(map[string]any) bool) {
	t.Helper()
	*settled = false
	before, err := c.latestTemplateVersion(ctx, target.ID)
	if err != nil || before == nil {
		t.Fatal("committed metadata fixture missing", err)
	}
	writes := 0
	converged := false
	for attempt := 0; attempt < 4; attempt++ {
		ex, err := c.compileConfigurationExecutor(raw, "templates", files)
		if err != nil {
			t.Fatal("metadata compile failed", label, err)
		}
		write, err := ex.next(ctx)
		if err != nil {
			t.Fatal("metadata plan failed", label, err)
		}
		if write == nil {
			converged = true
			break
		}
		if write.action != "Update" || write.identity.target != target.ID || write.identity.parent != target.ProjectID {
			t.Fatal("metadata update escaped captured fixture")
		}
		submitted := time.Now()
		task, err := write.submit(ctx)
		if err != nil {
			t.Fatal("metadata submission uncertain; no replay", err)
		}
		t.Log("generated template metadata task", label, task.ID)
		if err := waitLiveTemplateTaskTree(ctx, c, task, submitted); err != nil {
			t.Fatal(err)
		}
		ok, err := ex.verify(ctx, &cvk.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: target.ID, ParentID: target.ProjectID})
		if err != nil || !ok {
			t.Fatal("metadata result unverified", label, err)
		}
		writes++
	}
	if !converged || writes != wantWrites {
		t.Fatal("metadata transition did not converge", label, writes, wantWrites)
	}
	draft, err := c.readTemplateDefinition(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if !matches(draft) {
		t.Fatal("native metadata readback incorrect", label)
	}
	after, err := c.latestTemplateVersion(ctx, target.ID)
	if err != nil || after == nil {
		t.Fatal("version readback missing", err)
	}
	committed, err := c.readCommittedTemplate(ctx, target, *after)
	if err != nil {
		t.Fatal(err)
	}
	if !matches(committed) {
		t.Fatal("committed metadata incorrect", label)
	}
	if writes > 0 && (after.Number <= before.Number || after.ID == before.ID) || writes == 0 && *after != *before {
		t.Fatal("native version progression inconsistent", label)
	}
	*settled = true
}

func waitLiveTemplateTaskTree(ctx context.Context, c *client, task Task, submitted time.Time) error {
	for {
		done, err := c.configurationTaskTreeComplete(ctx, task.ID, submitted, time.Now())
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
