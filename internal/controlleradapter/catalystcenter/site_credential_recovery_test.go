// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func siteRecoveryFixture() (*siteCredentialExecutor, *cvk.NetworkControllerConfigurationOperation) {
	d := siteCredentialIntent{site: "Global/Lab", siteKind: "area", kind: "cli_credentials", name: "example"}
	op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), Section: "network_settings", Resource: d.resource(), Action: "Update", Phase: "Unknown", ConfigName: "intent", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, TargetID: "area", ParentID: "credential", TaskID: "original-task", SubmittedAt: &metav1.Time{Time: time.Now().Add(-time.Second)}}
	return &siteCredentialExecutor{desired: []siteCredentialIntent{d}}, op
}
func TestSiteCredentialKnownTaskScope(t *testing.T) {
	for _, mode := range []string{"valid", "taskless", "wrong-action", "missing-site", "missing-credential", "created-id", "device-serial", "missing-owner", "missing-name", "future", "prepared", "credential-versions", "history", "source-half", "duplicate", "removed", "inherit", "wrong-phase", "other-resource"} {
		t.Run(mode, func(t *testing.T) {
			e, op := siteRecoveryFixture()
			switch mode {
			case "taskless":
				op.TaskID = ""
			case "wrong-action":
				op.Action = "Create"
			case "missing-site":
				op.TargetID = ""
			case "missing-credential":
				op.ParentID = ""
			case "created-id":
				op.CreatedResourceID = "other"
			case "device-serial":
				op.TargetSerial = "serial"
			case "missing-owner":
				op.ConfigUID = ""
			case "missing-name":
				op.ConfigName = ""
			case "future":
				op.SubmittedAt = &metav1.Time{Time: time.Now().Add(time.Hour)}
			case "prepared":
				op.PreparedAt = op.SubmittedAt.DeepCopy()
			case "credential-versions":
				op.CredentialVersions = []cvk.NetworkControllerConfigurationCredentialVersion{{Version: 1}}
			case "history":
				op.SSIDCredentialHistory = []cvk.NetworkControllerConfigurationSSIDCredentialHistory{{}}
			case "source-half":
				op.SourceUID = "source"
			case "duplicate":
				e.desired = append(e.desired, e.desired[0])
			case "removed":
				e.desired = nil
			case "inherit":
				e.desired[0].name = ""
			case "wrong-phase":
				op.Phase = "Prepared"
			case "other-resource":
				op.Resource = "network-settings:other"
			}
			if siteCredentialTaskScope(e, op, time.Now()) != (mode == "valid") {
				t.Fatal("invalid observation scope admitted")
			}
		})
	}
}
func TestSiteCredentialTaskTreeAndDependencies(t *testing.T) {
	for _, mode := range []string{"matching", "running-child", "failed-child", "tree-unavailable", "wrong-site", "wrong-credential", "credential-race", "slot-race"} {
		t.Run(mode, func(t *testing.T) {
			e, op := siteRecoveryFixture()
			var rootReads, credentialReads, slotReads atomic.Int32
			e.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == sitesPath:
					sites := sampleSites()
					if mode == "wrong-site" {
						for i := range sites {
							if sites[i].ID == "area" {
								sites[i].ID = "replacement"
							}
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": sites})
				case r.URL.Path == globalCredentialsPath:
					n := credentialReads.Add(1)
					id := "credential"
					if mode == "wrong-credential" || mode == "credential-race" && n > 1 {
						id = "replacement"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"cliCredential": []any{map[string]any{"id": id, "description": "example"}}}})
				case r.URL.Path == siteCredentialsPath("area"):
					n := slotReads.Add(1)
					id := "credential"
					if mode == "slot-race" && n > 1 {
						id = "replacement"
					}
					slots := map[string]any{}
					for _, k := range globalCredentialKinds {
						slots[credentialSlot(k.model)] = nil
					}
					slots[credentialSlot("cli_credentials")] = map[string]any{"credentialsId": id}
					json.NewEncoder(w).Encode(map[string]any{"response": slots})
				case r.URL.Path == taskPath+op.TaskID+"/tree":
					if mode == "tree-unavailable" {
						w.WriteHeader(503)
						return
					}
					end := any(op.SubmittedAt.Add(100 * time.Millisecond).UnixMilli())
					if mode == "running-child" {
						end = nil
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"id": op.TaskID, "isError": false, "startTime": op.SubmittedAt.UnixMilli(), "endTime": op.SubmittedAt.Add(100 * time.Millisecond).UnixMilli()}, map[string]any{"id": "child", "rootId": op.TaskID, "parentId": op.TaskID, "startTime": op.SubmittedAt.UnixMilli(), "endTime": end, "isError": mode == "failed-child"}}})
				default:
					rootReads.Add(1)
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})
			done, err := e.observeKnownTask(context.Background(), op, time.Now())
			if (err == nil && done) != (mode == "matching") || rootReads.Load() != 0 {
				t.Fatal("partial tree or replaced dependency passed", err)
			}
			op.Phase = "Verifying"
			done, err = e.verify(context.Background(), op)
			if (err == nil && done) != (mode == "matching") || rootReads.Load() != 0 {
				t.Fatal("final verification bypassed task/dependency proof", err)
			}
		})
	}
}
func TestSiteCredentialRecoveryPreservesOtherObservers(t *testing.T) {
	e, op := siteRecoveryFixture()
	sentinel := errors.New("original observer")
	observed := false
	plan := configurationApplyPlan{executor: &networkSectionExecutor{assignments: e}, recoverKnownTask: func(*cvk.NetworkControllerConfigurationOperation) bool { return true }, observeCompletion: func(context.Context, *cvk.NetworkControllerConfigurationOperation, time.Time) (bool, error) {
		observed = true
		return false, sentinel
	}}
	bindSiteCredentialTaskRecovery(&plan)
	malformed := op.DeepCopy()
	malformed.TaskID = ""
	if plan.recoverKnownTask(malformed) {
		t.Fatal("other observer admitted malformed assignment")
	}
	other := op.DeepCopy()
	other.Resource = "other"
	if !plan.recoverKnownTask(other) {
		t.Fatal("prior recovery lost")
	}
	if _, err := plan.observeCompletion(context.Background(), other, time.Now()); !observed || !errors.Is(err, sentinel) {
		t.Fatal("prior observer lost")
	}
}
func TestSiteCredentialUnknownRecoveryManifest(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			siteCredentialManifestCycle(t, global, siteCredentialManifestQualification{recover: true})
		})
	}
}
func TestSelectedSiteCredentialUnknownRecoveryManifest(t *testing.T) {
	siteCredentialManifestCycle(t, false, siteCredentialManifestQualification{selected: true, recover: true})
}
