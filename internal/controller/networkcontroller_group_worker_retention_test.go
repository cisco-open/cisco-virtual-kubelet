// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controller

import (
	"context"
	"reflect"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestNetworkControllerGroupRetainsRecoveryWorker(t *testing.T) {
	for _, phase := range []string{"Prepared", "Submitting", "Accepted", "Unknown", "Verifying", "Verified", "Cancelling"} {
		t.Run(phase, func(t *testing.T) {
			exerciseNetworkControllerRecoveryWorkerRetention(t, phase, false)
		})
	}
}

func TestNetworkControllerMetadataRetainsRecoveryWorker(t *testing.T) {
	for _, phase := range []string{"Prepared", "Submitting", "Unknown", "Verifying", "Verified", "Refused", "Released", "Paused", "Deleting", "InvalidSpec", "TerminalVerified", "TerminalPaused", "TerminalInvalidSpec"} {
		t.Run(phase, func(t *testing.T) { exerciseNetworkControllerRecoveryWorkerRetention(t, phase, true) })
	}
}

func exerciseNetworkControllerRecoveryWorkerRetention(t *testing.T, phase string, metadataMode bool) {
	t.Helper()
	nc := validNetworkController("retained", "catalyst-center")
	nc.Finalizers = []string{networkControllerFinalizer}
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: &cvk.NetworkControllerConfigurationOperation{ID: "retained-operation", Phase: phase, PhysicalGroup: &cvk.NetworkControllerConfigurationGroupIdentity{}}}
	if metadataMode {
		nc.Status.Configuration.Operation.PhysicalGroup = nil
		retained := *nc.Status.Configuration.Operation.DeepCopy()
		nc.Status.Configuration.MetadataHandoff = &cvk.NetworkControllerConfigurationMetadataHandoff{Protocol: cvk.NetworkControllerMetadataHandoffProtocol, Operation: retained}
		switch phase {
		case "TerminalVerified", "TerminalPaused", "TerminalInvalidSpec":
			ensureMetadataScopeTestRegistered()
			nc.Spec.Type = "metadata-scope-test"
			nc.Status.Configuration.MetadataHandoff = nil
			nc.Status.Configuration.Operation.Phase = "Verified"
			nc.Status.Configuration.Operation.Resource = "test-remove:retained"
			if phase == "TerminalPaused" {
				nc.Spec.Paused = true
			}
			if phase == "TerminalInvalidSpec" {
				nc.Spec.Endpoint = "invalid-endpoint"
			}
		case "Refused":
			now := metav1.Now()
			nc.Status.Configuration.MetadataHandoff.RefusedAt = &now
			nc.Status.Configuration.Operation = nil
		case "Released":
			now := metav1.Now()
			nc.Status.Configuration.MetadataHandoff.ReleasedAt = &now
			nc.Status.Configuration.Operation = nil
		case "Paused":
			nc.Spec.Paused = true
			nc.Status.Configuration.Operation.Phase = "Prepared"
		case "Deleting":
			now := metav1.Now()
			nc.DeletionTimestamp = &now
			nc.Status.Configuration.Operation.Phase = "Unknown"
		case "InvalidSpec":
			nc.Spec.Endpoint = "invalid-endpoint"
			nc.Status.Configuration.Operation.Phase = "Unknown"
		}
	}
	// Retention must be conservative even when the remaining journal evidence
	// is malformed. It is not authority to reconstruct or submit an operation.
	worker := networkControllerWorkerName(nc.Name)
	metadata := metav1.ObjectMeta{Name: worker, Namespace: nc.Namespace}
	objects := []client.Object{
		&apps.Deployment{ObjectMeta: metadata, Spec: apps.DeploymentSpec{Template: core.PodTemplateSpec{Spec: core.PodSpec{Containers: []core.Container{{Name: "controller-worker", Image: "old-image"}}}}}},
		&core.ConfigMap{ObjectMeta: metadata, Data: map[string]string{"bootstrap": "retained"}},
		&core.ServiceAccount{ObjectMeta: metadata},
		&rbac.RoleBinding{ObjectMeta: metadata, RoleRef: rbac.RoleRef{APIGroup: rbac.GroupName, Kind: "ClusterRole", Name: "retained-role"}},
	}
	initial := append([]client.Object{nc}, objects...)
	r, kube := newNetworkControllerReconciler(t, initial...)
	if err := kube.Get(context.Background(), client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	r.Image = "replacement-image"
	before := make([]client.Object, len(objects))
	for i, obj := range objects {
		before[i] = obj.DeepCopyObject().(client.Object)
		if err := kube.Get(context.Background(), client.ObjectKeyFromObject(obj), before[i]); err != nil {
			t.Fatal(err)
		}
	}
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(nc)})
	terminal := metadataMode && (phase == "Verified" || phase == "Refused" || phase == "Released")
	if terminal {
		if err == nil {
			t.Fatal("terminal metadata lost its runtime settlement hold")
		}
	} else if err != nil || result.RequeueAfter != 10*time.Second {
		t.Fatalf("retention result=%+v err=%v", result, err)
	}
	for i, obj := range objects {
		after := obj.DeepCopyObject().(client.Object)
		if err := kube.Get(context.Background(), client.ObjectKeyFromObject(obj), after); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before[i], after) {
			t.Fatalf("retained %T changed", obj)
		}
	}
	var current cvk.NetworkController
	if err := kube.Get(context.Background(), client.ObjectKeyFromObject(nc), &current); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(current.Status.Configuration, nc.Status.Configuration) {
		t.Fatal("retention changed journal")
	}
}
