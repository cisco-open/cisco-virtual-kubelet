// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build envtest

package nexusdashboard

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// startAPIServer runs a real kube-apiserver with the repository CRDs, so the
// CEL rules, defaults and immutability checks are the ones users will hit.
func startAPIServer(t *testing.T, namespace string) (ctrlclient.Client, context.Context) {
	t.Helper()
	crds, err := filepath.Abs(filepath.Join("..", "..", "..", "config", "crd"))
	if err != nil {
		t.Fatal(err)
	}
	env := &envtest.Environment{CRDDirectoryPaths: []string{crds}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Error(err)
		}
	})
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, ciskov1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	c, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
		t.Fatal(err)
	}
	return c, ctx
}

func envtestController(name string, adoption *ciskov1.NetworkControllerDeviceAdoption) *ciskov1.NetworkController {
	return &ciskov1.NetworkController{
		ObjectMeta: metav1.ObjectMeta{Namespace: "nd-envtest", Name: name},
		Spec: ciskov1.NetworkControllerSpec{
			Type:                ciskov1.NetworkControllerType(TypeName),
			Endpoint:            "https://nd.example.test",
			CredentialSecretRef: ciskov1.NetworkControllerSecretReference{Name: "nd-creds"},
			DeviceAdoption:      adoption,
		},
	}
}

func TestEnvtest_NDDeviceAdoptionAdmission(t *testing.T) {
	c, ctx := startAPIServer(t, "nd-envtest")
	wave := func(n int) map[string]string {
		m := map[string]string{}
		for i := 0; i < n; i++ {
			m[string(rune('a'+i))] = "v"
		}
		return m
	}
	grace := &metav1.Duration{Duration: time.Hour}
	cases := []struct {
		name    string
		mutate  func(*ciskov1.NetworkControllerDeviceAdoption)
		wantErr bool
	}{
		{"valid with removal and labels", func(a *ciskov1.NetworkControllerDeviceAdoption) {
			a.Defaults.Labels = wave(3)
			a.Removal = &ciskov1.NetworkControllerDeviceRemoval{Policy: ciskov1.DeviceRemovalPrune, GracePeriod: grace}
		}, false},
		{"enabled without defaults", func(a *ciskov1.NetworkControllerDeviceAdoption) { a.Defaults = nil }, true},
		{"unknown removal policy", func(a *ciskov1.NetworkControllerDeviceAdoption) {
			a.Removal = &ciskov1.NetworkControllerDeviceRemoval{Policy: "Delete"}
		}, true},
		{"percent out of range", func(a *ciskov1.NetworkControllerDeviceAdoption) {
			v := int32(0)
			a.Removal = &ciskov1.NetworkControllerDeviceRemoval{MaxPrunePercent: &v}
		}, true},
		{"too many labels", func(a *ciskov1.NetworkControllerDeviceAdoption) { a.Defaults.Labels = wave(17) }, true},
		{"duplicate scope", func(a *ciskov1.NetworkControllerDeviceAdoption) {
			a.ScopeOverrides = append(a.ScopeOverrides, a.ScopeOverrides[0])
		}, true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := adoptionSpec()
			tc.mutate(a)
			nc := envtestController("admission-"+string(rune('a'+i)), a)
			err := c.Create(ctx, nc)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v got %v", tc.wantErr, err)
			}
			if err == nil && nc.Spec.DeviceAdoption.Removal != nil && nc.Spec.DeviceAdoption.Removal.Policy != ciskov1.DeviceRemovalPrune {
				t.Fatalf("policy not stored: %+v", nc.Spec.DeviceAdoption.Removal)
			}
		})
	}
}

func realSyncer(c ctrlclient.Client) *deviceSyncer {
	return &deviceSyncer{client: c, namespace: "nd-envtest", uid: testUID, adoption: adoptionSpec()}
}

func listNames(t *testing.T, ctx context.Context, c ctrlclient.Client) map[string]ciskov1.CiscoDevice {
	t.Helper()
	var list ciskov1.CiscoDeviceList
	if err := c.List(ctx, &list, ctrlclient.InNamespace("nd-envtest")); err != nil {
		t.Fatal(err)
	}
	out := map[string]ciskov1.CiscoDevice{}
	for _, d := range list.Items {
		out[d.Name] = d
	}
	return out
}

