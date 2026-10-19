// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"
)

type pnpReferenceFixtureIdentity struct {
	ID             string                    `json:"id"`
	Name           string                    `json:"name"`
	ConfigID       string                    `json:"configId"`
	InstanceType   string                    `json:"instanceType"`
	AddToInventory bool                      `json:"addToInventory"`
	Tasks          []pnpReferenceFixtureTask `json:"tasks"`
}

// Compare the declared reference, not response-time defaults such as Pending or
// zero timestamps that the appliance adds between POST and GET.
type pnpReferenceFixtureTask struct {
	Type       string                      `json:"type"`
	Sequence   *int                        `json:"taskSeqNo"`
	ConfigInfo *pnpWorkflowConfigReference `json:"configInfo"`
}

// Only creates/removes unassigned workflow definitions. No PnP device record is
// registered, claimed, reset, deleted or otherwise mutated.
func qualifyLivePnPTemplateReferences(t *testing.T, c *client, ctx context.Context, target templateSummary, settled *bool) {
	t.Helper()
	if os.Getenv("CATC_TEMPLATE_PNP_REFERENCES") != "isolated-unassigned-workflows" {
		return
	}
	version, err := c.latestTemplateVersion(ctx, target.ID)
	if err != nil || version == nil {
		*settled = false
		t.Fatal("version fixture missing", err)
	}
	initial, err := c.pnpWorkflows(ctx)
	if err != nil {
		*settled = false
		t.Fatal(err)
	}
	prior := map[string]bool{}
	for _, row := range initial {
		prior[row.ID] = true
	}
	created := []pnpReferenceFixtureIdentity{}
	check := func(checkCtx context.Context, want int) {
		refs, err := c.stableTemplatePnPReferences(checkCtx, target)
		if err != nil || len(refs) != want {
			*settled = false
			t.Fatal("native PnP reference set incorrect", err, len(refs), want)
		}
		for _, ref := range refs {
			known := false
			for _, owned := range created[:want] {
				known = known || ref.WorkflowID == owned.ID && ref.ConfigID == owned.Tasks[0].ConfigInfo.ConfigID
			}
			if !known || ref.DeviceID != "" || ref.Kind != "definition" {
				*settled = false
				t.Fatal("unexpected device or foreign workflow consumer")
			}
		}
		t.Log("native unassigned workflow reference set verified", want)
	}
	check(ctx, 0)
	t.Cleanup(func() {
		if !*settled {
			t.Log("retained uncertain unassigned workflow fixtures", len(created))
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for index := len(created) - 1; index >= 0; index-- {
			owned := created[index]
			raw, err := c.get(cleanup, pnpWorkflowsPath+"/"+owned.ID, nil, 200)
			if err != nil {
				*settled = false
				t.Error(err)
				return
			}
			var actual pnpReferenceFixtureIdentity
			if json.Unmarshal(raw, &actual) != nil || !reflect.DeepEqual(actual, owned) {
				*settled = false
				t.Error("unassigned workflow identity or declaration changed")
				return
			}
			var usage struct {
				State string `json:"state"`
				Count *int   `json:"usedInDeviceCount"`
				Start *int64 `json:"startTime"`
				End   *int64 `json:"endTime"`
			}
			if json.Unmarshal(raw, &usage) != nil || usage.State != "Pending" || usage.Count == nil || *usage.Count != 0 || usage.Start == nil || *usage.Start != 0 || usage.End == nil || *usage.End != 0 {
				*settled = false
				t.Error("generated workflow is no longer proven unused; retained")
				return
			}
			before, err := c.pnpCollectionCount(cleanup, pnpDevicesPath)
			if err != nil {
				*settled = false
				t.Error(err)
				return
			}
			devices, err := c.pnpDevices(cleanup)
			if err != nil {
				*settled = false
				t.Error(err)
				return
			}
			after, err := c.pnpCollectionCount(cleanup, pnpDevicesPath)
			if err != nil || before != after || len(devices) != before {
				*settled = false
				t.Error("incomplete device-use evidence", err)
				return
			}
			for _, device := range devices {
				used := device.Info.WorkflowID == owned.ID
				for _, workflow := range []*pnpWorkflowIdentity{device.Workflow, device.SystemWorkflow, device.ResetWorkflow} {
					used = used || workflow != nil && workflow.ID == owned.ID
				}
				if used {
					*settled = false
					t.Error("generated definition became assigned; retained")
					return
				}
			}
			*settled = false
			if _, err := c.mutate(cleanup, http.MethodDelete, pnpWorkflowsPath+"/"+owned.ID, nil); err != nil {
				t.Error("workflow deletion uncertain; no replay", err)
				return
			}
			removed := false
			for {
				rows, err := c.pnpWorkflows(cleanup)
				if err != nil {
					t.Error(err)
					return
				}
				found := false
				for _, row := range rows {
					found = found || row.ID == owned.ID
				}
				if !found {
					removed = true
					break
				}
				select {
				case <-cleanup.Done():
					t.Error("workflow absence not verified")
					return
				case <-time.After(time.Second):
				}
			}
			if !removed {
				return
			}
			*settled = true
			check(cleanup, index)
		}
		t.Log("exact unassigned workflow cleanup and reference removal verified; no device writes")
	})
	for index, configID := range []string{target.ID, version.ID} {
		name := "CVK-Q-Reference-" + time.Now().UTC().Format("20060102-150405") + []string{"-base", "-version"}[index]
		payload := map[string]any{"name": name, "type": "Standard", "instanceType": "UserWorkflow", "addToInventory": false, "tasks": []any{map[string]any{"name": "Config Download", "taskSeqNo": 0, "type": "Config", "configInfo": map[string]any{"configId": configID, "saveToStartUp": true, "connLossRollBack": true, "configParameters": []any{}}}}}
		rootConfigID := ""
		if index == 0 {
			// Prove root/task de-duplication, followed by a task-only version
			// reference that the old root-only projection would miss entirely.
			rootConfigID = configID
			payload["configId"] = configID
		}
		body, _ := json.Marshal(payload)
		*settled = false
		raw, err := c.mutate(ctx, http.MethodPost, pnpWorkflowsPath, body)
		if err != nil {
			t.Fatal("workflow creation uncertain; no replay", err)
		}
		var result pnpReferenceFixtureIdentity
		if json.Unmarshal(raw, &result) != nil || !validAPIID(result.ID) || prior[result.ID] {
			t.Fatal("new workflow UUID not proven; no replay")
		}
		created = append(created, result)
		t.Log("generated unassigned workflow UUID", result.ID)
		if result.Name != name || result.ConfigID != rootConfigID || result.InstanceType != "UserWorkflow" || result.AddToInventory {
			t.Fatal("unassigned workflow declaration not preserved")
		}
		if len(result.Tasks) != 1 || result.Tasks[0].ConfigInfo == nil || result.Tasks[0].ConfigInfo.ConfigID != configID {
			t.Fatal("native workflow task reference not preserved")
		}
		*settled = true
		check(ctx, index+1)
	}
}
