// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var siteCreationPublicationModes = []string{"complete", "conflict", "lost-response", "drop-history", "edit-history", "drop-successor", "drop-operation", "drop-unrelated"}

type siteCreationPublicationClient struct {
	ctrlclient.Client
	mode   string
	writes int
}

func (c *siteCreationPublicationClient) Status() ctrlclient.SubResourceWriter {
	return &siteCreationPublicationWriter{SubResourceWriter: c.Client.Status(), c: c}
}

type siteCreationPublicationWriter struct {
	ctrlclient.SubResourceWriter
	c *siteCreationPublicationClient
}

func (w *siteCreationPublicationWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	w.c.writes++
	nc := obj.(*cvk.NetworkController)
	switch w.c.mode {
	case "conflict":
		return apierrors.NewConflict(schema.GroupResource{Group: "cisco.vk", Resource: "networkcontrollers"}, nc.Name, errors.New("fixture conflict"))
	case "drop-history":
		nc.Status.Configuration.ActionApplications = nc.Status.Configuration.ActionApplications[1:]
	case "edit-history":
		nc.Status.Configuration.ActionApplications[0].Operation.TaskID = "changed-original"
	case "drop-successor":
		rows := nc.Status.Configuration.ActionApplications
		nc.Status.Configuration.ActionApplications = rows[:len(rows)-1]
	case "drop-operation":
		nc.Status.Configuration.Operation = nil
	case "drop-unrelated":
		rows := nc.Status.Configuration.ActionApplications[:0]
		for _, row := range nc.Status.Configuration.ActionApplications {
			if row.Section != "other" {
				rows = append(rows, row)
			}
		}
		nc.Status.Configuration.ActionApplications = rows
	}
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if w.c.mode == "lost-response" {
		return errors.New("fixture lost response")
	}
	return nil
}

func TestSiteCreationPublicationCAS(t *testing.T) {
	for _, mode := range siteCreationPublicationModes {
		t.Run(mode, func(t *testing.T) { exerciseSiteCreationPublicationCAS(t, mode, nil) })
	}
}

// Native proofs are synthetic here. This exercises the production publication
// boundary and shared status CAS, not native creation or dispatch.
func exerciseSiteCreationPublicationCAS(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	if install != nil {
		install(a, config)
	}
	state, op, row := siteRecreationFixture(t, true)
	unrelated := *state.ActionApplications[0].DeepCopy()
	unrelated.Section, unrelated.Resource = "other", "unrelated"
	unrelated.Operation.Section, unrelated.Operation.Resource, unrelated.Operation.ID = "other", "unrelated", strings.Repeat("9", 32)
	state.ActionApplications = append(state.ActionApplications, unrelated)
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration = state
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := nc.Status.Configuration.DeepCopy()
	// Use the operation read back from Kubernetes, as the production caller
	// does. metav1 timestamps serialize at second precision.
	op = before.Operation.DeepCopy()
	row.Operation = *op.DeepCopy()
	row.Operation.Phase = "Verified"
	row.VerifiedAt = op.SubmittedAt.DeepCopy()
	complete, err := siteCreationPublication(before, op, row)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration = complete
	underlying := a.statusWriter
	fault := &siteCreationPublicationClient{Client: underlying, mode: mode}
	a.statusWriter = fault
	err = a.persistSiteCreationPublication(ctx, nc)
	a.statusWriter = underlying
	if (err == nil) != (mode == "complete") || fault.writes != 1 {
		t.Fatalf("publication result or CAS count mismatch: %v, %d", err, fault.writes)
	}
	stored, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "conflict":
		if !reflect.DeepEqual(before, stored.Status.Configuration) {
			t.Fatal("conflict changed predecessor")
		}
	case "complete", "lost-response":
		if !siteCreatedReceiptPresent(stored.Status.Configuration, stored.Status.Configuration.Operation) {
			t.Fatal("complete publication lost history")
		}
		if err := a.finishConfiguration(ctx, op.ID); err != nil {
			t.Fatal("verified successor could not finish", err)
		}
	case "drop-operation":
		// A missing operation cannot authorize finish or a new publication.
		if err := a.persistSiteCreationPublication(ctx, stored); err == nil {
			t.Fatal("missing operation accepted")
		}
	case "drop-unrelated":
		if !siteCreatedReceiptPresent(stored.Status.Configuration, stored.Status.Configuration.Operation) {
			t.Fatal("unrelated-row loss fixture invalidated creator proof instead of testing complete-status readback")
		}
	default:
		if err := a.finishConfiguration(ctx, op.ID); err == nil {
			t.Fatal("corrupt history released operation fence")
		}
	}
}
