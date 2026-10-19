//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"testing"
	"time"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	auth "k8s.io/api/authentication/v1"
	core "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// Real service-account TokenRequest, bound Pod claims and RBAC; readiness/image
// status is staged because envtest runs no kubelet. This is not an installed
// manager/worker process or full metadata/native execution qualification.
func TestEnvtestControllerMetadataBoundRuntimeIdentity(t *testing.T) {
	environment := &envtest.Environment{}
	cfg, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Error(err)
		}
	})
	scheme := runtime.NewScheme()
	_ = core.AddToScheme(scheme)
	_ = rbac.AddToScheme(scheme)
	admin, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	adminAPI, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, ns := range []string{"metadata-runtime", "metadata-tenant", "metadata-leases"} {
		if err := admin.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []client.ObjectKey{{Namespace: "metadata-runtime", Name: "manager"}, {Namespace: "metadata-tenant", Name: "connector"}} {
		if err := admin.Create(ctx, &core.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: ref.Name, Namespace: ref.Namespace}}); err != nil {
			t.Fatal(err)
		}
	}
	role := &rbac.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: managedprotocol.NetworkManagementGlobalReadClusterRole}, Rules: managedprotocol.WorkerClusterRoleContracts()[managedprotocol.NetworkManagementGlobalReadClusterRole]}
	if err := admin.Create(ctx, role); err != nil {
		t.Fatal(err)
	}
	if err := admin.Create(ctx, &rbac.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "metadata-runtime-read"}, RoleRef: rbac.RoleRef{APIGroup: rbac.GroupName, Kind: "ClusterRole", Name: role.Name}, Subjects: []rbac.Subject{{Kind: "ServiceAccount", Name: "manager", Namespace: "metadata-runtime"}, {Kind: "ServiceAccount", Name: "connector", Namespace: "metadata-tenant"}}}); err != nil {
		t.Fatal(err)
	}
	b := contract.RuntimeAdmissionBinding{Protocol: contract.Protocol, ManagerUsername: "system:serviceaccount:metadata-runtime:manager", AdmissionPrefix: "cvk", PolicyNamespace: "metadata-runtime", PolicyName: "policy", LedgerName: "ledger", AppHostingServiceAccount: "apps", NetworkManagementServiceAccount: "network", LeaseNamespace: "metadata-leases"}
	now := metav1.NewTime(time.Now().Add(-time.Second))
	manager := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "manager", Namespace: "metadata-runtime", Labels: map[string]string{"app.kubernetes.io/component": "controller"}}, Spec: core.PodSpec{ServiceAccountName: "manager", Containers: []core.Container{{Name: "controller", Image: "example.invalid/qualification:never-started", ImagePullPolicy: core.PullNever, Command: []string{"cisco-vk", "manager", "--leader-elect", "--enable-managed-topology", "--" + managedprotocol.ControllerMetadataManagerFlag, "--topology-policy-namespace=metadata-runtime", "--topology-policy-name=policy", "--app-hosting-service-account=apps", "--network-management-service-account=network"}, Env: []core.EnvVar{{Name: "CONFIG_LEASE_NAMESPACE", Value: "metadata-leases"}}}}}}
	connector := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "connector", Namespace: "metadata-tenant"}, Spec: core.PodSpec{ServiceAccountName: "connector", Containers: []core.Container{{Name: "controller-worker", Image: "example.invalid/qualification:never-started", ImagePullPolicy: core.PullNever}}}}
	for _, p := range []*core.Pod{manager, connector} {
		if err := admin.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
		p.Status = core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue, LastTransitionTime: now}}, ContainerStatuses: []core.ContainerStatus{{Name: p.Spec.Containers[0].Name, ImageID: "containerd://sha256:staged-image", Ready: true, State: core.ContainerState{Running: &core.ContainerStateRunning{StartedAt: now}}}}}
		if err := admin.Status().Update(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	signed := func(p *core.Pod) (*rest.Config, kubernetes.Interface) {
		t.Helper()
		seconds := int64(600)
		result, err := adminAPI.CoreV1().ServiceAccounts(p.Namespace).CreateToken(ctx, p.Spec.ServiceAccountName, &auth.TokenRequest{Spec: auth.TokenRequestSpec{ExpirationSeconds: &seconds, BoundObjectRef: &auth.BoundObjectReference{APIVersion: "v1", Kind: "Pod", Name: p.Name, UID: p.UID}}}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		tokenConfig := rest.AnonymousClientConfig(cfg)
		tokenConfig.BearerToken = result.Status.Token
		api, err := kubernetes.NewForConfig(tokenConfig)
		if err != nil {
			t.Fatal(err)
		}
		return tokenConfig, api
	}
	_, managerAPI := signed(manager)
	workerConfig, workerAPI := signed(connector)
	if err := verifyManagedWorkerPodIdentity(ctx, managerAPI, b.ManagerUsername, manager.Name, string(manager.UID)); err != nil {
		t.Fatal("actual manager Pod token", err)
	}
	username := "system:serviceaccount:metadata-tenant:connector"
	if err := verifyManagedWorkerPodIdentity(ctx, workerAPI, username, connector.Name, string(connector.UID)); err != nil {
		t.Fatal("actual connector Pod token", err)
	}
	if err := verifyManagedWorkerPodIdentity(ctx, workerAPI, username, connector.Name, "replacement-uid"); err == nil {
		t.Fatal("actual token authorized a replacement Pod UID")
	}
	worker, err := client.New(workerConfig, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	observed, err := contract.ReadMetadataRuntimeManager(ctx, worker, b, true)
	if err != nil || observed.UID != manager.UID {
		t.Fatal("bound worker could not inspect current manager through existing read role", err)
	}
	var namespace core.Namespace
	if err := worker.Get(ctx, client.ObjectKey{Name: b.LeaseNamespace}, &namespace); err != nil || namespace.UID == "" {
		t.Fatal("bound worker lacks required fence namespace UID read", err)
	}
	patch := client.MergeFrom(manager.DeepCopy())
	manager.Labels["unexpected-write"] = "true"
	if err := worker.Patch(ctx, manager, patch); !apierrors.IsForbidden(err) {
		t.Fatal("metadata observation role allowed Pod mutation", err)
	}
	// Force removal of this test-only Pod; never touch a production worker.
	zero := int64(0)
	if err := adminAPI.CoreV1().Pods(connector.Namespace).Delete(ctx, connector.Name, metav1.DeleteOptions{GracePeriodSeconds: &zero, Preconditions: &metav1.Preconditions{UID: &connector.UID}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := verifyManagedWorkerPodIdentity(ctx, workerAPI, username, connector.Name, string(connector.UID))
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("deleted Pod token retained authenticated authority")
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Log("real cross-namespace Pod-bound manager/connector tokens and read-only manager/namespace inspection pass; replaced UID and deleted Pod credentials denied; process readiness staged")
}
