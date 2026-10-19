//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	coordination "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Real admission and UID/RV deletion, but staged release digests: no native
// completion, actual metadata runtime claim or production manager release runs.
func qualifyMarkedMetadataFenceAdmission(t *testing.T, ctx context.Context, admin, manager, worker client.Client, legacy *coordination.Lease) {
	t.Helper()
	var record bootstrapFenceRecord
	if err := json.Unmarshal([]byte(legacy.Annotations[bootstrapFenceBindingAnnotation]), &record); err != nil {
		t.Fatal(err)
	}
	record.Identity.PhysicalIdentity = "metadata123"
	candidate, err := bootstrapFenceCandidate(legacy.Namespace, record.OwnerNamespace, &record.Identity, &record.Worker, record.Installation, &record.Protection)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Labels[metadataFenceLabel] = MetadataFenceProtocol
	deny := func(err error) {
		t.Helper()
		if !apierrors.IsForbidden(err) && !apierrors.IsInvalid(err) {
			t.Fatalf("expected fence admission denial: %v", err)
		}
	}
	t.Run("metadata-worker-create", func(t *testing.T) { deny(worker.Create(ctx, candidate.DeepCopy(), client.DryRunAll)) })
	t.Run("metadata-cannot-adopt-unmarked", func(t *testing.T) {
		copy := legacy.DeepCopy()
		copy.Labels[metadataFenceLabel] = MetadataFenceProtocol
		deny(manager.Update(ctx, copy, client.DryRunAll))
	})
	t.Run("metadata-create-already-released", func(t *testing.T) {
		copy := candidate.DeepCopy()
		copy.Annotations[metadataFenceReleaseAnnotation] = strings.Repeat("a", 64)
		deny(manager.Create(ctx, copy, client.DryRunAll))
	})
	t.Run("metadata-create-unknown-marker", func(t *testing.T) {
		copy := candidate.DeepCopy()
		copy.Labels[metadataFenceLabel] = "future"
		deny(manager.Create(ctx, copy, client.DryRunAll))
	})
	if err := manager.Create(ctx, candidate); err != nil {
		t.Fatal("metadata manager fence creation", err)
	}
	fetch := func() *coordination.Lease {
		t.Helper()
		var l coordination.Lease
		if err := admin.Get(ctx, client.ObjectKeyFromObject(candidate), &l); err != nil {
			t.Fatal(err)
		}
		return &l
	}
	release := func(l *coordination.Lease) {
		l.Annotations[metadataFenceReleaseAnnotation] = strings.Repeat("a", 64)
		l.Finalizers = nil
	}
	t.Run("metadata-manager-delete-before-release", func(t *testing.T) { deny(manager.Delete(ctx, fetch(), client.DryRunAll)) })
	for _, mode := range []string{"worker", "binding", "holder", "marker", "owner", "retention", "extra-annotation", "invalid-proof", "retained-finalizer"} {
		t.Run("metadata-release-"+mode, func(t *testing.T) {
			copy := fetch()
			release(copy)
			switch mode {
			case "worker":
				deny(worker.Update(ctx, copy, client.DryRunAll))
				return
			case "binding":
				copy.Annotations[bootstrapFenceBindingAnnotation] = "changed"
			case "holder":
				value := "controller-bootstrap/" + strings.Repeat("b", 64)
				copy.Spec.HolderIdentity = &value
			case "marker":
				delete(copy.Labels, metadataFenceLabel)
			case "owner":
				copy.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "other", UID: "other"}}
			case "retention":
				delete(copy.Annotations, "topology.cisco.vk/retain-lease")
			case "extra-annotation":
				copy.Annotations["unrelated"] = "changed"
			case "invalid-proof":
				copy.Annotations[metadataFenceReleaseAnnotation] = "invalid"
			case "retained-finalizer":
				copy.Finalizers = []string{bootstrapFenceFinalizer}
			}
			deny(manager.Update(ctx, copy, client.DryRunAll))
		})
	}
	marked := fetch()
	release(marked)
	if err := manager.Update(ctx, marked); err != nil {
		t.Fatal("metadata manager release marker", err)
	}
	t.Run("metadata-release-proof-immutable", func(t *testing.T) {
		copy := fetch()
		copy.Annotations[metadataFenceReleaseAnnotation] = strings.Repeat("b", 64)
		deny(manager.Update(ctx, copy, client.DryRunAll))
	})
	t.Run("metadata-worker-delete-released", func(t *testing.T) { deny(worker.Delete(ctx, fetch(), client.DryRunAll)) })
	released := fetch()
	if err := manager.Update(ctx, released); err != nil {
		t.Fatal("released manager no-op", err)
	}
	released = fetch()
	uid, rv := released.UID, released.ResourceVersion
	if err := manager.Delete(ctx, released, client.Preconditions{UID: &uid, ResourceVersion: &rv}); err != nil {
		t.Fatal("metadata exact release deletion", err)
	}
	var absent coordination.Lease
	if err := admin.Get(ctx, client.ObjectKeyFromObject(released), &absent); !apierrors.IsNotFound(err) {
		t.Fatal("released fence retained", err)
	}
	replacement := candidate.DeepCopy()
	replacement.ObjectMeta = metav1.ObjectMeta{Name: candidate.Name, Namespace: candidate.Namespace, Labels: candidate.Labels, Annotations: candidate.Annotations, Finalizers: []string{bootstrapFenceFinalizer}}
	delete(replacement.Annotations, metadataFenceReleaseAnnotation)
	if err := manager.Create(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	release(replacement)
	if err := manager.Update(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	t.Run("metadata-stale-uid-cannot-delete-replacement", func(t *testing.T) {
		err := manager.Delete(ctx, replacement.DeepCopy(), client.Preconditions{UID: &uid})
		if !apierrors.IsConflict(err) {
			t.Fatal("stale fence UID deletion was not rejected", err)
		}
	})
	current := fetch()
	newUID, newRV := current.UID, current.ResourceVersion
	if err := manager.Delete(ctx, current, client.Preconditions{UID: &newUID, ResourceVersion: &newRV}); err != nil {
		t.Fatal(err)
	}
	t.Log("marked metadata fence manager-only create/release/delete and replacement UID protection passed; unmarked v1 immutable; release proof staged, no native operation")
}
