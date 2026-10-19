// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func globalRecoveryFixture(t *testing.T, update bool) (*globalCredentialExecutor, *cvk.NetworkControllerConfigurationOperation) {
	t.Helper()
	want, snapshot := globalCredentialFixture(t, "snmpv3_credentials")
	target := ""
	action := "Create"
	if update {
		target = "owned-snmp"
		action = "Update"
	}
	_, versions, err := want.payload(snapshot, "owner", target)
	if err != nil {
		t.Fatal(err)
	}
	e := &globalCredentialExecutor{owner: "owner", configName: "intent", desired: []globalCredentialIntent{want}, secrets: snapshot}
	op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), Section: "network_settings", Resource: want.resource(), Action: action, Phase: "Unknown", ConfigName: "intent", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, TaskID: "original-task", SubmittedAt: &metav1.Time{Time: time.Now().Add(-time.Second)}, TargetID: target, CredentialVersions: versions}
	if update {
		e.receipts = []cvk.NetworkControllerConfigurationCredentialApplication{{Section: "network_settings", Resource: want.resource(), ConfigUID: "owner", TargetID: target, OperationID: strings.Repeat("b", 32), Versions: versions}}
	}
	return e, op
}
func TestGlobalCredentialKnownTaskSelection(t *testing.T) {
	for _, update := range []bool{false, true} {
		for _, mode := range []string{"valid", "taskless", "undeclared", "foreign-owner", "foreign-config", "wrong-section", "parent", "physical", "dependency", "cancelled", "created-id", "prepared", "ssid-history", "pruned-version", "changed-version", "changed-alias", "changed-secret-key", "duplicate-version", "wrong-target", "missing-prior", "duplicate-prior", "foreign-prior", "pruned-prior-versions", "changed-prior-target", "wrong-phase", "partial-source"} {
			t.Run(fmt.Sprintf("update-%t/%s", update, mode), func(t *testing.T) {
				e, op := globalRecoveryFixture(t, update)
				switch mode {
				case "taskless":
					op.TaskID = ""
				case "undeclared":
					op.Resource += "-foreign"
				case "foreign-owner":
					op.ConfigUID = "foreign"
				case "foreign-config":
					op.ConfigName = "foreign"
				case "wrong-section":
					op.Section = "wireless"
				case "parent":
					op.ParentID = "foreign"
				case "physical":
					op.PhysicalGroup = &cvk.NetworkControllerConfigurationGroupIdentity{}
				case "dependency":
					op.DependencyVersions = &cvk.NetworkControllerConfigurationDependencyVersions{}
				case "cancelled":
					op.CancellationRequestedAt = op.SubmittedAt.DeepCopy()
				case "created-id":
					op.CreatedResourceID = "unexpected"
				case "prepared":
					op.PreparedAt = op.SubmittedAt.DeepCopy()
				case "ssid-history":
					op.SSIDCredentialHistory = []cvk.NetworkControllerConfigurationSSIDCredentialHistory{{Priority: 0, Version: 1}}
				case "pruned-version":
					op.CredentialVersions = nil
				case "changed-version":
					op.CredentialVersions[0].Version++
				case "changed-alias":
					op.CredentialVersions[0].SourceAlias = "foreign"
				case "changed-secret-key":
					op.CredentialVersions[0].SecretKey = "foreign"
				case "duplicate-version":
					op.CredentialVersions = append(op.CredentialVersions, op.CredentialVersions[0])
				case "wrong-target":
					if update {
						op.TargetID = "foreign"
					} else {
						op.TargetID = "unexpected"
					}
				case "missing-prior":
					e.receipts = nil
				case "duplicate-prior":
					if update {
						e.receipts = append(e.receipts, e.receipts[0])
					}
				case "foreign-prior":
					if update {
						e.receipts[0].ConfigUID = "foreign"
					}
				case "pruned-prior-versions":
					if update {
						e.receipts[0].Versions = nil
					}
				case "changed-prior-target":
					if update {
						e.receipts[0].TargetID = "foreign"
					}
				case "wrong-phase":
					op.Phase = "Submitting"
				case "partial-source":
					op.SourceUID = "source"
				}
				expected := mode == "valid" || !update && (mode == "missing-prior" || mode == "duplicate-prior" || mode == "foreign-prior" || mode == "pruned-prior-versions" || mode == "changed-prior-target")
				if e.knownCredentialTask(op) != expected {
					t.Fatal("global credential task scope changed", mode)
				}
			})
		}
	}
}
func TestGlobalCredentialTaskTreeNoRootFallback(t *testing.T) {
	exerciseCredentialTaskTree(t, false)
}
func TestGlobalCredentialMetadataTaskTreeNoRootFallback(t *testing.T) {
	exerciseCredentialTaskTree(t, true)
}
func exerciseCredentialTaskTree(t *testing.T, metadata bool) {
	for _, mode := range []string{"complete", "running-child", "failed-child", "foreign-child", "unavailable", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			e, op := globalRecoveryFixture(t, false)
			if metadata {
				e, op = metadataRecoveryFixture(t, false)
			}
			rootReads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != taskPath+op.TaskID+"/tree" {
					rootReads++
					t.Error("root fallback requested")
					fmt.Fprint(w, `{"response":{}}`)
					return
				}
				if mode == "unavailable" {
					w.WriteHeader(503)
					return
				}
				if mode == "malformed" {
					fmt.Fprint(w, `{"response":null}`)
					return
				}
				start := op.SubmittedAt.UnixMilli()
				end := time.Now().UnixMilli()
				root := map[string]any{"id": op.TaskID, "rootId": op.TaskID, "startTime": start, "endTime": end, "isError": false}
				child := map[string]any{"id": "child", "rootId": op.TaskID, "parentId": op.TaskID, "startTime": start, "endTime": end, "isError": mode == "failed-child"}
				if mode == "running-child" {
					child["endTime"] = nil
				}
				if mode == "foreign-child" {
					child["rootId"] = "foreign"
				}
				json.NewEncoder(w).Encode(map[string]any{"response": []any{root, child}})
			})
			a := &adapter{client: c}
			var plan configurationApplyPlan
			plan.section = "network_settings"
			a.bindGlobalCredentialTaskRecovery(&plan, e)
			done, err := plan.taskComplete(context.Background(), c, op, time.Now())
			if (err == nil && done) != (mode == "complete") || rootReads != 0 {
				t.Fatal("root/partial task tree passed", mode, err)
			}
		})
	}
}
