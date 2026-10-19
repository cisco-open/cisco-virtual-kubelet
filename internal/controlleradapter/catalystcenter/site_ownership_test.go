// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const siteOwnedTestUUID = "11111111-1111-4111-8111-111111111111"

func siteCreatorTestRecord() cvk.NetworkControllerConfigurationActionApplication {
	now := metav1.NewTime(time.Now().Add(-time.Second))
	op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, Section: "sites", Resource: "Global/Probe", Action: "Create", Phase: "Verified", TaskID: "original-task", ParentID: "root", SubmittedAt: &now}
	return cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: siteCreatedResource(&op, "area", siteOwnedTestUUID), Operation: op, VerifiedAt: &now}
}

func TestSiteCreatorReceiptBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "configmap", "lost-source", "bad-target", "bad-path", "bad-kind", "no-parent", "unverified", "no-task", "no-time", "future", "duplicate", "alias", "pruned-result", "foreign-section"} {
		t.Run(mode, func(t *testing.T) {
			r := siteCreatorTestRecord()
			switch mode {
			case "configmap", "lost-source":
				r.Operation.SourceUID, r.Operation.SourceVersion = "source", "revision"
				r.Resource = siteCreatedResource(&r.Operation, "area", siteOwnedTestUUID)
				if mode == "lost-source" {
					r.Operation.SourceUID, r.Operation.SourceVersion = "", ""
				}
			case "bad-target":
				r.Resource = strings.TrimSuffix(r.Resource, siteOwnedTestUUID) + "invalid"
			case "bad-path":
				r.Operation.Resource = "Global/Other"
			case "bad-kind":
				r.Resource = strings.Replace(r.Resource, ":area:", ":device:", 1)
			case "no-parent":
				r.Operation.ParentID = ""
			case "unverified":
				r.Operation.Phase = "Verifying"
			case "no-task":
				r.Operation.TaskID = ""
			case "no-time":
				r.Operation.SubmittedAt = nil
			case "future":
				future := metav1.NewTime(time.Now().Add(time.Hour))
				r.VerifiedAt = &future
			case "pruned-result":
				r.Resource = ""
			case "foreign-section":
				r.Section = "templates"
			}
			state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{r}}
			if mode == "duplicate" {
				state.ActionApplications = append(state.ActionApplications, r)
			}
			if mode == "alias" {
				alias := r
				alias.Resource = "unrelated"
				state.ActionApplications = append(state.ActionApplications, alias)
			}
			_, err := siteCreatedRecords(state)
			valid := mode == "valid" || mode == "configmap"
			if (err == nil) != valid || siteCreatedReceiptPresent(state, &r.Operation) != valid {
				t.Fatal("invalid creator receipt accepted", err)
			}
		})
	}
}

func TestSiteCreatorIdentityFence(t *testing.T) {
	for _, mode := range []string{"same", "replaced", "missing", "parent", "foreign-owner", "imported"} {
		t.Run(mode, func(t *testing.T) {
			r := siteCreatorTestRecord()
			records, err := siteCreatedRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{r}})
			if err != nil {
				t.Fatal(err)
			}
			desired, actual := siteDispatchFixture("area-create")
			actual = append(actual, Site{ID: siteOwnedTestUUID, ParentID: "root", Name: "Probe", NameHierarchy: "Global/Probe", Type: "area"})
			e := &siteConfigurationExecutor{desired: desired, creators: records, creatorOwner: "owner"}
			switch mode {
			case "replaced":
				actual[len(actual)-1].ID = "replaced"
			case "missing":
				actual = actual[:len(actual)-1]
			case "parent":
				actual[len(actual)-1].ParentID = "new-parent"
			case "foreign-owner":
				e.creatorOwner = "foreign"
			case "imported":
				e.creators = nil
			}
			if (e.checkCreatorIdentities(actual) == nil) != (mode == "same" || mode == "imported") {
				t.Fatal("creator identity fence was bypassed")
			}
		})
	}
}

func TestSiteCreatorCannotSettleWithoutReceipt(t *testing.T) {
	a, config := journalFixture(t)
	ctx := context.Background()
	op, err := a.prepareConfiguration(ctx, config, nil, "sites", "Global/Probe", "Create")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.startConfiguration(ctx, config, nil, op.ID); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"Unknown", "Verifying", "Verified"} {
		if err := a.advanceConfiguration(ctx, op.ID, phase, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.finishConfiguration(ctx, op.ID); !errors.Is(err, errConfigurationBusy) {
		t.Fatal("generic settlement bypassed creator receipt", err)
	}
	if journalState(t, a).Operation == nil {
		t.Fatal("missing proof released the operation")
	}
}

func TestSiteCreatorCapacity(t *testing.T) {
	for _, mode := range []string{"empty", "existing", "duplicate-operation", "count", "bytes", "changed-history"} {
		t.Run(mode, func(t *testing.T) {
			r := siteCreatorTestRecord()
			op := r.Operation.DeepCopy()
			op.Phase, op.TaskID = "Submitting", ""
			state := &cvk.NetworkControllerConfigurationStatus{}
			switch mode {
			case "existing", "changed-history":
				state.ActionApplications = append(state.ActionApplications, r)
			case "duplicate-operation":
				state.ActionApplications = append(state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "other", Resource: "other", Operation: *op})
			case "count", "bytes":
				count := 256
				if mode == "bytes" {
					count = 255
				}
				for index := 0; index < count; index++ {
					entry := cvk.NetworkControllerConfigurationActionApplication{Section: "other", Resource: fmt.Sprint(index)}
					if mode == "bytes" {
						entry.Resource = strings.Repeat("x", 512)
						entry.Operation.Resource = strings.Repeat("x", 512)
						entry.Operation.SourceVersion = strings.Repeat("x", 512)
					}
					state.ActionApplications = append(state.ActionApplications, entry)
				}
			}
			if mode == "changed-history" {
				e := &siteConfigurationExecutor{creators: map[string]siteCreatorRecord{}}
				if e.creatorRecordsMatch(state) == nil {
					t.Fatal("changed creator history accepted")
				}
				return
			}
			if (siteCreateCapacity(state, op) == nil) != (mode == "empty") {
				t.Fatal("creator storage was not reserved before dispatch")
			}
		})
	}
}

func TestSiteCreatorPublicIdentityFence(t *testing.T) {
	for _, stage := range []string{"area-create", "building-create", "floor-create"} {
		for _, mode := range []string{"creator-replaced", "creator-missing"} {
			t.Run(stage+"/"+mode, func(t *testing.T) { exerciseSiteSingularPublic(t, stage, mode, nil) })
		}
	}
}

func TestSiteCreatorSnapshotDoesNotAliasStatus(t *testing.T) {
	r := siteCreatorTestRecord()
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{r}}
	records, err := siteCreatedRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	e := &siteConfigurationExecutor{creators: records}
	state.ActionApplications[0].VerifiedAt.Time = state.ActionApplications[0].VerifiedAt.Add(-time.Millisecond)
	if e.creatorRecordsMatch(state) == nil {
		t.Fatal("mutable status rewrote the private creator snapshot")
	}
}
