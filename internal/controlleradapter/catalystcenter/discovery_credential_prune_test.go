// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var discoveryCredentialPruneModes = []string{"prune", "prune-empty", "prune-configmap", "prune-unknown", "prune-retain", "prune-conflict", "prune-composition", "prune-lost-permit", "prune-lost-receipt", "prune-proof-pruned", "prune-ambiguous", "prune-failed", "prune-capacity", "prune-empty-conflict", "prune-empty-unknown", "prune-legacy", "prune-active", "prune-composition-pending", "prune-empty-orphan-credentials", "prune-empty-orphan-actions", "prune-source-loss", "prune-empty-source-loss"}

func TestDiscoveryCredentialPublicPrune(t *testing.T) {
	for _, mode := range discoveryCredentialPruneModes {
		t.Run(mode, func(t *testing.T) { discoveryCredentialMultipleScenario(t, mode, nil) })
	}
}

func discoveryCredentialPruneCycle(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, model map[string]any, first map[string]any, original []byte, jobs map[string]discoveryMetadata, posts, deletes *int, mode string) {
	t.Helper()
	ctx := context.Background()
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	refs := append([]configapi.NetworkControllerSecretRef(nil), c.Spec.SecretRefs...)
	initial := journalState(t, a).DeepCopy()
	inventory := model["catalyst_center"].(map[string]any)["inventory"].(map[string]any)
	inventory["discovery"] = []any{first}
	if strings.HasPrefix(mode, "prune-empty") {
		inventory["discovery"] = []any{}
	}
	raw, _ := json.Marshal(model)
	setSource := func(raw []byte) {
		t.Helper()
		if c.Spec.Source.ConfigMapRef == nil {
			c.Spec.Source.Inline.Raw = raw
		} else {
			var cm core.ConfigMap
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: c.Namespace, Name: c.Spec.Source.ConfigMapRef.Name}, &cm); err != nil {
				t.Fatal(err)
			}
			cm.Data["model"] = string(raw)
			if err := a.statusWriter.Update(ctx, &cm); err != nil {
				t.Fatal(err)
			}
		}
	}
	setSource(raw)
	c.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	if mode == "prune-retain" {
		c.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	}
	c.Spec.SecretRefs = nil
	if !strings.HasPrefix(mode, "prune-empty") {
		for _, ref := range refs {
			if ref.Path == "/discovery/0/password_list" {
				c.Spec.SecretRefs = append(c.Spec.SecretRefs, ref)
			}
		}
	}
	if mode == "prune-unknown" || mode == "prune-empty-unknown" {
		c.Spec.TaskTimeout = &metav1.Duration{Duration: time.Nanosecond}
	}
	c.Generation++
	if err := a.statusWriter.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if mode == "prune-conflict" || mode == "prune-empty-conflict" {
		row := jobs["another"]
		row.Usernames = []string{"foreign"}
		jobs["another"] = row
	}
	if mode == "prune-composition-pending" {
		row := jobs["fixture"]
		row.Status = "Active"
		row.Condition = "In Progress"
		jobs["fixture"] = row
	}
	if mode == "prune-active" {
		row := jobs["another"]
		row.Status = "Active"
		row.Condition = "In Progress"
		jobs["another"] = row
	}
	if mode == "prune-legacy" {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i := range nc.Status.Configuration.ActionApplications {
			r := &nc.Status.Configuration.ActionApplications[i]
			id, err := parseDiscoveryCredentialReceipt(r.Resource)
			if err == nil && id.name == discoveryPublicDigest("another") {
				r.Resource = discoveryCredentialReceiptPrefix + id.name + ":" + id.id
				base, _, ok := discoveryCredentialInputParts(r.Operation.Resource)
				if !ok {
					t.Fatal("invalid legacy fixture")
				}
				r.Operation.Resource = base
				for j := range nc.Status.Configuration.CredentialApplications {
					app := &nc.Status.Configuration.CredentialApplications[j]
					if app.OperationID == r.Operation.ID {
						app.Resource = base
					}
				}
			}
		}
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "prune-empty-orphan-credentials" || mode == "prune-empty-orphan-actions" {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "prune-empty-orphan-credentials" {
			nc.Status.Configuration.ActionApplications = nil
		} else {
			nc.Status.Configuration.CredentialApplications = nil
		}
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "prune-capacity" {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, pnpCapacityHistory(254, false)...)
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	base := a.statusWriter
	hits := 0
	fault := ""
	switch mode {
	case "prune-lost-permit":
		fault = "lost-permit-ack"
	case "prune-lost-receipt":
		fault = "lost-receipt-ack"
	case "prune-proof-pruned":
		fault = "pruned-retirement"
	}
	if fault != "" {
		a.statusWriter = discoveryRetirementFaultClient{base, fault, &hits}
	}
	defer func() { a.statusWriter = base }()
	reconcile := func() {
		t.Helper()
		copy := *a
		if _, err := (&configReconciler{a: &copy}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
	}
	blocked := mode == "prune-empty-orphan-credentials" || mode == "prune-empty-orphan-actions" || mode == "prune-legacy" || mode == "prune-empty-conflict" || mode == "prune-conflict" || mode == "prune-capacity" || mode == "prune-lost-permit" || mode == "prune-proof-pruned" || mode == "prune-ambiguous" || mode == "prune-failed"
	sawUnknown := false
	cleanedWhileOtherPending := false
	retainedSource := mode == "prune-source-loss" || mode == "prune-empty-source-loss"
	var removedSource *core.ConfigMap
	restoredSource := false
	for i := 0; i < 28; i++ {
		reconcile()
		if mode == "prune-composition-pending" && *deletes == 1 {
			row := jobs["fixture"]
			if row.Status == "Active" {
				cleanedWhileOtherPending = true
				row.Status = "Inactive"
				row.Condition = "Complete"
				jobs["fixture"] = row
			}
		}
		state := journalState(t, a)
		if retainedSource && removedSource == nil && *deletes == 1 {
			var cm core.ConfigMap
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: c.Namespace, Name: c.Spec.Source.ConfigMapRef.Name}, &cm); err != nil {
				t.Fatal(err)
			}
			removedSource = cm.DeepCopy()
			if err := a.statusWriter.Delete(ctx, &cm); err != nil {
				t.Fatal(err)
			}
		}
		if retainedSource && removedSource != nil && !restoredSource && state.Operation == nil {
			if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || *posts != 2 || *deletes != 1 || len(state.CredentialApplications) != 2 || len(state.ActionApplications) != 3 {
				t.Fatal("retained prune fabricated current readiness or delivery")
			}
			before := state.DeepCopy()
			for j := 0; j < 3; j++ {
				reconcile()
			}
			if *deletes != 1 || *posts != 2 || !reflect.DeepEqual(before, journalState(t, a)) {
				t.Fatal("missing source authorized further pruning")
			}
			cm := removedSource.DeepCopy()
			cm.ResourceVersion = ""
			cm.UID = ""
			if removedSource.UID == "fixture-composed-model" {
				cm.UID = "fixture-recreated-model"
			}
			if err := a.statusWriter.Create(ctx, cm); err != nil {
				t.Fatal(err)
			}
			restoredSource = true
		}
		if state != nil && state.Operation != nil && state.Operation.Phase == "Unknown" {
			sawUnknown = true
		}
		if !blocked && c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			break
		}
		if blocked && i >= 11 {
			break
		}
	}
	if retainedSource && (removedSource == nil || !restoredSource) {
		t.Fatal("retained prune source recovery not exercised")
	}
	if blocked {
		if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || *posts != 2 || *deletes > 1 {
			t.Fatal("prune replayed or falsely converged", c.Status.Conditions, *posts, *deletes)
		}
		if (mode == "prune-empty-orphan-credentials" || mode == "prune-empty-orphan-actions" || mode == "prune-conflict" || mode == "prune-empty-conflict" || mode == "prune-legacy" || mode == "prune-capacity" || mode == "prune-lost-permit") && *deletes != 0 {
			t.Fatal("cleanup bypassed pre-dispatch guard")
		}
		if fault != "" && hits != 1 {
			t.Fatal("fault was not exercised")
		}
		return
	}
	if c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("prune did not converge", c.Status.Conditions, journalState(t, a).Operation)
	}
	if mode == "prune-retain" {
		if *deletes != 0 || !reflect.DeepEqual(initial, journalState(t, a)) {
			t.Fatal("Retain performed cleanup")
		}
		return
	}
	if (mode == "prune-unknown" || mode == "prune-empty-unknown") && !sawUnknown {
		t.Fatal("original-task timeout recovery not exercised")
	}
	if fault != "" && hits != 1 {
		t.Fatal("fault was not exercised")
	}
	expected := 1
	if strings.HasPrefix(mode, "prune-empty") {
		expected = 2
	}
	if *deletes != expected || *posts != 2 || len(jobs) != 2-expected {
		t.Fatal("wrong native cleanup scope", *posts, *deletes, len(jobs))
	}
	if mode == "prune-composition-pending" && !cleanedWhileOtherPending {
		t.Fatal("ordinary pruning stranded behind unrelated active job")
	}
	retained := journalState(t, a).DeepCopy()
	if !strings.HasPrefix(mode, "prune-composition") {
		if len(retained.CredentialApplications) != 2 || len(retained.ActionApplications) != 2+expected || !reflect.DeepEqual(initial.CredentialApplications, retained.CredentialApplications) {
			t.Fatal("cleanup claimed credential delivery or dropped history")
		}
		for i := 0; i < 2; i++ {
			if !reflect.DeepEqual(initial.ActionApplications[i], retained.ActionApplications[i]) {
				t.Fatal("creator evidence overwritten")
			}
		}
	}
	for i := 0; i < 3; i++ {
		reconcile()
	}
	if *posts != 2 || *deletes != expected || !reflect.DeepEqual(retained, journalState(t, a)) {
		t.Fatal("restart replayed cleanup")
	}
	a.statusWriter = base
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	setSource(original)
	c.Spec.SecretRefs = refs
	c.Generation++
	if err := base.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 28; i++ {
		reconcile()
		if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			break
		}
	}
	if c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || *posts != 2+expected || *deletes != expected || len(jobs) != 2 {
		t.Fatal("later bound input did not recreate pruned jobs", c.Status.Conditions, *posts, *deletes)
	}
	if _, err := discoveryCredentialLineages(journalState(t, a), string(c.UID)); err != nil {
		t.Fatal("invalid retained prune/recreation chain", err)
	}
}
