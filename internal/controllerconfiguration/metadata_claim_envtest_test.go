//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Actual actor admission and original journal CAS; compatibility callbacks and
// worker readiness are staged. No product plan, native API or device is exercised.
func qualifyMetadataClaimOnAPI(t *testing.T, ctx context.Context, cfg *rest.Config, admin client.Client, nc *cvk.NetworkController, installation BootstrapFenceInstallation, policyName string) {
	t.Helper()
	h := nc.Status.Configuration.MetadataHandoff
	if h == nil || h.ReadyAt == nil || len(h.Fences) != len(h.PhysicalIdentities) {
		t.Fatal("manager did not attest complete custody")
	}
	workerClient := func(uid string) client.Client {
		cc := rest.CopyConfig(cfg)
		cc.Impersonate = rest.ImpersonationConfig{UserName: h.WorkerUsername, Groups: []string{"system:masters"}, Extra: map[string][]string{"authentication.kubernetes.io/pod-name": {h.WorkerPod.Name}, "authentication.kubernetes.io/pod-uid": {uid}}}
		c, err := client.New(cc, client.Options{Scheme: admin.Scheme()})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	authorize := func(context.Context, *cvk.NetworkController) error { return nil }
	if permit, err := ClaimMetadataOnce(ctx, admin, workerClient("replaced-pod"), nc, h.WorkerPod, installation, policyName, authorize, time.Now()); permit != nil || (!apierrors.IsForbidden(err) && !apierrors.IsInvalid(err)) || !strings.Contains(err.Error(), policyName) {
		t.Fatal("actual replaced caller claimed original metadata", err)
	}
	var unclaimed cvk.NetworkController
	if err := admin.Get(ctx, client.ObjectKeyFromObject(nc), &unclaimed); err != nil {
		t.Fatal(err)
	}
	if unclaimed.Status.Configuration.Operation.Phase != "Prepared" {
		t.Fatal("rejected actor changed journal")
	}
	worker := workerClient(h.WorkerPod.UID)
	permit, err := ClaimMetadataOnce(ctx, admin, worker, &unclaimed, h.WorkerPod, installation, policyName, authorize, time.Now())
	if err != nil || permit == nil {
		t.Fatal("actual bound worker could not claim ready operation", err)
	}
	var claimed cvk.NetworkController
	if err := admin.Get(ctx, client.ObjectKeyFromObject(nc), &claimed); err != nil {
		t.Fatal(err)
	}
	if claimed.Status.Configuration.Operation.Phase != "Submitting" || claimed.Status.Configuration.Operation.SubmittedAt == nil || claimed.Status.Configuration.MetadataHandoff.ReadyAt == nil {
		t.Fatal("original journal claim was not retained")
	}
	if p, err := ClaimMetadataOnce(ctx, admin, worker, &claimed, h.WorkerPod, installation, policyName, authorize, time.Now()); p != nil || err == nil {
		t.Fatal("persisted claim reconstructed process authority", err)
	}
	clone := *permit
	if err := permit.Check(ctx, time.Now()); err != nil {
		t.Fatal("original claim check failed", err)
	}
	if err := clone.Check(ctx, time.Now()); err == nil {
		t.Fatal("copied process permit replayed")
	}
	t.Log("actual Pod-bound worker alone claimed original ready journal; replaced caller denied; persisted Submitting cannot mint a permit; copied capability spent once; native product preflight/execution not qualified")
}
