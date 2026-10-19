// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var siteRetirementReceiptModes = []string{"complete", "latest", "unknown", "generic", "pending", "failed", "conflict", "lost-response", "pruned-receipt", "pruned-creator", "pruned-operation", "capacity", "bytes", "edit-target", "edit-prepared", "edit-source", "edit-row-target", "edit-result", "edit-both-results", "duplicate", "missing-time", "future-time"}

func TestSiteRetirementReceipts(t *testing.T) {
	for _, cm := range []bool{false, true} {
		for _, mode := range siteRetirementReceiptModes {
			t.Run(fmt.Sprintf("cm-%t/%s", cm, mode), func(t *testing.T) { exerciseSiteRetirementBinding(t, "recovery-receipt-"+mode, cm, nil) })
		}
	}
}

// Fault injection occurs at the single status CAS, never at a native request.
type siteRetirementReceiptClient struct {
	ctrlclient.Client
	mode   string
	writes int
}

func (c *siteRetirementReceiptClient) Status() ctrlclient.SubResourceWriter {
	return &siteRetirementReceiptWriter{SubResourceWriter: c.Client.Status(), c: c}
}

type siteRetirementReceiptWriter struct {
	ctrlclient.SubResourceWriter
	c *siteRetirementReceiptClient
}

func (w *siteRetirementReceiptWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	nc, ok := obj.(*cvk.NetworkController)
	if !ok || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.Phase != "Verified" || !siteRetirementOperation(nc.Status.Configuration.Operation) {
		return w.SubResourceWriter.Update(ctx, obj, opts...)
	}
	w.c.writes++
	switch w.c.mode {
	case "conflict":
		return apierrors.NewConflict(schema.GroupResource{Group: "cisco.vk", Resource: "networkcontrollers"}, nc.Name, errors.New("test status conflict"))
	case "pruned-operation":
		nc.Status.Configuration.Operation.ParentID = ""
	case "pruned-receipt", "pruned-creator":
		rows := nc.Status.Configuration.ActionApplications[:0]
		for _, row := range nc.Status.Configuration.ActionApplications {
			remove := w.c.mode == "pruned-receipt" && strings.HasPrefix(row.Resource, siteRetiredPrefix) || w.c.mode == "pruned-creator" && strings.HasPrefix(row.Resource, sitePrivateCreatedPrefix)
			if !remove {
				rows = append(rows, row)
			}
		}
		nc.Status.Configuration.ActionApplications = rows
	}
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if w.c.mode == "lost-response" {
		return errors.New("test lost status response")
	}
	return nil
}

