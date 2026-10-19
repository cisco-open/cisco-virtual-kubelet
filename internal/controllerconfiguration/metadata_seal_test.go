// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type metadataSealStore struct {
	client.Client
	mode    string
	updates int
}

func (s *metadataSealStore) Status() client.SubResourceWriter {
	return metadataSealWriter{s.Client.Status(), s}
}

type metadataSealWriter struct {
	client.SubResourceWriter
	store *metadataSealStore
}

func (w metadataSealWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	n, ok := obj.(*cvk.NetworkController)
	if !ok || n.Status.Configuration == nil || n.Status.Configuration.MetadataHandoff == nil {
		return w.SubResourceWriter.Update(ctx, obj, opts...)
	}
	w.store.updates++
	if w.store.mode == "conflict" && w.store.updates == 1 {
		var current cvk.NetworkController
		if err := w.store.Client.Get(ctx, client.ObjectKeyFromObject(n), &current); err != nil {
			return err
		}
		current.Status.Phase = cvk.NetworkControllerPhaseConnecting
		if err := w.SubResourceWriter.Update(ctx, &current); err != nil {
			return err
		}
	}
	if w.store.mode == "pruned" {
		n = n.DeepCopy()
		n.Status.Configuration.MetadataHandoff = nil
		obj = n
	}
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if w.store.mode == "lost-ack" && w.store.updates == 1 {
		return errors.New("seal acknowledgement lost")
	}
	return nil
}

func TestMetadataHandoffSeal(t *testing.T) {
	for _, mode := range []string{"valid", "mutate-arguments", "missing-runtime", "denied", "denied-after", "missing-policy", "foreign-existing", "lost-ack", "conflict", "pruned"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, _ := bootstrapFenceFixture(t)
			_ = ops.AddToScheme(c.Scheme())
			nc.Status.Configuration.Operation.Action = "Delete"
			nc.Status.Configuration.Operation.TargetSerial = ""
			nc.Status.Configuration.Operation.Resource = "pnp-remove:v2:native-pnp:" + strings.Repeat("b", 64)
			if err := c.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
				t.Fatal(err)
			}
			policy, binding, ns := metadataProtectionFixture(t)
			if mode != "missing-policy" {
				for _, o := range []client.Object{policy, binding, ns} {
					o.SetResourceVersion("")
					if err := c.Create(ctx, o); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "foreign-existing" {
				nc.Status.Configuration.MetadataHandoff = &cvk.NetworkControllerConfigurationMetadataHandoff{Protocol: "future"}
				if err := c.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			store := &metadataSealStore{Client: c, mode: mode}
			calls := 0
			runtime := MetadataHandoffRuntimeCheck(func(_ context.Context, n *cvk.NetworkController, o MetadataScopeWorkerObservation, p AdmissionProtection) error {
				calls++
				if mode == "denied" || mode == "denied-after" && calls > 1 {
					return errors.New("metadata settlement runtime unavailable")
				}
				if mode == "mutate-arguments" {
					n.Status.Configuration.Operation.ID = "changed"
					o.PhysicalIdentities[0] = "foreign"
					p.PolicyDigest = "wrong"
				}
				return nil
			})
			if mode == "missing-runtime" {
				runtime = nil
			}
			before := nc.DeepCopy()
			seal, err := EnsureMetadataHandoff(ctx, c, store, nc, policy.Name, bootstrapTestManager, "metadata-leases", []string{"bootstrap123", "second123"}, runtime, time.Now())
			good := mode == "valid" || mode == "mutate-arguments"
			if (err == nil) != good {
				t.Fatal("incorrect seal result", err)
			}
			var current cvk.NetworkController
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &current); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, nc) || !reflect.DeepEqual(before.Status.Configuration.Operation, current.Status.Configuration.Operation) || !reflect.DeepEqual(before.Status.Configuration.ActionApplications, current.Status.Configuration.ActionApplications) {
				t.Fatal("seal changed input, operation or effect history")
			}
			if mode == "missing-runtime" || mode == "denied" || mode == "missing-policy" || mode == "foreign-existing" {
				if store.updates != 0 {
					t.Fatal("refused before seal but wrote status")
				}
				return
			}
			if mode == "pruned" {
				if seal != nil || current.Status.Configuration.MetadataHandoff != nil {
					t.Fatal("pruned seal accepted")
				}
				return
			}
			if mode == "conflict" {
				if current.Status.Configuration.MetadataHandoff != nil || current.Status.Phase != cvk.NetworkControllerPhaseConnecting {
					t.Fatal("stale CAS overwrote concurrent state")
				}
			} else if current.Status.Configuration.MetadataHandoff == nil {
				t.Fatal("committed custody was lost")
			}
			if mode == "denied-after" {
				return
			}
			// A fresh snapshot recovers a lost acknowledgement or CAS conflict, and
			// observes an existing seal idempotently without replacing its identity.
			recovered, err := EnsureMetadataHandoff(ctx, c, store, &current, policy.Name, bootstrapTestManager, "metadata-leases", []string{"second123", "bootstrap123"}, runtime, time.Now())
			if err != nil || recovered == nil {
				t.Fatal("fresh-snapshot seal recovery", err)
			}
			if recovered.WorkerUsername != "system:serviceaccount:lab:connector" || recovered.WorkerPod.UID != "pod-uid" || recovered.Operation.Phase != "Prepared" || recovered.RefusedAt != nil || recovered.ReleasedAt != nil {
				t.Fatal("seal manufactured an outcome or wrong actor")
			}
			if good && store.updates != 1 || mode == "lost-ack" && store.updates != 1 || mode == "conflict" && store.updates != 2 {
				t.Fatal("existing custody was rewritten", store.updates)
			}
		})
	}
}
