// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"errors"
	"testing"
)

func TestPnPClaimWorkflowReferences(t *testing.T) {
	for _, mode := range []string{"task-only", "task-duplicate", "task-conflict", "task-foreign-pending-image", "task-malformed", "task-empty", "match", "foreign-image", "foreign-template", "missing-image", "missing-template", "missing-workflow", "missing-workflow-id", "system-only", "reset-only", "foreign-template-pending-image", "foreign-image-pending-template", "skip-image", "default-skip-image", "no-template", "not-requested", "foreign-pnp", "foreign-site", "malformed-workflow", "completed-native-state", "failed-native-state"} {
		t.Run(mode, func(t *testing.T) {
			skip := false
			want := pnpDeviceIntent{Name: "edge", State: "PNP", Serial: "FOC12345678", Site: "Global/Lab/Building", PID: "C9300-48P", ImageID: "image-id", ImageSkip: &skip, Template: &pnpTemplateIntent{Name: "Lab#bootstrap"}}
			deps := &pnpClaimDependencies{refs: pnpClaimReferences{deviceID: "pnp-id", siteID: "site-id", imageID: "image-id", templateID: "template-id"}, site: Site{ID: "site-id", NameHierarchy: want.Site}, image: &Image{ID: "image-id", Version: "17.18.04"}, template: &regularTemplateContent{project: templateProject{ID: "project-id"}, base: templateSummary{ID: "template-id", ProjectID: "project-id"}, version: templateVersion{ID: "committed-id", Number: 1}}}
			workflow := &pnpWorkflowIdentity{ID: "workflow-id", ImageID: "image-id", ConfigID: "template-id"}
			snapshot := &pnpClaimInventorySnapshot{pnp: pnpDevice{ID: "pnp-id", Workflow: workflow}, site: deps.site}
			expected := "MatchedReferences"
			var expectedErr error
			switch mode {
			case "task-only", "task-duplicate", "task-conflict", "task-foreign-pending-image", "task-malformed", "task-empty":
				config := "template-id"
				if mode != "task-duplicate" && mode != "task-conflict" {
					workflow.ConfigID = ""
				}
				switch mode {
				case "task-conflict", "task-foreign-pending-image":
					config = "foreign"
					expectedErr = errConfigurationStale
				case "task-malformed":
					config = "../foreign"
					expectedErr = errInvalidResponse
				case "task-empty":
					config = ""
					expected = "PendingTemplateReference"
				}
				if mode == "task-foreign-pending-image" {
					workflow.ImageID = ""
				}
				workflow.Tasks = []pnpWorkflowTaskMetadata{{ConfigInfo: &pnpWorkflowConfigReference{ConfigID: config}}}
				if mode == "task-duplicate" {
					workflow.Tasks = append(workflow.Tasks, workflow.Tasks[0])
				}

			case "foreign-image":
				workflow.ImageID = "foreign"
				expectedErr = errConfigurationStale
			case "foreign-template":
				workflow.ConfigID = "foreign"
				expectedErr = errConfigurationStale
			case "missing-image":
				workflow.ImageID = ""
				expected = "PendingImageReference"
			case "missing-template":
				workflow.ConfigID = ""
				expected = "PendingTemplateReference"
			case "missing-workflow":
				snapshot.pnp.Workflow = nil
				expected = "PendingWorkflow"
			case "missing-workflow-id":
				workflow.ID = ""
				expected = "PendingWorkflow"
			case "system-only":
				snapshot.pnp.SystemWorkflow = workflow
				snapshot.pnp.Workflow = nil
				expected = "PendingWorkflow"
			case "reset-only":
				snapshot.pnp.ResetWorkflow = workflow
				snapshot.pnp.Workflow = nil
				expected = "PendingWorkflow"
			case "foreign-template-pending-image":
				workflow.ImageID = ""
				workflow.ConfigID = "foreign"
				expectedErr = errConfigurationStale
			case "foreign-image-pending-template":
				workflow.ConfigID = ""
				workflow.ImageID = "foreign"
				expectedErr = errConfigurationStale
			case "skip-image":
				skip = true
				workflow.ImageID = "native-unselected"
			case "default-skip-image":
				want.ImageSkip = nil
				workflow.ImageID = "native-unselected"
			case "no-template":
				want.Template = nil
				deps.refs.templateID = ""
				deps.template = nil
				workflow.ConfigID = "native-unselected"
			case "not-requested":
				skip = true
				want.Template = nil
				deps.refs.templateID = ""
				deps.template = nil
				snapshot.pnp.Workflow = nil
				expected = "NotRequested"
			case "foreign-pnp":
				snapshot.pnp.ID = "foreign"
				expectedErr = errConfigurationStale
			case "foreign-site":
				snapshot.site.ID = "foreign"
				expectedErr = errConfigurationStale
			case "malformed-workflow":
				workflow.ID = "bad/id"
				expectedErr = errInvalidResponse
			case "completed-native-state":
				workflow.State = "Completed"
			case "failed-native-state":
				workflow.State = "Failed"
			}
			got, err := observePnPClaimWorkflow(want, deps, snapshot)
			if !errors.Is(err, expectedErr) {
				t.Fatal("unexpected reference validation", err)
			}
			if expectedErr != nil {
				if got != (pnpClaimWorkflowObservation{}) {
					t.Fatal("stale evidence escaped")
				}
				return
			}
			if got.state != expected {
				t.Fatal("wrong reference evidence", got)
			}
			if expected == "MatchedReferences" && got.workflowID != "workflow-id" {
				t.Fatal("missing workflow identity")
			}
		})
	}
}
