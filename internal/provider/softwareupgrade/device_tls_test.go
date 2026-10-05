// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package softwareupgrade

import (
	"context"
	"strings"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDeviceCATrustHashPreservesHistoricalReceipts(t *testing.T) {
	legacy, err := canonicalPreparedHash(struct {
		Credential   string `json:"credential"`
		TLS          string `json:"tls"`
		Provisioning string `json:"provisioning"`
	}{"credential", "gnoi-ca", "provisioning"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := PreparedTrustIdentityHash("credential", "gnoi-ca", "provisioning", "")
	if err != nil || got != legacy {
		t.Fatalf("historical hash changed: %s %s %v", got, legacy, err)
	}
	oldCA, err := PreparedTrustIdentityHash("credential", "gnoi-ca", "provisioning", "101")
	if err != nil {
		t.Fatal(err)
	}
	newCA, err := PreparedTrustIdentityHash("credential", "gnoi-ca", "provisioning", "102")
	if err != nil || oldCA == got || oldCA == newCA {
		t.Fatal("device CA revision not bound into receipt")
	}
}

func TestDeviceCAFreshRuntimeFence(t *testing.T) {
	for _, scenario := range []string{"exact", "rotated", "missing", "unbound", "removed-reference", "uncached"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			up := managedTestLeaf("device-ca-" + scenario)
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: managedTestDeviceNamespace, Name: "device-ca", UID: "ca-uid", ResourceVersion: "101"}}
			r := newManagedTestReconciler(t, up, nil, secret)
			var device ciskov1.CiscoDevice
			if err := r.Client.Get(ctx, client.ObjectKey{Namespace: managedTestDeviceNamespace, Name: managedTestDeviceName}, &device); err != nil {
				t.Fatal(err)
			}
			device.Spec.TLS = &ciskov1.TLSConfig{Enabled: true, CASecretRef: &ciskov1.DeviceTLSCASecretReference{Name: secret.Name}}
			if scenario == "removed-reference" {
				device.Spec.TLS.CASecretRef = nil
			}
			if err := r.Client.Update(ctx, &device); err != nil {
				t.Fatal(err)
			}
			r.DeviceTLSCARevision = "101"
			switch scenario {
			case "rotated":
				r.DeviceTLSCARevision = "100"
			case "unbound":
				r.DeviceTLSCARevision = ""
			case "missing":
				if err := r.Client.Delete(ctx, secret); err != nil {
					t.Fatal(err)
				}
			case "uncached":
				fresh := secret.DeepCopy()
				fresh.ResourceVersion = "102"
				r.Reader = fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(managedTestNode(), &device, fresh).Build()
			}
			decision := r.evaluateManagedLeaf(ctx, up)
			if scenario == "exact" {
				if !decision.allowProgress || !decision.allowClaim {
					t.Fatalf("current CA was denied: %+v", decision)
				}
			} else if decision.allowProgress || decision.allowClaim || !strings.Contains(decision.message, "device TLS CA") {
				t.Fatalf("stale/unbound CA permitted: %+v", decision)
			}
		})
	}
}
