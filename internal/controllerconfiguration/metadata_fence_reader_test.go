// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type metadataFenceReadDrift struct {
	client.Reader
	mode   string
	counts map[string]int
}

func (r *metadataFenceReadDrift) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if l, ok := obj.(*coordination.Lease); ok {
		r.counts[key.Name]++
		if r.mode == "missing" {
			return apierrors.NewNotFound(schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"}, key.Name)
		}
		if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
			return err
		}
		switch r.mode {
		case "replacement":
			l.UID = "replacement"
		case "late-replacement":
			if r.counts[key.Name] > 1 {
				l.UID = "replacement"
			}
		case "unmarked":
			delete(l.Labels, metadataFenceLabel)
		case "release":
			l.Annotations[metadataFenceReleaseAnnotation] = strings.Repeat("d", 64)
			l.Finalizers = nil
		case "binding", "worker":
			var record bootstrapFenceRecord
			_ = json.Unmarshal([]byte(l.Annotations[bootstrapFenceBindingAnnotation]), &record)
			if r.mode == "binding" {
				record.Identity.OperationDigest = strings.Repeat("e", 64)
			} else {
				record.Worker.Pod.UID = "replacement"
			}
			raw, _ := json.Marshal(record)
			l.Annotations[bootstrapFenceBindingAnnotation] = string(raw)
		case "extra-annotation":
			l.Annotations["other"] = "changed"
		}
		return nil
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}

func TestReadMetadataFenceCohort(t *testing.T) {
	for _, mode := range []string{"valid", "no-references", "partial-cohort", "duplicate-identity", "duplicate-uid", "wrong-name", "missing", "replacement", "late-replacement", "unmarked", "release", "binding", "worker", "extra-annotation", "stale-status", "new-desired-generation", "future-original-generation"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, _ := bootstrapFenceFixture(t)
			identity, err := ReadBootstrapIdentity(ctx, c, nc)
			if err != nil {
				t.Fatal(err)
			}
			worker := bootstrapFenceFixtureWorker(t, c, nc)
			protection := bootstrapFenceFixtureProtection(t, c)
			op := nc.Status.Configuration.Operation.DeepCopy()
			op.Action = "Delete"
			op.TargetSerial = ""
			op.Phase = "Prepared"
			op.SubmittedAt = nil
			op.Resource = "pnp-remove:v2:" + op.TargetID + ":" + strings.Repeat("a", 64)
			h := &cvk.NetworkControllerConfigurationMetadataHandoff{Protocol: cvk.NetworkControllerMetadataHandoffProtocol, BindingDigest: strings.Repeat("b", 64), LeaseNamespace: "leases", WorkerPod: worker.Pod, WorkerUsername: worker.Username, Operation: *op, PhysicalIdentities: []string{"serial456", "serial123"}}
			for _, serial := range h.PhysicalIdentities {
				i := *identity
				i.PhysicalIdentity = serial
				i.OperationDigest = h.BindingDigest
				candidate, err := bootstrapFenceCandidate("leases", nc.Namespace, &i, worker, bootstrapFenceTestInstallation(), protection)
				if err != nil {
					t.Fatal(err)
				}
				candidate.Labels[metadataFenceLabel] = MetadataFenceProtocol
				candidate.UID = types.UID("original-" + serial)
				if err := c.Create(ctx, candidate); err != nil {
					t.Fatal(err)
				}
				h.Fences = append(h.Fences, cvk.NetworkControllerMetadataFenceReference{PhysicalIdentity: serial, Name: candidate.Name, UID: string(candidate.UID)})
			}
			switch mode {
			case "new-desired-generation":
				nc.Spec.Paused = true
				nc.Generation++
				if err := c.Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "future-original-generation":
				h.Operation.ControllerGeneration = nc.Generation + 1
			case "no-references":
				h.Fences = nil
			case "partial-cohort":
				h.Fences = h.Fences[:1]
			case "duplicate-identity":
				h.Fences[1] = h.Fences[0]
			case "duplicate-uid":
				h.Fences[1].UID = h.Fences[0].UID
			case "wrong-name":
				h.Fences[0].Name = "cvk-bootstrap-" + strings.Repeat("f", 32)
			}
			nc.Status.Configuration.Operation = op
			nc.Status.Configuration.MetadataHandoff = h
			if err := c.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
				t.Fatal(err)
			}
			if mode == "stale-status" {
				fresh := nc.DeepCopy()
				fresh.Status.Configuration.Operation.Phase = "Unknown"
				if err := c.Status().Update(ctx, fresh); err != nil {
					t.Fatal(err)
				}
			}
			reader := &metadataFenceReadDrift{Reader: c, mode: mode, counts: map[string]int{}}
			leases, err := ReadMetadataFenceCohort(ctx, reader, nc)
			if mode != "valid" && mode != "new-desired-generation" {
				if err == nil || leases != nil {
					t.Fatal("changed/incomplete original custody accepted", err)
				}
				return
			}
			if err != nil || len(leases) != 2 {
				t.Fatal("original complete cohort rejected", err)
			}
			first, _ := BootstrapFenceName("serial123")
			if leases[0].Name != first {
				t.Fatal("cohort not canonical ordered")
			}
			leases[0].Annotations[bootstrapFenceBindingAnnotation] = "caller mutation"
			fresh, err := ReadMetadataFenceCohort(ctx, c, nc)
			if err != nil || fresh[0].Annotations[bootstrapFenceBindingAnnotation] == "caller mutation" {
				t.Fatal("reader leaked mutable retained state", err)
			}
		})
	}
}
