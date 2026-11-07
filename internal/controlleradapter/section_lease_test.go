// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controlleradapter

import (
	"context"
	coordinationv1 "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
	"time"
)

func TestSectionOwnershipAndWriteAheadFence(t *testing.T) {
	scheme := runtime.NewScheme()
	coordinationv1.AddToScheme(scheme)
	k := fake.NewClientBuilder().WithScheme(scheme).Build()
	now := time.Now()
	s := &SectionLease{Client: k, Namespace: "lab", ControllerUID: "endpoint", ConfigUID: "config", Scope: "global", Section: "templates", Session: "worker1", Now: func() time.Time { return now }}
	ctx := context.Background()
	lease, j, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	j["object"] = MutationReceipt{State: "Dispatched"}
	if err = s.Save(ctx, lease, j); err != nil {
		t.Fatal(err)
	}
	other := *s
	other.ConfigUID = "different"
	if _, _, err = other.Acquire(ctx); err == nil {
		t.Fatal("other config acquired")
	}
	other = *s
	other.Session = "worker2"
	now = now.Add(2 * time.Minute)
	if _, _, err = other.Acquire(ctx); err == nil {
		t.Fatal("unresolved dispatch allowed takeover")
	}
	lease, j, err = s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	j["object"] = MutationReceipt{State: "Verified", ID: "remote"}
	if err = s.Save(ctx, lease, j); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	next, _, err := other.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Save(ctx, lease, j); err == nil {
		t.Fatal("stale holder wrote")
	}
	if *next.Spec.HolderIdentity != "worker2" {
		t.Fatal("takeover")
	}
}
