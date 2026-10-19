// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"testing"
	"time"
)

// Native history is real; the journal and worker restart are deliberately
// simulated. No deployment receipt, physical admission or device outcome is
// claimed by this read-only reference qualification.
func qualifyLiveTemplateJournalReferences(t *testing.T, c *client, ctx context.Context, target templateSummary) {
	t.Helper()
	version, err := c.latestTemplateVersion(ctx, target.ID)
	if err != nil || version == nil {
		t.Fatal("native committed fixture unavailable", err)
	}
	a, config := journalFixture(t)
	a.client = c
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	op := templateJournalOperation(time.Now())
	op.ConfigName = config.Name
	op.ConfigUID = string(config.UID)
	op.ConfigGeneration = config.Generation
	op.ControllerGeneration = nc.Generation
	op.Resource = fmt.Sprintf("template-deploy:%s/%s/%d/%064x", target.ID, version.ID, version.Number, 1)
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: op}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"held", "source-deleted", "restarted"} {
		if mode == "source-deleted" {
			if err := a.statusWriter.Delete(ctx, config); err != nil {
				t.Fatal(err)
			}
		}
		if mode == "restarted" {
			copy := *a
			a = &copy
		}
		refs, err := a.retainedTemplateDeploymentReferences(ctx, target)
		if err != nil || len(refs) != 1 || refs[0].BaseID != target.ID || refs[0].VersionID != version.ID || refs[0].DeploymentID != "" || refs[0].Journal != "operation" {
			t.Fatal("native-history reference observation failed", mode, err)
		}
		t.Log("real native history and simulated taskless Unknown journal reference verified", mode)
	}
}