func (q *siteRetirementRecoveryFixture) verifyReceipt(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, expected *cvk.NetworkControllerConfigurationOperation, updateState func(func(*cvk.NetworkController))) {
	t.Helper()
	ctx := context.Background()
	mode := strings.TrimPrefix(q.mode, "receipt-")
	if mode == "generic" {
		updateState(func(n *cvk.NetworkController) { n.Status.Configuration.Operation.Phase = "Verifying" })
		before := journalState(t, a).DeepCopy()
		if err := a.advanceConfiguration(ctx, expected.ID, "Verified", ""); err == nil || !reflect.DeepEqual(before, journalState(t, a)) {
			t.Fatal("generic verification bypassed retirement receipt")
		}
	}
	if mode == "capacity" || mode == "bytes" {
		updateState(func(n *cvk.NetworkController) { fillSiteRetirementHistory(n.Status.Configuration, mode == "bytes") })
	}
	before := journalState(t, a).DeepCopy()
	if mode == "bytes" {
		raw, err := json.Marshal(before.ActionApplications)
		if err != nil || len(raw) <= 256<<10 {
			t.Fatal("byte-capacity fixture did not exceed the supported receipt bound")
		}
	}
	underlying := a.statusWriter
	fault := &siteRetirementReceiptClient{Client: underlying, mode: mode}
	if mode == "conflict" || mode == "lost-response" || strings.HasPrefix(mode, "pruned-") {
		a.statusWriter = fault
	}
	err := a.recordSiteRetired(ctx, config, source, expected.ID)
	a.statusWriter = underlying
	blocked := mode == "pending" || mode == "failed" || mode == "capacity" || mode == "bytes" || mode == "conflict" || mode == "lost-response" || strings.HasPrefix(mode, "pruned-")
	if (err != nil) != blocked {
		t.Fatal("retirement receipt publication mismatch", mode, err)
	}
	state := journalState(t, a)
	if mode == "pending" || mode == "failed" || mode == "capacity" || mode == "bytes" || mode == "conflict" {
		if !reflect.DeepEqual(before, state) {
			t.Fatal("failed publication changed durable predecessor state")
		}
		if mode != "conflict" {
			return
		}
		if fault.writes != 1 {
			t.Fatal("conflict retried status publication without fresh observation")
		}
		if err := a.recordSiteRetired(ctx, config, source, expected.ID); err != nil {
			t.Fatal("original-operation reobservation did not recover conflict", err)
		}
		state = journalState(t, a)
	}
	if strings.HasPrefix(mode, "pruned-") {
		if state.Operation == nil || state.Operation.Phase != "Verified" {
			t.Fatal("pruned schema did not retain verified uncertainty")
		}
		if err := a.finishConfiguration(ctx, expected.ID); err == nil {
			t.Fatal("schema loss released retirement fence")
		}
		if err := a.recordSiteRetired(ctx, config, source, expected.ID); err == nil {
			t.Fatal("schema loss was silently reconstructed")
		}
		return
	}
	if state.Operation == nil || state.Operation.Phase != "Verified" || !siteRetiredReceiptPresent(state, state.Operation) || len(state.ActionApplications) != len(before.ActionApplications)+1 {
		t.Fatal("receipt and Verified operation did not persist together")
	}
	for i, row := range before.ActionApplications {
		if !reflect.DeepEqual(row, state.ActionApplications[i]) {
			t.Fatal("retirement changed predecessor custody")
		}
	}
	first := state.DeepCopy()
	treeReads, siteReads := q.treeReads, q.siteReads
	if err := a.recordSiteRetired(ctx, config, source, expected.ID); err != nil {
		t.Fatal("committed receipt retry failed", err)
	}
	if !reflect.DeepEqual(first, journalState(t, a)) || q.treeReads != treeReads || q.siteReads != siteReads {
		t.Fatal("committed retry rewrote proof or observed native state again")
	}
	if mode == "lost-response" && fault.writes != 1 {
		t.Fatal("lost status response replayed publication")
	}
	malformed := strings.HasPrefix(mode, "edit-") || mode == "duplicate" || mode == "missing-time" || mode == "future-time"
	if malformed {
		updateState(func(n *cvk.NetworkController) {
			state := n.Status.Configuration
			row := &state.ActionApplications[len(state.ActionApplications)-1]
			switch mode {
			case "edit-prepared":
				state.Operation.Phase = "Prepared"
			case "edit-target":
				state.Operation.TargetID = "22222222-2222-4222-8222-222222222222"
			case "edit-source":
				state.Operation.SourceVersion = "changed"
			case "edit-row-target":
				row.Operation.TargetID = "22222222-2222-4222-8222-222222222222"
			case "edit-result":
				row.Resource = "unrecognized-result"
			case "edit-both-results":
				row.Resource = "unrecognized-result"
				row.Operation.Resource = "unrecognized-producer"
				state.Operation.Resource = "unrecognized-producer"
			case "duplicate":
				state.ActionApplications = append(state.ActionApplications, *row.DeepCopy())
			case "missing-time":
				row.VerifiedAt = nil
			case "future-time":
				row.VerifiedAt = row.VerifiedAt.DeepCopy()
				row.VerifiedAt.Time = row.VerifiedAt.AddDate(1, 0, 0)
			}
		})
		if err := a.finishConfiguration(ctx, expected.ID); err == nil {
			t.Fatal("malformed retirement result released the operation fence")
		}
		return
	}
	if err := a.finishConfiguration(ctx, expected.ID); err != nil {
		t.Fatal("verified retirement receipt did not settle", err)
	}
	settled := journalState(t, a)
	if settled.Operation != nil || !reflect.DeepEqual(settled.ActionApplications, first.ActionApplications) || !reflect.DeepEqual(settled.Owners, first.Owners) {
		t.Fatal("settlement erased lineage or released section custody")
	}
}

func TestSiteRetirementPreparedCancellation(t *testing.T) {
	a, config := journalFixture(t)
	ctx := context.Background()
	resource := siteRetirementPrefix + strings.Join([]string{"area", strings.Repeat("1", 64), "i", strings.Repeat("2", 64), strings.Repeat("3", 64), strings.Repeat("0", 64), strings.Repeat("4", 64), strings.Repeat("5", 64)}, ":")
	op, err := a.prepareConfiguration(ctx, config, nil, "sites", resource, "Delete", configurationResourceIdentity{target: siteOwnedTestUUID, parent: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.finishConfiguration(ctx, op.ID); err != nil {
		t.Fatal("unsubmitted retirement preparation could not be cancelled", err)
	}
	state := journalState(t, a)
	if state.Operation != nil || len(state.Owners) != 1 || len(state.ActionApplications) != 0 {
		t.Fatal("prepared cancellation changed custody or invented a receipt")
	}
}

// Shared schema-valid history fixtures for pre-dispatch and publication bounds.
func fillSiteRetirementHistory(state *cvk.NetworkControllerConfigurationStatus, byteBound bool) {
	count := 255
	if byteBound {
		count = 220
	}
	for i := 0; i < count; i++ {
		row := state.ActionApplications[0].DeepCopy()
		row.Section = "other"
		row.Resource = fmt.Sprintf("unrelated-%d", i)
		row.Operation.ID = fmt.Sprintf("%032x", i+100)
		row.Operation.Resource = row.Resource
		row.Operation.Section = "other"
		row.Operation.Action = "Update"
		row.Operation.TargetID = ""
		row.Operation.ParentID = ""
		if byteBound {
			row.Resource += strings.Repeat("x", 470)
			row.Operation.Resource = row.Resource
			row.Operation.SourceVersion = strings.Repeat("v", 128)
		}
		state.ActionApplications = append(state.ActionApplications, *row)
	}
}