func TestEnvtest_NDDeviceSyncAgainstRealAPIServer(t *testing.T) {
	c, ctx := startAPIServer(t, "nd-envtest")
	s := realSyncer(c)
	s.adoption.Defaults.Labels = map[string]string{"upgrade-wave": "1"}
	s.adoption.Removal = &ciskov1.NetworkControllerDeviceRemoval{
		Policy: ciskov1.DeviceRemovalPrune, GracePeriod: &metav1.Duration{Duration: 10 * time.Minute},
	}
	clk := &testClock{t: time.Now()}
	s.now = clk.now

	// A device this controller did not create must never be touched.
	foreign := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Namespace: "nd-envtest", Name: "nd-s2"},
		Spec:       ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverNXOS, Address: "198.51.100.1", Username: "u"},
	}
	if err := c.Create(ctx, foreign); err != nil {
		t.Fatal(err)
	}

	leaf1, leaf2, leaf3 := item("S1", "leaf1", "fab-a"), item("S2", "leaf2", "fab-b"), item("S3", "leaf3", "fab-b")
	res, err := s.Sync(ctx, []InventoryItem{leaf1, leaf2, leaf3})
	if err != nil || res.Created != 2 || res.Conflicts != 1 || res.Failed != 0 {
		t.Fatalf("create: %+v err=%v", res, err)
	}
	got := listNames(t, ctx, c)
	d := got["nd-s1"]
	if d.Spec.NodeName != "leaf1" || d.Spec.PhysicalIdentity != "S1" || d.Spec.CredentialSecretRef.Name != "switch-creds" ||
		d.Labels["upgrade-wave"] != "1" || d.Spec.Labels["upgrade-wave"] != "1" || got["nd-s3"].Spec.CredentialSecretRef.Name != "fab-b-creds" {
		t.Fatalf("unexpected device %+v", d.Spec)
	}
	if got["nd-s2"].Spec.Address != "198.51.100.1" || got["nd-s2"].Labels[labelController] != "" {
		t.Fatal("foreign device was modified")
	}

	// Resync is a no-op on the real server: nothing is written.
	rv := d.ResourceVersion
	if res, err = s.Sync(ctx, []InventoryItem{leaf1, leaf2, leaf3}); err != nil || res.Unchanged != 2 || res.Updated != 0 {
		t.Fatalf("resync: %+v err=%v", res, err)
	}
	if listNames(t, ctx, c)["nd-s1"].ResourceVersion != rv {
		t.Fatal("idempotent resync wrote to the API server")
	}

	// Merge patch: address change and a dropped label, against real admission
	// (nodeName immutability and physicalIdentity write-once stay satisfied).
	leaf1.MgmtAddress = "192.0.2.77"
	s.adoption.Defaults.Labels = nil
	if res, err = s.Sync(ctx, []InventoryItem{leaf1, leaf2, leaf3}); err != nil || res.Updated != 2 || res.Failed != 0 {
		t.Fatalf("update: %+v err=%v", res, err)
	}
	d = listNames(t, ctx, c)["nd-s1"]
	if _, has := d.Labels["upgrade-wave"]; has || d.Spec.Address != "192.0.2.77" || d.Spec.NodeName != "leaf1" {
		t.Fatalf("after update: labels=%v spec=%+v", d.Labels, d.Spec)
	}

	// Prune: S3 leaves ND, is annotated, then deleted after the grace period.
	only1 := []InventoryItem{leaf1, leaf2}
	if res, err = s.Sync(ctx, only1); err != nil || res.Missing != 1 || res.Pruned != 0 {
		t.Fatalf("annotate: %+v err=%v", res, err)
	}
	clk.advance(time.Hour)
	if res, err = s.Sync(ctx, only1); err != nil || res.Pruned != 1 {
		t.Fatalf("prune: %+v err=%v", res, err)
	}
	var gone ciskov1.CiscoDevice
	if err := c.Get(ctx, types.NamespacedName{Namespace: "nd-envtest", Name: "nd-s3"}, &gone); !apierrors.IsNotFound(err) {
		t.Fatalf("pruned device still present: %v", err)
	}
	if _, ok := listNames(t, ctx, c)["nd-s2"]; !ok {
		t.Fatal("foreign device must survive pruning")
	}
}
