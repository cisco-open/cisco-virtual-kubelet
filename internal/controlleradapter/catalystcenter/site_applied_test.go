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
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func siteAppliedTestRecords() (cvk.NetworkControllerConfigurationActionApplication, cvk.NetworkControllerConfigurationActionApplication) {
	creator := siteCreatorTestRecord()
	cmp := &templateInputComparison{keyID: strings.Repeat("a", 64)}
	creator.Operation.Resource = sitePrivateCreateResource(desiredSite{kind: "area", path: "Global/Probe"}, &configapi.NetworkControllerConfig{}, cmp)
	creator.Resource = siteCreatedResource(&creator.Operation, "area", siteOwnedTestUUID, strings.Repeat("b", 64))
	op := creator.Operation.DeepCopy()
	op.ID = strings.Repeat("c", 32)
	op.Action = "Update"
	op.TargetID = siteOwnedTestUUID
	op.Resource = sitePrivateUpdatePrefix + strings.TrimPrefix(creator.Operation.Resource, sitePrivateCreatePrefix) + ":" + siteReceiptDigest(creator)
	submitted := metav1.NewTime(time.Now().Add(-time.Millisecond * 100))
	op.SubmittedAt = &submitted
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Operation: *op, VerifiedAt: &submitted}
	records, _ := siteCreatedRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator}})
	original, _ := siteCreatorForResource(records, op.Resource)
	row.Resource = siteAppliedResult(op, original, strings.Repeat("d", 64))
	return creator, row
}

func TestSiteAppliedReceiptBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "missing-creator", "legacy-creator", "creator-altered", "target", "parent", "owner", "generation", "missing-task", "missing-time", "future", "unverified", "pruned-result", "pruned-producer", "bad-mac", "wrong-key", "alias", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			creator, row := siteAppliedTestRecords()
			switch mode {
			case "legacy-creator":
				creator = siteCreatorTestRecord()
			case "creator-altered":
				creator.Operation.TaskID = "different-original-task"
			case "target":
				row.Operation.TargetID = "22222222-2222-4222-8222-222222222222"
			case "parent":
				row.Operation.ParentID = "different-parent"
			case "owner":
				row.Operation.ConfigUID = "foreign"
			case "generation":
				row.Operation.ConfigGeneration = 0
			case "missing-task":
				row.Operation.TaskID = ""
			case "missing-time":
				row.Operation.SubmittedAt = nil
			case "future":
				future := metav1.NewTime(time.Now().Add(time.Hour))
				row.VerifiedAt = &future
			case "unverified":
				row.Operation.Phase = "Verifying"
			case "pruned-result":
				row.Resource = ""
			case "pruned-producer":
				row.Operation.Resource = ""
			case "bad-mac":
				row.Resource = strings.Replace(row.Resource, strings.Repeat("d", 64), "bad", 1)
			case "wrong-key":
				row.Resource = strings.Replace(row.Resource, strings.Repeat("a", 64), strings.Repeat("e", 64), 1)
			}
			state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator, row}}
			if mode == "missing-creator" {
				state.ActionApplications = state.ActionApplications[1:]
			}
			if mode == "duplicate" {
				state.ActionApplications = append(state.ActionApplications, row)
			}
			if mode == "alias" {
				alias := row
				alias.Resource = "other"
				alias.Section = "other"
				state.ActionApplications = append(state.ActionApplications, alias)
			}
			_, err := siteAppliedRecords(state)
			if (err == nil) != (mode == "valid") || siteAppliedReceiptPresent(state, &row.Operation) != (mode == "valid") {
				t.Fatal("invalid latest-applied receipt accepted", err)
			}
		})
	}
}

func TestSiteAppliedCapacityAndPriorBinding(t *testing.T) {
	for _, mode := range []string{"first", "replace", "stale-prior", "count", "bytes"} {
		t.Run(mode, func(t *testing.T) {
			creator, row := siteAppliedTestRecords()
			state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator}}
			op := row.Operation.DeepCopy()
			op.Phase = "Submitting"
			op.TaskID = ""
			if mode == "replace" {
				state.ActionApplications = append(state.ActionApplications, row)
				op.ID = strings.Repeat("f", 32)
				op.Resource = sitePrivateUpdatePrefix + strings.TrimPrefix(creator.Operation.Resource, sitePrivateCreatePrefix) + ":" + siteReceiptDigest(row)
			}
			if mode == "stale-prior" {
				op.Resource = op.Resource[:len(op.Resource)-64] + strings.Repeat("0", 64)
			}
			if mode == "count" || mode == "bytes" {
				count := 255
				if mode == "bytes" {
					count = 250
				}
				for index := 0; index < count; index++ {
					entry := cvk.NetworkControllerConfigurationActionApplication{Section: "other", Resource: "other"}
					if mode == "bytes" {
						entry.Resource = strings.Repeat("x", 512)
						entry.Operation.Resource = strings.Repeat("x", 512)
						entry.Operation.SourceVersion = strings.Repeat("x", 512)
					}
					state.ActionApplications = append(state.ActionApplications, entry)
				}
			}
			if (siteApplyCapacity(state, op) == nil) != (mode == "first" || mode == "replace") {
				t.Fatal("private Update capacity/prior binding incorrect")
			}
		})
	}
}

