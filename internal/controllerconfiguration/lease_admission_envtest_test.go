// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build envtest

package controllerconfiguration

import (
	"bytes"
	"context"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"io"
	coordination "k8s.io/api/coordination/v1"
	"os/exec"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	core "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
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

func TestConfigurationLeaseAdmission(t *testing.T) {
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
	_ = ops.AddToScheme(scheme)
	admin, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	const ns = "reservation-test"
	if err = admin.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
		t.Fatal(err)
	}
	raw, err := exec.CommandContext(ctx, "helm", "template", "cvk", "../../charts/cisco-virtual-kubelet", "--namespace", ns, "--kube-version", "1.35.0", "--set", "topology.enabled=true", "--set", "controller.leaderElect=true", "--set", "rbac.profile=strict", "--show-only", "templates/topology-admission.yaml").CombinedOutput()
	if err != nil {
		t.Fatalf("helm %v %s", err, raw)
	}
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	for {
		var obj unstructured.Unstructured
		err = decoder.Decode(&obj.Object)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(obj.GetName(), "-managed-maintenance-lease") {
			continue
		}
		if err = admin.Create(ctx, &obj); err != nil {
			t.Fatal(err)
		}
	}

	const worker = "cvk-cisco-virtual-kubelet-network-management"
	const appWorker = "cvk-cisco-virtual-kubelet-app-hosting"
	callers := map[string]client.Client{}
	for _, name := range []string{"cisco-virtual-kubelet-controller", worker, appWorker, "other"} {
		username := "system:serviceaccount:" + ns + ":" + name
		role := &rbac.Role{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Rules: []rbac.PolicyRule{{APIGroups: []string{"coordination.k8s.io"}, Resources: []string{"leases"}, Verbs: []string{"get", "create", "update", "delete"}}}}
		if err := admin.Create(ctx, role); err != nil {
			t.Fatal(err)
		}
		if err := admin.Create(ctx, &rbac.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, RoleRef: rbac.RoleRef{APIGroup: rbac.GroupName, Kind: "Role", Name: name}, Subjects: []rbac.Subject{{Kind: "User", APIGroup: rbac.GroupName, Name: username}}}); err != nil {
			t.Fatal(err)
		}
		cc := rest.CopyConfig(cfg)
		cc.Impersonate = rest.ImpersonationConfig{UserName: username, Extra: map[string][]string{"authentication.kubernetes.io/pod-uid": {name + "-pod"}}}
		callers[name], err = client.New(cc, client.Options{Scheme: scheme})
		if err != nil {
			t.Fatal(err)
		}
	}
	username := "system:serviceaccount:" + ns + ":" + worker
	key := devicecoordination.DeviceKey(ns, "switch")
	lease := &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: engine.LeaseName(key, devicecoordination.MutationLeaseFamily), Labels: map[string]string{"cisco.vk/device": key, "cisco.vk/family": devicecoordination.MutationLeaseFamily}, Annotations: map[string]string{
		managedprotocol.AnnotationAppWorkerUsername: "system:serviceaccount:" + ns + ":" + appWorker,
		managedprotocol.AnnotationAppWorkerPodName:  "app", managedprotocol.AnnotationAppWorkerPodUID: appWorker + "-pod",
		managedprotocol.AnnotationManaged: "true", devicecoordination.RetainLeaseAnnotation: "true", managedprotocol.AnnotationDeviceNamespace: ns, managedprotocol.AnnotationDeviceName: "switch", managedprotocol.AnnotationDeviceUID: "device-uid", managedprotocol.AnnotationNodeName: "switch", managedprotocol.AnnotationNodeUID: "node-uid", managedprotocol.AnnotationWorkerUsername: username, managedprotocol.AnnotationWorkerProtocol: managedprotocol.Version, managedprotocol.AnnotationLeasePurpose: managedprotocol.LeasePurposeDeviceMutation, managedprotocol.AnnotationNetworkWorkerUsername: username, managedprotocol.AnnotationNetworkWorkerPodName: "worker", managedprotocol.AnnotationNetworkWorkerPodUID: worker + "-pod",
	}}}
	denied := func(err error) {
		t.Helper()
		if !apierrors.IsForbidden(err) && !apierrors.IsInvalid(err) {
			t.Fatalf("expected denial, got %v", err)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		err = callers["other"].Create(ctx, lease.DeepCopy(), client.DryRunAll)
		if apierrors.IsForbidden(err) || apierrors.IsInvalid(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("policy not active: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := callers["cisco-virtual-kubelet-controller"].Create(ctx, lease); err != nil {
		t.Fatal(err)
	}
	holder, ttl, transitions := "controller-configuration/reservation-uid", int32(3600), int32(1)
	now := metav1.NewMicroTime(time.Now().UTC().Truncate(time.Second))
	lease.Spec = coordination.LeaseSpec{HolderIdentity: &holder, LeaseDurationSeconds: &ttl, LeaseTransitions: &transitions, AcquireTime: &now, RenewTime: &now}
	denied(callers[appWorker].Update(ctx, lease.DeepCopy(), client.DryRunAll))
	if err := callers[worker].Update(ctx, lease); err != nil {
		t.Fatal("acquire", err)
	}
	for k, v := range map[string]string{managedprotocol.AnnotationMaintenanceRequestVersion: Protocol, managedprotocol.AnnotationMaintenancePurpose: managedprotocol.MaintenancePurposeConfigurationMutation, managedprotocol.AnnotationMaintenanceSessionToken: "11111111-1111-4111-8111-111111111111", managedprotocol.AnnotationMaintenanceRequestedAt: now.Format(time.RFC3339), managedprotocol.AnnotationMaintenanceOperationNS: ns, managedprotocol.AnnotationMaintenanceOperationName: "reservation", managedprotocol.AnnotationMaintenanceOperationUID: "reservation-uid", managedprotocol.AnnotationMaintenanceControlRevision: "1"} {
		lease.Annotations[k] = v
	}
	for _, scenario := range []string{"wrong purpose", "wrong uid", "wrong token version", "zero revision", "wrong namespace", "unknown protocol"} {
		t.Run(scenario, func(t *testing.T) {
			v := lease.DeepCopy()
			switch scenario {
			case "wrong purpose":
				v.Annotations[managedprotocol.AnnotationMaintenancePurpose] = "SoftwareMutation"
			case "wrong uid":
				v.Annotations[managedprotocol.AnnotationMaintenanceOperationUID] = "foreign"
			case "wrong token version":
				v.Annotations[managedprotocol.AnnotationMaintenanceSessionToken] = "11111111-1111-1111-8111-111111111111"
			case "zero revision":
				v.Annotations[managedprotocol.AnnotationMaintenanceControlRevision] = "0"
			case "wrong namespace":
				v.Annotations[managedprotocol.AnnotationMaintenanceOperationNS] = "other"
			case "unknown protocol":
				v.Annotations[managedprotocol.AnnotationMaintenanceRequestVersion] = "future"
			}
			denied(callers[worker].Update(ctx, v, client.DryRunAll))
		})
	}
	denied(callers["other"].Update(ctx, lease.DeepCopy(), client.DryRunAll))
	replacementConfig := rest.CopyConfig(cfg)
	replacementConfig.Impersonate = rest.ImpersonationConfig{UserName: username, Extra: map[string][]string{"authentication.kubernetes.io/pod-uid": {"replacement"}}}
	replacement, err := client.New(replacementConfig, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	denied(replacement.Update(ctx, lease.DeepCopy(), client.DryRunAll))
	if err := callers[worker].Update(ctx, lease); err != nil {
		t.Fatal("publish request", err)
	}
	changed := lease.DeepCopy()
	changed.Annotations[managedprotocol.AnnotationMaintenanceSessionToken] = "22222222-2222-4222-8222-222222222222"
	denied(callers[worker].Update(ctx, changed, client.DryRunAll))
	lease.Spec.RenewTime = &metav1.MicroTime{Time: now.Add(time.Second)}
	if err := callers[worker].Update(ctx, lease); err != nil {
		t.Fatal("renew", err)
	}
	lease.Spec = coordination.LeaseSpec{LeaseTransitions: &transitions}
	for k := range lease.Annotations {
		if strings.HasPrefix(k, "topology.cisco.vk/maintenance-") {
			delete(lease.Annotations, k)
		}
	}
	denied(replacement.Update(ctx, lease.DeepCopy(), client.DryRunAll))
	if err := callers[worker].Update(ctx, lease); err != nil {
		t.Fatal("release", err)
	}
	// Legacy request forms retain their holder and token contracts.
	for _, protocol := range []string{"rollout-v1", "pdb-drain-v1"} {
		holder = "software-upgrade/upgrade-uid"
		transitions++
		lease.Spec = coordination.LeaseSpec{HolderIdentity: &holder, LeaseDurationSeconds: &ttl, LeaseTransitions: &transitions, AcquireTime: &now, RenewTime: &now}
		if err := callers[worker].Update(ctx, lease); err != nil {
			t.Fatal(err)
		}
		for k, v := range map[string]string{managedprotocol.AnnotationMaintenanceRequestVersion: protocol, managedprotocol.AnnotationMaintenancePurpose: "SoftwareMutation", managedprotocol.AnnotationMaintenanceSessionToken: "11111111-1111-4111-8111-111111111111", managedprotocol.AnnotationMaintenanceRequestedAt: now.Format(time.RFC3339), managedprotocol.AnnotationMaintenanceOperationNS: ns, managedprotocol.AnnotationMaintenanceOperationName: "upgrade", managedprotocol.AnnotationMaintenanceOperationUID: "upgrade-uid", managedprotocol.AnnotationMaintenanceControlRevision: "0"} {
			lease.Annotations[k] = v
		}
		if err := callers[worker].Update(ctx, lease); err != nil {
			t.Fatal(protocol, err)
		}
		lease.Spec = coordination.LeaseSpec{LeaseTransitions: &transitions}
		for k := range lease.Annotations {
			if strings.HasPrefix(k, "topology.cisco.vk/maintenance-") {
				delete(lease.Annotations, k)
			}
		}
		if err := callers[worker].Update(ctx, lease); err != nil {
			t.Fatal(err)
		}
	}
}
