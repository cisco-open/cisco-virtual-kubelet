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

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

// The actual CRD/API must preserve the complete receipt emitted by the bridge.
// Native service, grants, device results and semantic proof are fixtures. This
// schema-envelope check does not assert live group admission or installed actors.
func TestEnvtestSDATransitApplicationEnvelope(t *testing.T) {
	ctx := context.Background()
	a, config, source, want, op, members, _, now := sdaTransitDeviceEvidenceFixture(t)
	authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
	readySDATransitApplicationEvidence(t, a, func() error {
		_, err := a.collectSDATransitDeviceEvidence(ctx, config, source, want, op, members, authorize, now)
		return err
	}, members, now)
	proof := func(context.Context, sdaTransitIntent, *cvk.NetworkControllerConfigurationOperation, []contract.GroupTargetIdentity, time.Time, map[string]sdaTransitDeviceEvidence) error {
		return nil
	}
	if err := a.recordSDATransitApplication(ctx, config, op.ID, want, members, authorize, proof, now); err != nil {
		t.Fatal(err)
	}
	state := journalState(t, a).DeepCopy()
	original, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	api := qualificationAPIServerClient(t)
	const namespace = "sda-application-envelope"
	if err := api.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
		t.Fatal(err)
	}
	stored := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: original.Name, Namespace: namespace}, Spec: original.Spec}
	if err := api.Create(ctx, stored); err != nil {
		t.Fatal(err)
	}
	stored.Status.Configuration = state.DeepCopy()
	if err := api.Status().Update(ctx, stored); err != nil {
		t.Fatal(err)
	}
	var read cvk.NetworkController
	if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(stored), &read); err != nil {
		t.Fatal(err)
	}
	if read.UID == "" || read.UID == original.UID || !reflect.DeepEqual(read.Status.Configuration, state) {
		t.Fatal("real API changed receipt envelope")
	}
	raw, err := json.Marshal(read.Status.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SYNTHETIC SEMANTIC FIXTURE") || strings.Contains(string(raw), "Processor board ID") {
		t.Fatal("private device output leaked into retained receipt")
	}
	if read.Status.Configuration.Operation.Phase != "Verified" || len(read.Status.Configuration.ActionApplications) != 1 || read.Status.Configuration.ActionApplications[0].Operation.PhysicalGroup == nil {
		t.Fatal("incomplete application envelope")
	}
	t.Log("actual API preserves generated Verified operation and complete task/source/group/claim receipt without raw device output; physical actors, native mutation and semantic proof remain simulated")
}
