// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Structural archive round trips through the real CRD/status API. Native
// observations here are synthetic; the separate live public cycle qualifies
// actual commit/body correlation and native source updates.
func TestEnvtestTemplateVersionArchivePersistence(t *testing.T) {
	ctx := context.Background()
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"templates"}
	config.Spec.Source.Inline = nil
	config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: "version-source", Key: "model.yaml"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	installWirelessFeatureQualificationAPI(t, api, "template-version-archive")(a, config)
	model := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "version-source", Namespace: config.Namespace}, Data: map[string]string{"model.yaml": projectModel}}
	if err := a.statusWriter.Create(ctx, model); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	comparison, state, resource, footprint := templateVersionCohortFixture(t)
	original := *state.ActionApplications[0].DeepCopy()
	original.Operation.ConfigUID, original.Operation.ConfigName = string(config.UID), config.Name
	original.Operation.ConfigGeneration, original.Operation.ControllerGeneration = config.Generation, nc.Generation
	original.Operation.SourceUID, original.Operation.SourceVersion = string(model.UID), model.ResourceVersion
	state.ActionApplications[0] = original
	for i := 1; i < len(state.ActionApplications); i++ {
		row := &state.ActionApplications[i]
		op := &row.Operation
		op.ConfigUID, op.ConfigName = string(config.UID), config.Name
		op.ConfigGeneration, op.ControllerGeneration = config.Generation, nc.Generation
		op.SourceUID, op.SourceVersion = string(model.UID), model.ResourceVersion
		parts := strings.Split(row.Resource, ":")
		archive := strings.HasPrefix(row.Resource, templateAppliedHistoryPrefix)
		row.Resource = templateAppliedVersionResult(op, original, parts[7], parts[len(parts)-2], parts[len(parts)-1])
		if archive {
			row.Resource = templateAppliedHistoryPrefix + strings.TrimPrefix(row.Resource, templateAppliedPrefix)
		}
	}
	state.Owners = []cvk.NetworkControllerSectionOwner{{Section: "templates", ConfigUID: string(config.UID), ConfigName: config.Name}}
	state.Operation = state.ActionApplications[len(state.ActionApplications)-1].Operation.DeepCopy()
	nc.Status.Configuration = state
	// Seed retained history through the API first, as actual earlier reconciles
	// do; do not fabricate pre-serialization operation timestamps at completion.
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	normalized, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.persistConfigurationActionReceipt(ctx, normalized); err != nil {
		t.Fatal(err)
	}
	stored, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !templateAppliedReceiptPresent(stored.Status.Configuration, stored.Status.Configuration.Operation) {
		t.Fatal("real API lost strict version receipt")
	}
	before, err := templateAppliedHistoryRecords(stored.Status.Configuration)
	if err != nil || len(before) != 1 {
		t.Fatal("real API lost archived version", err)
	}
	if matched, err := templateVersionCohortMatches(comparison, stored.Status.Configuration, resource, footprint); err != nil || !matched {
		t.Fatal("persisted version cohort failed", err)
	}
	current := *stored.Status.Configuration.ActionApplications[len(stored.Status.Configuration.ActionApplications)-1].DeepCopy()
	op := current.Operation.DeepCopy()
	op.ID = strings.Repeat("e", 32)
	op.Resource = templateApplyResource(resource, "draft")
	now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	op.SubmittedAt = &now
	op.Phase = "Verifying"
	stored.Status.Configuration.Operation = op
	if err := a.statusWriter.Status().Update(ctx, stored); err != nil {
		t.Fatal(err)
	}
	stored, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	op = stored.Status.Configuration.Operation.DeepCopy()
	op.Phase = "Verified"
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: templateAppliedResult(op, original, strings.Repeat("f", 64)), Operation: *op, VerifiedAt: &now}
	if err := templateReplaceApplied(stored.Status.Configuration, row); err != nil {
		t.Fatal(err)
	}
	stored.Status.Configuration.Operation = op
	if err := a.persistConfigurationActionReceipt(ctx, stored); err != nil {
		t.Fatal(err)
	}
	after, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	history, err := templateAppliedHistoryRecords(after.Status.Configuration)
	if err != nil || len(history) != 2 || !reflect.DeepEqual(history[0], before[0]) {
		t.Fatal("draft update corrupted real API archive", err)
	}
	if matched, err := templateVersionCohortMatches(comparison, after.Status.Configuration, resource, footprint); err != nil || !matched {
		t.Fatal("round-tripped draft discarded version ownership", err)
	}
	raw, _ := json.Marshal(after.Status.Configuration)
	if strings.Contains(string(raw), "synthetic private committed body") || strings.Contains(string(raw), "private version metadata") {
		t.Fatal("private native content entered status")
	}
	t.Log("two original strict version receipts survive real API archive/draft round trips without content disclosure")
}
