// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build envtest

package controller

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controllerhandoff"
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

func TestEnvtest_SWIMHandoffOwnership(t *testing.T) {
	env := &envtest.Environment{CRDDirectoryPaths: []string{findControllerCRDPath(t)}, ErrorIfCRDPathMissing: true}
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
	ns := "swim-handoff-test"
	if err = admin.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(findControllerCRDPath(t)))
	rendered, err := exec.CommandContext(ctx, "helm", "template", "cvk", filepath.Join(root, "charts/cisco-virtual-kubelet"), "--namespace", ns, "--kube-version", "1.35.0", "--set", "topology.enabled=true", "--set", "controller.leaderElect=true", "--set", "rbac.profile=strict").CombinedOutput()
	if err != nil {
		t.Fatalf("helm %v %s", err, rendered)
	}
	dec := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(rendered), 4096)
	installed := 0
	for {
		var obj unstructured.Unstructured
		err = dec.Decode(&obj.Object)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(obj.GetName(), "-swim-handoff") {
			if err = admin.Create(ctx, &obj); err != nil {
				t.Fatal(err)
			}
			installed++
		}
	}
	if installed != 2 {
		t.Fatal("missing SWIM admission policy/binding")
	}
	callers := map[string]client.Client{}
	for _, name := range []string{"xe-worker", "catc-worker", "other-worker"} {
		username := "system:serviceaccount:" + ns + ":" + name
		role := &rbac.Role{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Rules: []rbac.PolicyRule{{APIGroups: []string{"ops.cisco.vk"}, Resources: []string{"catalystcenterswimhandoffs", "catalystcenterswimhandoffs/status"}, Verbs: []string{"get", "create", "update", "delete"}}}}
		if err = admin.Create(ctx, role); err != nil {
			t.Fatal(err)
		}
		if err = admin.Create(ctx, &rbac.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, RoleRef: rbac.RoleRef{APIGroup: rbac.GroupName, Kind: "Role", Name: name}, Subjects: []rbac.Subject{{Kind: "User", APIGroup: rbac.GroupName, Name: username}}}); err != nil {
			t.Fatal(err)
		}
		cc := rest.CopyConfig(cfg)
		cc.Impersonate = rest.ImpersonationConfig{UserName: username, Extra: map[string][]string{"authentication.kubernetes.io/pod-uid": {name + "-pod"}}}
		callers[name], err = client.New(cc, client.Options{Scheme: scheme})
		if err != nil {
			t.Fatal(err)
		}
	}
	h := &ops.CatalystCenterSWIMHandoff{ObjectMeta: metav1.ObjectMeta{Name: "handoff", Namespace: ns, Finalizers: []string{controllerhandoff.Finalizer}}, Spec: ops.CatalystCenterSWIMHandoffSpec{UpgradeName: "upgrade", UpgradeUID: "upgrade-uid", DeviceName: "switch", DeviceUID: "device-uid", Serial: "SERIAL", ControllerGeneration: 1, ControllerUsername: "system:serviceaccount:" + ns + ":catc-worker", DeviceWorkerUsername: "system:serviceaccount:" + ns + ":xe-worker", DeviceWorkerPodUID: "xe-worker-pod", Source: ops.CatalystCenterImageSource{ControllerName: "catc", ControllerUID: "controller-uid", DeviceID: "device-id", ImageID: "image-id", ImageVersion: "17.18.04.0.759"}, TargetVersion: "17.18.04"}}
	h.Spec.Source.Preparation = &ops.SWIMPreparationPolicyRef{Name: "policy", UID: "policy-uid", SHA256: strings.Repeat("a", 64)}
	// Admission caches start asynchronously; exercise a denied dry-run without
	// ever storing an unauthorized journal during startup.
	deadline := time.Now().Add(10 * time.Second)
	for {
		err = callers["other-worker"].Create(ctx, h.DeepCopy(), client.DryRunAll)
		if apierrors.IsForbidden(err) || apierrors.IsInvalid(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("policy did not enforce: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err = callers["xe-worker"].Create(ctx, h); err != nil {
		t.Fatal(err)
	}
	h.Status.Phase = "Pending"
	if err = callers["xe-worker"].Status().Update(ctx, h.DeepCopy()); !(apierrors.IsForbidden(err) || apierrors.IsInvalid(err)) {
		t.Fatalf("XE wrote controller status: %v", err)
	}
	if err = callers["catc-worker"].Status().Update(ctx, h); err != nil {
		t.Fatalf("controller status rejected: %v", err)
	}
	changed := h.DeepCopy()
	changed.Spec.TargetVersion = "26.02.01"
	if err = callers["xe-worker"].Update(ctx, changed); err == nil {
		t.Fatal("immutable handoff intent changed")
	}
	if err = callers["xe-worker"].Delete(ctx, h); !(apierrors.IsForbidden(err) || apierrors.IsInvalid(err)) {
		t.Fatalf("unresolved journal deleted: %v", err)
	}
	h.Status.Phase = "Succeeded"
	h.Status.ReadinessHistory = []ops.SWIMReadinessReceipt{{Task: "prior-task", Claim: "prior-claim", Stage: "ReadyToActivate", SubmittedAt: metav1.Now()}}
	if err = callers["catc-worker"].Status().Update(ctx, h); err != nil {
		t.Fatal(err)
	}
	if err = admin.Get(ctx, client.ObjectKeyFromObject(h), h); err != nil || len(h.Status.ReadinessHistory) != 1 || h.Status.ReadinessHistory[0].Task != "prior-task" {
		t.Fatalf("readiness receipt was pruned or lost: %v", err)
	}
	h.Finalizers = nil
	if err = callers["xe-worker"].Update(ctx, h); err != nil {
		t.Fatalf("verified journal finalizer cleanup rejected: %v", err)
	}
}
