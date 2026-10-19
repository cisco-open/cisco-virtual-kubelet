// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package controllerconfiguration

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	admission "k8s.io/api/admissionregistration/v1"
	apps "k8s.io/api/apps/v1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// Controller/source/worker identities are simulated; policy, namespace and
// Lease I/O is served by the real isolated API. This is not an installed manager.
type bootstrapAdmissionReader struct {
	client.Reader
	simulated client.Reader
}

func (r bootstrapAdmissionReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	switch obj.(type) {
	case *cvk.NetworkController, *configapi.NetworkControllerConfig, *core.ConfigMap, *apps.Deployment, *apps.ReplicaSet, *core.Pod, *core.Node:
		return r.simulated.Get(ctx, key, obj, opts...)
	default:
		return r.Reader.Get(ctx, key, obj, opts...)
	}
}
func (r bootstrapAdmissionReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	switch list.(type) {
	case *cvk.CiscoDeviceList, *cvk.NetworkControllerList, *core.NodeList, *core.PodList:
		return r.simulated.List(ctx, list, opts...)
	default:
		return r.Reader.List(ctx, list, opts...)
	}
}

// Real API-server admission, separate from native CC qualification. No product
// device API is called. All resources live in an isolated disposable envtest.
func TestBootstrapFenceAdmission(t *testing.T) {
	env := &envtest.Environment{}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := env.Stop(); err != nil {
			t.Error(err)
		}
	}()
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	admin, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, ns := range []string{"manager-system", "bootstrap-leases", "other-tenant"} {
		if err := admin.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
			t.Fatal(err)
		}
	}
	// Impersonated identities retain broad test-only RBAC; denials therefore
	// exercise admission rather than accidentally passing due to missing RBAC.
	caller := func(username string) client.Client {
		c := rest.CopyConfig(cfg)
		c.Impersonate = rest.ImpersonationConfig{UserName: username, Groups: []string{"system:masters"}}
		cc, err := client.New(c, client.Options{Scheme: scheme})
		if err != nil {
			t.Fatal(err)
		}
		return cc
	}
	manager := caller("system:serviceaccount:manager-system:cisco-virtual-kubelet-controller")
	worker := caller("system:serviceaccount:other-tenant:connector")
	raw, err := exec.CommandContext(ctx, "helm", "template", "cvk", "../../charts/cisco-virtual-kubelet", "--namespace", "manager-system", "--kube-version", "1.35.0", "--set", "topology.enabled=true", "--set", "controller.leaderElect=true", "--set", "rbac.profile=strict", "--set", "config.leaseNamespace=bootstrap-leases", "--show-only", "templates/bootstrap-fence-admission.yaml").CombinedOutput()
	if err != nil {
		t.Fatalf("helm: %v %s", err, raw)
	}
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	var policyName string
	for {
		var obj unstructured.Unstructured
		err := decoder.Decode(&obj.Object)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if obj.GetKind() == "ValidatingAdmissionPolicy" {
			policyName = obj.GetName()
		}
		if err := admin.Create(ctx, &obj); err != nil {
			t.Fatal(err)
		}
	}
	if policyName == "" {
		t.Fatal("bootstrap policy missing")
	}
	c, nc, _ := bootstrapFenceFixture(t)
	identity, err := ReadBootstrapIdentity(ctx, c, nc)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := bootstrapFenceCandidate("bootstrap-leases", nc.Namespace, identity, bootstrapFenceFixtureWorker(t, c, nc), BootstrapFenceInstallation{PolicyName: policyName, ManagerUsername: bootstrapTestManager, LeaseNamespace: "bootstrap-leases"}, bootstrapFenceFixtureProtection(t, c))
	if err != nil {
		t.Fatal(err)
	}
	denied := func(err error) {
		t.Helper()
		if !apierrors.IsForbidden(err) && !apierrors.IsInvalid(err) {
			t.Fatalf("expected admission denial: %v", err)
		}
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		err := worker.Create(ctx, lease.DeepCopy(), client.DryRunAll)
		if apierrors.IsForbidden(err) || apierrors.IsInvalid(err) {
			break
		}
		if err != nil || time.Now().After(deadline) {
			t.Fatal("bootstrap admission not active", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	var policy admission.ValidatingAdmissionPolicy
	if err := admin.Get(ctx, client.ObjectKey{Name: policyName}, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Status.TypeChecking != nil && len(policy.Status.TypeChecking.ExpressionWarnings) > 0 {
		t.Fatal("policy type warnings", policy.Status.TypeChecking.ExpressionWarnings)
	}
	// Envtest runs API-server enforcement but no background policy type-checking
	// controller. First prove unobserved status cannot pass runtime inspection.
	if _, err := ReadBootstrapFenceProtection(ctx, admin, policyName, "system:serviceaccount:manager-system:cisco-virtual-kubelet-controller", "bootstrap-leases"); err == nil {
		t.Fatal("runtime protection accepted an unobserved policy")
	}
	if err := admin.Get(ctx, client.ObjectKey{Name: policyName}, &policy); err != nil {
		t.Fatal(err)
	}
	digest, err := bootstrapProtectionDigest(&policy, "system:serviceaccount:manager-system:cisco-virtual-kubelet-controller", "bootstrap-leases")
	if err != nil || digest != bootstrapProtectionSpecDigest {
		t.Fatal("API-defaulted policy contract mismatch", digest, err)
	}
	// Simulate ONLY that absent controller's status publication. The policy
	// Spec, binding and admission denials remain real API-server observations.
	policy.Status.ObservedGeneration = policy.Generation
	policy.Status.TypeChecking = &admission.TypeChecking{}
	if err := admin.Status().Update(ctx, &policy); err != nil {
		t.Fatal(err)
	}
	protection, err := ReadBootstrapFenceProtection(ctx, admin, policyName, "system:serviceaccount:manager-system:cisco-virtual-kubelet-controller", "bootstrap-leases")
	if err != nil || protection.PolicyUID == "" || protection.BindingUID == "" || protection.NamespaceUID == "" {
		t.Fatal("live bootstrap protection inspection", err)
	}
	t.Log("API-server protection Spec/binding verified; background compiler status was simulated explicitly")
	installation := BootstrapFenceInstallation{PolicyName: policyName, ManagerUsername: bootstrapTestManager, LeaseNamespace: "bootstrap-leases"}
	reader := bootstrapAdmissionReader{Reader: admin, simulated: c}
	// The remaining runtime compatibility callback is simulated and carries no
	// native dispatch authority; every protection read and Lease write is real.
	authorize := BootstrapFenceRuntimeCheck(func(context.Context, string, *cvk.NetworkController, BootstrapIdentityObservation) error { return nil })
	lease, err = ReserveBootstrapFence(ctx, reader, manager, nc, installation, authorize)
	if err != nil {
		t.Fatal("protected manager reservation", err)
	}
	again, err := ReserveBootstrapFence(ctx, reader, manager, nc, installation, authorize)
	if err != nil || again.UID != lease.UID {
		t.Fatal("protected reservation idempotence", err)
	}
	t.Log("reservation retained real policy/binding/namespace identities and created its Lease through real manager admission; source/worker/remaining compatibility simulated")
	if err := CheckBootstrapEnrollment(ctx, admin, "bootstrap-leases", identity.PhysicalIdentity); err == nil {
		t.Fatal("protected serial did not hold enrollment")
	}
	if err := CheckBootstrapEnrollment(ctx, admin, "bootstrap-leases", "another-serial"); err != nil {
		t.Fatal("unrelated serial enrollment was held", err)
	}
	t.Log("real retained Lease holds enrollment for its canonical serial; unrelated serial remains clear")
	for _, scenario := range []string{"worker-create", "wrong-namespace", "unmarked-name", "wrong-name", "missing-retain", "missing-binding", "missing-finalizer", "owner-ref", "expiring", "holder", "manager-update", "worker-update", "remove-markers", "manager-delete", "worker-delete"} {
		t.Run(scenario, func(t *testing.T) {
			copy := lease.DeepCopy()
			copy.ResourceVersion = ""
			copy.UID = ""
			copy.Name = "cvk-bootstrap-" + strings.Repeat("e", 32)
			switch scenario {
			case "worker-create":
				denied(worker.Create(ctx, copy, client.DryRunAll))
				return
			case "wrong-namespace":
				copy.Namespace = "other-tenant"
			case "unmarked-name":
				copy.Labels = nil
				copy.Annotations = nil
				copy.Finalizers = nil
			case "wrong-name":
				copy.Name = "ordinary-lease"
			case "missing-retain":
				delete(copy.Annotations, "topology.cisco.vk/retain-lease")
			case "missing-binding":
				delete(copy.Annotations, bootstrapFenceBindingAnnotation)
			case "missing-finalizer":
				copy.Finalizers = nil
			case "owner-ref":
				copy.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "owner", UID: "owner"}}
			case "expiring":
				ttl := int32(1)
				copy.Spec.LeaseDurationSeconds = &ttl
			case "holder":
				holder := "foreign"
				copy.Spec.HolderIdentity = &holder
			case "manager-update", "worker-update", "remove-markers":
				copy = lease.DeepCopy()
				copy.Annotations["unrelated"] = "changed"
				if scenario == "remove-markers" {
					copy.Annotations = nil
					copy.Labels = nil
					copy.Finalizers = nil
				}
				if scenario == "worker-update" {
					denied(worker.Update(ctx, copy, client.DryRunAll))
				} else {
					denied(manager.Update(ctx, copy, client.DryRunAll))
				}
				return
			case "manager-delete":
				denied(manager.Delete(ctx, lease.DeepCopy(), client.DryRunAll))
				return
			case "worker-delete":
				denied(worker.Delete(ctx, lease.DeepCopy(), client.DryRunAll))
				return
			}
			denied(manager.Create(ctx, copy, client.DryRunAll))
		})
	}
	qualifyMarkedMetadataFenceAdmission(t, ctx, admin, manager, worker, lease)
	// The policy must not capture ordinary config/device/leader-election leases.
	ordinary := &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: "ordinary-lease", Namespace: "other-tenant"}}
	if err := worker.Create(ctx, ordinary); err != nil {
		t.Fatal("ordinary lease create", err)
	}
	if err := worker.Delete(ctx, ordinary); err != nil {
		t.Fatal("ordinary lease delete", err)
	}
	var retained coordination.Lease
	if err := admin.Get(ctx, client.ObjectKeyFromObject(lease), &retained); err != nil || !sameBootstrapFence(&retained, lease) {
		t.Fatal("protected evidence changed", err)
	}
}
