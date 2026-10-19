// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package provider

import (
	"context"
	"errors"
	"testing"

	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	vklog "github.com/virtual-kubelet/virtual-kubelet/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNXOSConfigMutationGuard(t *testing.T) {
	for _, mode := range []string{"denied", "allowed", "report", "relinquish-denied"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-new"}}`)}
			cr := &config.NXOSConfig{ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "network", Generation: 1}, Spec: config.NXOSConfigSpec{DeviceRef: config.DeviceRef{Name: "leaf"}, ManagedFamilies: []string{"system"}, ModelSource: validNXOSModelSource(), Source: config.ConfigurationSource{Inline: &raw}}}
			if mode == "report" {
				cr.Spec.DriftPolicy = config.DriftPolicyReport
			}
			if mode == "relinquish-denied" {
				cr.Spec.ManagedFamilies = []string{"vlan"}
				cr.Spec.PruneOnRelinquish = true
				cr.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(`{"vlan":{"vlans":[{"id":101,"name":"owned"}]}}`)}
				cr.Finalizers = []string{nxosConfigFinalizer}
				cr.Status.AtomicReplaceOwnedKeys = map[string][]string{"vlan": {"101"}}
			}
			c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithStatusSubresource(&config.NXOSConfig{}).WithObjects(cr).Build()
			if mode == "relinquish-denied" {
				if err := c.Delete(ctx, cr); err != nil {
					t.Fatal(err)
				}
			}
			transport := &fakeNXOSTransport{hostname: "old", vlans: map[int]string{101: "owned", 200: "baseline"}}
			calls, finishes := 0, 0
			denied := errors.New("bootstrap evidence retained")
			r := &NXOSConfigReconciler{Client: c, DeviceName: "leaf", DeviceNamespace: "network", Transport: transport, Lookup: testedNXOSWriter, AcquireMutation: func(ctx context.Context) (context.Context, func(error), error) {
				calls++
				if mode != "allowed" {
					return ctx, nil, denied
				}
				return ctx, func(err error) {
					finishes++
					if err != nil {
						t.Errorf("successful mutation finish: %v", err)
					}
				}, nil
			}}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cr)})
			if mode == "allowed" {
				if err != nil || calls != 1 || finishes != 1 || transport.hostname != "leaf-new" {
					t.Fatal("allowed mutation was not completed", err, calls, finishes, transport.hostname)
				}
				return
			}
			if mode == "report" {
				if err != nil || calls != 0 || transport.hostname != "old" || transport.fetches == 0 {
					t.Fatal("report-only behavior changed", err, calls)
				}
				return
			}
			if calls != 1 || finishes != 0 || transport.hostname != "old" || transport.fetches != 0 || transport.saves != 0 || len(transport.deleteOps) != 0 {
				t.Fatal("denied mutation performed device I/O", calls, transport.fetches, transport.saves)
			}
			var retained config.NXOSConfig
			if err := c.Get(ctx, client.ObjectKeyFromObject(cr), &retained); err != nil {
				t.Fatal("denied config evidence lost", err)
			}
			if mode == "relinquish-denied" {
				if err == nil || !containsFinalizer(retained.Finalizers, nxosConfigFinalizer) || len(retained.Status.AtomicReplaceOwnedKeys["vlan"]) != 1 || transport.vlans[101] != "owned" {
					t.Fatal("denied relinquish removed finalizer or ownership", err)
				}
			} else if retained.Status.Phase != "LeaseBlocked" {
				t.Fatal("denied mutation status", retained.Status.Phase)
			}
		})
	}
}

func TestIOSXEMutationGuardRetainsRelinquishEvidence(t *testing.T) {
	ctx := context.Background()
	cr := newCR("guarded-delete", "edge-01")
	cr.Spec.PruneOnRelinquish = true
	cr.Finalizers = []string{iosxeConfigFinalizer}
	cr.Status.AtomicReplaceOwnedKeys = map[string][]string{"vlan": {"4001"}}
	c := fake.NewClientBuilder().WithScheme(newSchemeWithLeases(t)).WithObjects(cr).Build()
	if err := c.Delete(ctx, cr); err != nil {
		t.Fatal(err)
	}
	prunes := map[string]int{}
	r, _, _ := makeReachableReconciler(t, c, "edge-01", map[string]*noopWriter{"vlan": {family: "vlan", pruneCalls: &prunes}})
	r.Leaser = nil
	denied := errors.New("bootstrap evidence retained")
	calls := 0
	r.AcquireMutation = func(ctx context.Context) (context.Context, func(error), error) { calls++; return ctx, nil, denied }
	if err := r.relinquishOwnedKeys(ctx, cr); !errors.Is(err, denied) {
		t.Fatal("blocked relinquish was treated as completion", err)
	}
	r.reconcileAll(ctx, vklog.G(ctx), triggerPoll)
	var retained config.IOSXEConfig
	if err := c.Get(ctx, client.ObjectKeyFromObject(cr), &retained); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || prunes["vlan"] != 0 || !containsFinalizer(retained.Finalizers, iosxeConfigFinalizer) || len(retained.Status.AtomicReplaceOwnedKeys["vlan"]) != 1 {
		t.Fatal("blocked IOS-XE relinquish lost evidence", calls, prunes)
	}
}
