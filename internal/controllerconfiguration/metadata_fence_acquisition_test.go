// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	admission "k8s.io/api/admissionregistration/v1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type metadataAcquisitionStore struct {
	client.Client
	mode             string
	creates, updates int
	source           client.ObjectKey
}

func (s *metadataAcquisitionStore) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	l, ok := obj.(*coordination.Lease)
	if !ok {
		return errors.New("acquisition wrote a non-Lease")
	}
	s.creates++
	if s.mode == "create-refused" {
		return errors.New("create refused")
	}
	if s.mode != "missing-uid" {
		l.UID = types.UID(fmt.Sprintf("original-%d", s.creates))
	}
	if s.mode == "foreign-race" || s.mode == "pruned-binding" {
		l.Annotations[bootstrapFenceBindingAnnotation] = "foreign"
	}
	if err := s.Client.Create(ctx, l, opts...); err != nil {
		return err
	}
	switch s.mode {
	case "create-lost-ack":
		return errors.New("create response lost")
	case "create-race", "foreign-race":
		return apierrors.NewAlreadyExists(schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"}, l.Name)
	case "late-device":
		return s.Client.Create(ctx, &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "late-device", Namespace: "other-tenant"}, Spec: cvk.DeviceSpec{PhysicalIdentity: "bootstrap123"}})
	case "late-source":
		var source config.NetworkControllerConfig
		if err := s.Client.Get(ctx, s.source, &source); err != nil {
			return err
		}
		source.Generation++
		return s.Client.Update(ctx, &source)
	}
	return nil
}
func (s *metadataAcquisitionStore) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := s.Client.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if l, ok := obj.(*coordination.Lease); ok && (s.mode == "published-replacement" || s.mode == "replaced-after-create" && s.creates > 0) {
		l.UID = "replacement"
	}
	return nil
}
func (s *metadataAcquisitionStore) Status() client.SubResourceWriter {
	return metadataAcquisitionStatus{s.Client.Status(), s}
}

type metadataAcquisitionStatus struct {
	client.SubResourceWriter
	store *metadataAcquisitionStore
}

func (w metadataAcquisitionStatus) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	n, ok := obj.(*cvk.NetworkController)
	if !ok {
		return errors.New("acquisition wrote a non-controller status")
	}
	w.store.updates++
	if w.store.mode == "status-conflict" && w.store.updates == 1 || w.store.mode == "ready-conflict" && n.Status.Configuration.MetadataHandoff.ReadyAt != nil {
		var concurrent cvk.NetworkController
		if err := w.store.Client.Get(ctx, client.ObjectKeyFromObject(n), &concurrent); err != nil {
			return err
		}
		concurrent.Status.Phase = cvk.NetworkControllerPhaseConnecting
		if err := w.SubResourceWriter.Update(ctx, &concurrent); err != nil {
			return err
		}
	}
	if w.store.mode == "ready-pruned" {
		n = n.DeepCopy()
		n.Status.Configuration.MetadataHandoff.ReadyAt = nil
	}
	if w.store.mode == "status-pruned" {
		n = n.DeepCopy()
		n.Status.Configuration.MetadataHandoff.Fences = nil
	}
	if err := w.SubResourceWriter.Update(ctx, n, opts...); err != nil {
		return err
	}
	if w.store.mode == "status-lost-ack" && w.store.updates == 1 || w.store.mode == "ready-lost-ack" && n.Status.Configuration.MetadataHandoff.ReadyAt != nil {
		return errors.New("status response lost")
	}
	return nil
}