func TestSiteAppliedSettlementRejectsSchemaLoss(t *testing.T) {
	for _, mode := range []string{"valid", "resource", "target", "parent", "task", "missing-row", "both-resources"} {
		t.Run(mode, func(t *testing.T) {
			a, _ := journalFixture(t)
			ctx := context.Background()
			creator, row := siteAppliedTestRecords()
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			op := row.Operation.DeepCopy()
			state := &cvk.NetworkControllerConfigurationStatus{Operation: op, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator, row}}
			switch mode {
			case "resource":
				op.Resource = ""
			case "target":
				op.TargetID = ""
			case "parent":
				op.ParentID = ""
			case "task":
				op.TaskID = ""
			case "missing-row":
				state.ActionApplications = state.ActionApplications[:1]
			case "both-resources":
				op.Resource = ""
				state.ActionApplications[1].Resource = ""
			}
			nc.Status.Configuration = state
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			err = a.finishConfiguration(ctx, op.ID)
			if mode == "valid" {
				if err != nil {
					t.Fatal("valid latest receipt did not settle", err)
				}
			} else if !errors.Is(err, errConfigurationBusy) {
				t.Fatal("partial or missing latest proof settled", err)
			}
		})
	}
}

func TestSiteAppliedSnapshotDoesNotAliasStatus(t *testing.T) {
	creator, row := siteAppliedTestRecords()
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator, row}}
	creators, err := siteCreatedRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := siteAppliedRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	e := &siteConfigurationExecutor{creators: creators, applied: applied}
	state.ActionApplications[1].VerifiedAt.Time = state.ActionApplications[1].VerifiedAt.Add(-time.Millisecond)
	if e.creatorRecordsMatch(state) == nil {
		t.Fatal("mutable status rewrote prior latest-applied evidence")
	}
}

func TestSitePrivateBaselineReusesNativeInventory(t *testing.T) {
	_, actual := siteDispatchFixture("floor-update")
	ids := map[string]string{"area": "11111111-1111-4111-8111-111111111111", "building": "22222222-2222-4222-8222-222222222222", "floor": "33333333-3333-4333-8333-333333333333"}
	for index := range actual {
		if id, ok := ids[actual[index].ID]; ok {
			actual[index].ID = id
		}
		if id, ok := ids[actual[index].ParentID]; ok {
			actual[index].ParentID = id
		}
	}
	reads := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" || r.URL.Path != sitesPath {
			t.Error("unexpected native request")
			w.WriteHeader(500)
			return
		}
		reads++
		json.NewEncoder(w).Encode(map[string]any{"response": actual})
	})
	comparison := &templateInputComparison{key: []byte(strings.Repeat("k", 32)), keyID: strings.Repeat("a", 64), check: func(context.Context) error { return nil }}
	e := &siteConfigurationExecutor{client: c, creatorOwner: "owner", creators: map[string]siteCreatorRecord{}, comparison: comparison}
	for _, site := range actual {
		if site.Type == "global" {
			continue
		}
		desired := desiredSite{kind: site.Type, path: site.NameHierarchy}
		e.desired = append(e.desired, desired)
		creator := siteCreatorTestRecord()
		creator.Operation.ParentID = site.ParentID
		creator.Operation.Resource = sitePrivateCreateResource(desired, &configapi.NetworkControllerConfig{}, comparison)
		view, err := c.siteNativeFootprint(context.Background(), actual, site.NameHierarchy, false)
		if err != nil {
			t.Fatal(err)
		}
		mac, err := siteNativeMAC(comparison, creator.Operation.Resource, view)
		if err != nil {
			t.Fatal(err)
		}
		e.creators[creator.Operation.Resource] = siteCreatorRecord{kind: site.Type, id: site.ID, keyID: comparison.keyID, mac: mac, receipt: creator}
	}
	reads = 0
	if err := e.checkPrivateBaselines(context.Background(), actual); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatal("baseline check fetched the inventory once per site", reads)
	}
}
