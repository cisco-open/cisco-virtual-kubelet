// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"github.com/cisco/virtual-kubelet-cisco/internal/appartifact"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"testing"
)

func TestManagedVerifiedJournalUsesIncarnationAndPinsObjectUID(t *testing.T) {
	ctx := context.Background()
	identity := workerRuntimeIdentity{ManagedTopology: true, DeviceNamespace: "edge", DeviceName: "router", DeviceUID: "device-uid"}
	name := appartifact.ManagedJournalName(identity.DeviceUID)
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: identity.DeviceNamespace, UID: "journal-uid", Annotations: appartifact.JournalAnnotations(identity.DeviceNamespace, identity.DeviceName, identity.DeviceUID)}}
	kube := fake.NewClientset(cm)
	journal, err := verifiedInstallationJournal(ctx, kube, identity, "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if journal.Name != name || journal.UID != "journal-uid" {
		t.Fatalf("incorrect journal %+v", journal)
	}
	// Device address changes must not discard the incarnation's prior history.
	moved, err := verifiedInstallationJournal(ctx, kube, identity, "192.0.2.2")
	if err != nil || moved.Name != name {
		t.Fatalf("address change lost journal: %v", err)
	}
	if err = kube.CoreV1().ConfigMaps("edge").Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	replacement := cm.DeepCopy()
	replacement.UID = "replacement-uid"
	if _, err = kube.CoreV1().ConfigMaps("edge").Create(ctx, replacement, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = journal.Load(ctx, "app"); err == nil {
		t.Fatal("worker accepted journal replacement")
	}
	if err = journal.Save(ctx, "app", "", appartifact.Installation{Identity: "test", Phase: "running"}); err == nil {
		t.Fatal("worker wrote replaced journal")
	}
	replacement.Annotations[appartifact.JournalDeviceUID] = "foreign-device"
	if _, err = kube.CoreV1().ConfigMaps("edge").Update(ctx, replacement, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = verifiedInstallationJournal(ctx, kube, identity, "192.0.2.1"); err == nil {
		t.Fatal("startup accepted foreign journal")
	}
	identity.DeviceUID = "new-incarnation"
	if _, err = verifiedInstallationJournal(ctx, kube, identity, "192.0.2.1"); err == nil {
		t.Fatal("new incarnation adopted old history")
	}
}
func TestStandaloneVerifiedJournalNamingIsPreserved(t *testing.T) {
	ctx := context.Background()
	address := "192.0.2.1"
	name := iosxe.VerifiedJournalName(address)
	kube := fake.NewClientset(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "edge"}})
	journal, err := verifiedInstallationJournal(ctx, kube, workerRuntimeIdentity{DeviceNamespace: "edge"}, address)
	if err != nil || journal.Name != name {
		t.Fatalf("standalone compatibility: %v", err)
	}
}