func metadataAcquisitionFixture(t *testing.T) (client.Client, *cvk.NetworkController, *config.NetworkControllerConfig, string) {
	t.Helper()
	ctx := context.Background()
	c, nc, source := bootstrapFenceFixture(t)
	_ = ops.AddToScheme(c.Scheme())
	op := nc.Status.Configuration.Operation
	op.Action = "Delete"
	op.TargetSerial = ""
	op.Resource = "pnp-remove:v2:native-pnp:" + strings.Repeat("b", 64)
	if err := c.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	policy, binding, _ := metadataProtectionFixture(t)
	for i := range policy.Spec.Variables {
		if policy.Spec.Variables[i].Name == "leaseNamespace" {
			policy.Spec.Variables[i].Expression = strings.ReplaceAll(policy.Spec.Variables[i].Expression, "metadata-leases", "leases")
		}
	}
	for _, obj := range []client.Object{policy, binding} {
		obj.SetResourceVersion("")
		if err := c.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	compatible := func(context.Context, *cvk.NetworkController, MetadataScopeWorkerObservation, AdmissionProtection) error {
		return nil
	}
	if _, err := EnsureMetadataHandoff(ctx, c, c, nc, policy.Name, bootstrapTestManager, "leases", []string{"second123", "bootstrap123"}, compatible, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	return c, nc, source, policy.Name
}

func TestAcquireMetadataFenceCohort(t *testing.T) {
	for _, mode := range []string{"valid", "mutate-arguments", "missing-runtime", "denied", "post-denied", "policy-drift", "worker-drift", "callback-source-drift", "foreign-existing", "create-refused", "create-lost-ack", "create-race", "foreign-race", "pruned-binding", "missing-uid", "replaced-after-create", "status-lost-ack", "status-conflict", "status-pruned", "ready-lost-ack", "ready-conflict", "ready-pruned", "late-device", "late-source", "published-replacement"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, source, policy := metadataAcquisitionFixture(t)
			before := nc.DeepCopy()
			if mode == "foreign-existing" {
				name, _ := BootstrapFenceName("bootstrap123")
				if err := c.Create(ctx, &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "leases", UID: "foreign"}}); err != nil {
					t.Fatal(err)
				}
			}
			store := &metadataAcquisitionStore{Client: c, mode: mode, source: client.ObjectKeyFromObject(source)}
			calls := 0
			compatible := MetadataHandoffRuntimeCheck(func(_ context.Context, n *cvk.NetworkController, o MetadataScopeWorkerObservation, p AdmissionProtection) error {
				calls++
				if mode == "denied" || mode == "post-denied" && calls > 1 {
					return errors.New("runtime denied")
				}
				if mode == "policy-drift" {
					var p admission.ValidatingAdmissionPolicy
					if err := c.Get(ctx, client.ObjectKey{Name: policy}, &p); err != nil {
						return err
					}
					ignore := admission.Ignore
					p.Spec.FailurePolicy = &ignore
					return c.Update(ctx, &p)
				}
				if mode == "worker-drift" {
					var p core.Pod
					if err := c.Get(ctx, client.ObjectKey{Namespace: n.Namespace, Name: n.Status.Configuration.MetadataHandoff.WorkerPod.Name}, &p); err != nil {
						return err
					}
					p.Status.Phase = core.PodFailed
					return c.Status().Update(ctx, &p)
				}
				if mode == "callback-source-drift" {
					var intent config.NetworkControllerConfig
					if err := c.Get(ctx, client.ObjectKeyFromObject(source), &intent); err != nil {
						return err
					}
					intent.Generation++
					return c.Update(ctx, &intent)
				}
				if mode == "mutate-arguments" {
					n.Status.Configuration.Operation.ID = "foreign"
					n.Status.Configuration.MetadataHandoff.BindingDigest = "foreign"
					o.PhysicalIdentities[0] = "foreign"
					p.PolicyDigest = "foreign"
				}
				return nil
			})
			if mode == "missing-runtime" {
				compatible = nil
			}
			if mode == "published-replacement" {
				store.mode = "valid"
			}
			result, err := AcquireMetadataFenceCohort(ctx, store, store, nc, bootstrapFenceTestInstallation(), policy, []string{"second123", "bootstrap123"}, compatible, time.Now())
			good := mode == "valid" || mode == "mutate-arguments" || mode == "create-race" || mode == "published-replacement"
			if (err == nil) != good {
				t.Fatal("incorrect acquisition result", err)
			}
			if !reflect.DeepEqual(before, nc) {
				t.Fatal("acquisition mutated input")
			}
			var current cvk.NetworkController
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &current); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(current.Status.Configuration.Operation, before.Status.Configuration.Operation) || !reflect.DeepEqual(current.Status.Configuration.ActionApplications, before.Status.Configuration.ActionApplications) {
				t.Fatal("acquisition changed journal or fabricated effect")
			}
			if mode == "policy-drift" || mode == "worker-drift" || mode == "callback-source-drift" || mode == "foreign-existing" {
				if store.creates != 0 || store.updates != 0 {
					t.Fatal("drifting/foreign prerequisite wrote custody")
				}
				return
			}
			if mode == "missing-runtime" || mode == "denied" || mode == "create-refused" {
				if store.updates != 0 || mode != "create-refused" && store.creates != 0 {
					t.Fatal("denied acquisition wrote")
				}
				return
			}
			if mode == "late-device" || mode == "late-source" || mode == "post-denied" {
				if len(current.Status.Configuration.MetadataHandoff.Fences) != 1 || store.creates != 1 {
					t.Fatal("post-acquisition denial lost original custody")
				}
				return
			}
			if mode == "foreign-race" || mode == "pruned-binding" || mode == "missing-uid" || mode == "replaced-after-create" {
				if len(current.Status.Configuration.MetadataHandoff.Fences) != 0 || store.updates != 0 {
					t.Fatal("malformed/replaced original was published")
				}
				return
			}
			if mode == "published-replacement" {
				store.mode = mode
				creates, updates := store.creates, store.updates
				if _, err := AcquireMetadataFenceCohort(ctx, store, store, &current, bootstrapFenceTestInstallation(), policy, []string{"bootstrap123", "second123"}, compatible, time.Now()); err == nil || store.creates != creates || store.updates != updates {
					t.Fatal("recorded replacement accepted or rewritten", err)
				}
				return
			}
			if good {
				if result == nil || len(result.Status.Configuration.MetadataHandoff.Fences) != 2 || store.creates != 2 || store.updates != 3 {
					t.Fatal("complete original cohort was not durably published", store.creates, store.updates)
				}
				if _, err := ReadMetadataFenceCohort(ctx, c, &current); err != nil {
					t.Fatal(err)
				}
				if err := CheckUnmanagedMetadataScope(ctx, c, &current, "leases", []string{"bootstrap123", "second123"}); err == nil {
					t.Fatal("public absence observer adopted own retained fences")
				}
			}
			// Fresh-snapshot recovery after partial create/status responses never
			// creates another object for a retained original. Known UIDs remain exact.
			store.mode = "valid"
			creates := store.creates
			recovered, err := AcquireMetadataFenceCohort(ctx, store, store, &current, bootstrapFenceTestInstallation(), policy, []string{"bootstrap123", "second123"}, compatible, time.Now())
			if err != nil || recovered == nil || len(recovered.Status.Configuration.MetadataHandoff.Fences) != 2 || store.creates != 2 {
				t.Fatal("original partial acquisition recovery failed", err, creates, store.creates)
			}
			creates, updates := store.creates, store.updates
			if _, err := AcquireMetadataFenceCohort(ctx, store, store, recovered, bootstrapFenceTestInstallation(), policy, []string{"second123", "bootstrap123"}, compatible, time.Now()); err != nil || store.creates != creates || store.updates != updates {
				t.Fatal("completed acquisition was not idempotent", err)
			}
		})
	}
}
