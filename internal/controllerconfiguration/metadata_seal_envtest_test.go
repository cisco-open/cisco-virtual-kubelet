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
	admission "k8s.io/api/admissionregistration/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The isolated API stores real source/worker/policy objects. Worker readiness,
// compiler status and remaining settlement compatibility are explicitly staged.
func qualifyMetadataSealOnAPI(t *testing.T, ctx context.Context, cfg *rest.Config, admin client.Client, nc *cvk.NetworkController, observation *MetadataScopeWorkerObservation) {
	t.Helper()
	if err := admission.AddToScheme(admin.Scheme()); err != nil {
		t.Fatal(err)
	}
	policy, binding, namespace := metadataProtectionFixture(t)
	policy.UID = ""
	policy.ResourceVersion = ""
	policy.Generation = 0
	policy.Status = admission.ValidatingAdmissionPolicyStatus{}
	binding.UID = ""
	binding.ResourceVersion = ""
	binding.Generation = 0
	namespace.UID = ""
	namespace.ResourceVersion = ""
	for _, object := range []client.Object{namespace, policy, binding} {
		if err := admin.Create(ctx, object); err != nil {
			t.Fatal(err)
		}
	}
	managerCfg := rest.CopyConfig(cfg)
	managerCfg.Impersonate = rest.ImpersonationConfig{UserName: bootstrapTestManager, Groups: []string{"system:masters"}}
	manager, err := client.New(managerCfg, client.Options{Scheme: admin.Scheme()})
	if err != nil {
		t.Fatal(err)
	}
	// An unbound caller with broad RBAC must be refused before the manager seal.
	denied := nc.DeepCopy()
	denied.Status.Configuration.MetadataHandoff = &cvk.NetworkControllerConfigurationMetadataHandoff{Protocol: cvk.NetworkControllerMetadataHandoffProtocol, BindingDigest: observation.BindingDigest, LeaseNamespace: "metadata-leases", WorkerUsername: observation.Username, WorkerPod: observation.Pod, PhysicalIdentities: append([]string(nil), observation.PhysicalIdentities...), Operation: *nc.Status.Configuration.Operation.DeepCopy()}
	deadline := time.Now().Add(15 * time.Second)
	for {
		err := admin.Status().Update(ctx, denied.DeepCopy(), client.DryRunAll)
		if err != nil && (apierrors.IsForbidden(err) || apierrors.IsInvalid(err)) && strings.Contains(err.Error(), policy.Name) {
			break
		}
		if err != nil || time.Now().After(deadline) {
			t.Fatal("metadata admission not active for seal qualification", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := admin.Get(ctx, client.ObjectKeyFromObject(policy), policy); err != nil {
		t.Fatal(err)
	}
	policy.Status.ObservedGeneration = policy.Generation
	policy.Status.TypeChecking = &admission.TypeChecking{}
	if err := admin.Status().Update(ctx, policy); err != nil {
		t.Fatal(err)
	}
	writes := &metadataSealStore{Client: manager, mode: "lost-ack"}
	calls := 0
	compatibility := MetadataHandoffRuntimeCheck(func(context.Context, *cvk.NetworkController, MetadataScopeWorkerObservation, AdmissionProtection) error {
		calls++
		return nil
	})
	if _, err := EnsureMetadataHandoff(ctx, admin, writes, nc, policy.Name, bootstrapTestManager, "metadata-leases", observation.PhysicalIdentities, nil, time.Now()); err == nil || writes.updates != 0 {
		t.Fatal("missing settlement runtime wrote custody")
	}
	// Commit through real manager admission, then simulate only loss of the
	// response in the client. Reconstruct from a new authoritative snapshot.
	if _, err := EnsureMetadataHandoff(ctx, admin, writes, nc, policy.Name, bootstrapTestManager, "metadata-leases", observation.PhysicalIdentities, compatibility, time.Now()); err == nil {
		t.Fatal("lost seal acknowledgement not surfaced")
	}
	if err := admin.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	seal, err := EnsureMetadataHandoff(ctx, admin, writes, nc, policy.Name, bootstrapTestManager, "metadata-leases", observation.PhysicalIdentities, compatibility, time.Now())
	if err != nil || seal == nil || seal.WorkerPod != observation.Pod || seal.BindingDigest != observation.BindingDigest || seal.WorkerUsername != observation.Username || seal.RefusedAt != nil || seal.ReleasedAt != nil || writes.updates != 1 || calls < 2 {
		t.Fatal("real API seal recovery/idempotence", err, writes.updates)
	}
	t.Log("real manager seal CAS retained exact actor/source/cohort and recovered a lost client acknowledgement without another write; settlement compatibility callback simulated; no fences/native calls")
	qualifyMetadataAcquisitionOnAPI(t, ctx, admin, manager, cfg, nc, policy.Name, observation.PhysicalIdentities)
}
