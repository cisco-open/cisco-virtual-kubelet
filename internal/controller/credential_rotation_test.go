// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

// Wave 6B regression tests for external-review-followup Finding #5:
// rotating a CiscoDevice's credentialSecretRef must trigger a
// per-device pod rollout. The mechanism: a pod-template annotation
// keyed on the Secret's resourceVersion, refreshed by the
// reconciler whenever the referenced Secret's resourceVersion
// changes.

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const (
	credAnnoKey             = "cisco.vk/credential-resource-version"
	gnoiProvisioningAnnoKey = "cisco.vk/gnoi-provisioning-secret-resource-version"
)

func deviceWithCredSecret(name, ns, secretName string) *ciskov1.CiscoDevice {
	d := newDevice(name, ns)
	// newDevice() sets a literal Password; clear it so the controller
	// uses the Secret reference path.
	d.Spec.Password = ""
	d.Spec.CredentialSecretRef = &corev1.LocalObjectReference{Name: secretName}
	return d
}

func deviceWithGNOIProvisioningSecret(name, ns, secretName string) *ciskov1.CiscoDevice {
	d := newDevice(name, ns)
	configureXEGNOICertificateProvisioning(d, secretName)
	return d
}

// TestReconcile_CredentialAnnotationStampedFromSecret pins the
// happy path: an upserted Deployment carries a pod-template
// annotation that includes the Secret's current resourceVersion.
func TestReconcile_CredentialAnnotationStampedFromSecret(t *testing.T) {
	dev := deviceWithCredSecret("router-cred", "default", "creds")
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "creds",
			Namespace:       "default",
			ResourceVersion: "111",
		},
		Data: map[string][]byte{"password": []byte("v1")},
	}
	r := reconcilerFor(t, dev, sec)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-cred")); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var d appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-cred" + deploymentSuffix}, &d); err != nil {
		t.Fatalf("Get Deployment: %v", err)
	}
	got := d.Spec.Template.Annotations[credAnnoKey]
	if got != "111" {
		t.Errorf("expected pod-template annotation %s=111, got %q (annos=%v)", credAnnoKey, got, d.Spec.Template.Annotations)
	}
}

// TestReconcile_CredentialAnnotationRollsOnSecretRotation is the
// headline assertion for Finding #5. Rotating the Secret bumps its
// resourceVersion; the next Reconcile must update the pod-template
// annotation accordingly so the ReplicaSet rolls.
func TestReconcile_CredentialAnnotationRollsOnSecretRotation(t *testing.T) {
	dev := deviceWithCredSecret("router-rot", "default", "creds")
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "creds",
			Namespace:       "default",
			ResourceVersion: "1",
		},
		Data: map[string][]byte{"password": []byte("v1")},
	}
	r := reconcilerFor(t, dev, sec)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-rot")); err != nil {
		t.Fatalf("Reconcile #1: %v", err)
	}
	var d1 appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-rot" + deploymentSuffix}, &d1); err != nil {
		t.Fatalf("Get Deployment #1: %v", err)
	}
	first := d1.Spec.Template.Annotations[credAnnoKey]
	if first != "1" {
		t.Fatalf("first annotation = %q, want 1", first)
	}

	// Rotate the Secret: change its data; the fake client auto-bumps
	// resourceVersion on every successful Update (mirroring the real
	// API server's optimistic-concurrency contract). Setting RV
	// explicitly here would race the fake client's own bookkeeping.
	var current corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "creds"}, &current); err != nil {
		t.Fatalf("Get Secret: %v", err)
	}
	current.Data["password"] = []byte("v2-rotated")
	if err := r.Update(ctx, &current); err != nil {
		t.Fatalf("Update Secret: %v", err)
	}

	if _, err := r.Reconcile(ctx, reconcileRequest("default", "router-rot")); err != nil {
		t.Fatalf("Reconcile #2: %v", err)
	}
	var d2 appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "router-rot" + deploymentSuffix}, &d2); err != nil {
		t.Fatalf("Get Deployment #2: %v", err)
	}
	second := d2.Spec.Template.Annotations[credAnnoKey]
	if second == first {
		t.Errorf("Secret rotation did not roll the pod template: annotation stayed %q", second)
	}
}

func TestReconcile_GNOIProvisioningAnnotationRollsOnSecretRotation(t *testing.T) {
	dev := deviceWithGNOIProvisioningSecret("router-gnoi-rot", "default", "gnoi-certs")
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "gnoi-certs",
			Namespace:       "default",
			ResourceVersion: "1",
		},
		Data: map[string][]byte{
			"tls.crt": []byte("leaf-v1"),
			"ca.crt":  []byte("ca-v1"),
			"ca.key":  []byte("key-v1"),
		},
	}
	r := reconcilerFor(t, dev, sec)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcileRequest("default", dev.Name)); err != nil {
		t.Fatalf("Reconcile #1: %v", err)
	}
	var d1 appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: dev.Name + deploymentSuffix}, &d1); err != nil {
		t.Fatalf("Get Deployment #1: %v", err)
	}
	first := d1.Spec.Template.Annotations[gnoiProvisioningAnnoKey]
	if first != "1" {
		t.Fatalf("first annotation = %q, want 1", first)
	}

	var current corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: sec.Name}, &current); err != nil {
		t.Fatalf("Get Secret: %v", err)
	}
	current.Data["tls.crt"] = []byte("leaf-v2-rotated")
	if err := r.Update(ctx, &current); err != nil {
		t.Fatalf("Update Secret: %v", err)
	}

	if _, err := r.Reconcile(ctx, reconcileRequest("default", dev.Name)); err != nil {
		t.Fatalf("Reconcile #2: %v", err)
	}
	var d2 appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: dev.Name + deploymentSuffix}, &d2); err != nil {
		t.Fatalf("Get Deployment #2: %v", err)
	}
	second := d2.Spec.Template.Annotations[gnoiProvisioningAnnoKey]
	if second == first {
		t.Errorf("gNOI provisioning Secret rotation did not roll the pod template: annotation stayed %q", second)
	}
}

// TestMapSecretToCiscoDevices_OnlyMatchingDevices pins the
// fan-out behaviour: a Secret event triggers reconciles only for
// the devices whose credentialSecretRef references it.
func TestMapSecretToCiscoDevices_OnlyMatchingDevices(t *testing.T) {
	matching := deviceWithCredSecret("dev-match", "ns", "creds-A")
	other := deviceWithCredSecret("dev-other", "ns", "creds-B")
	noRef := newDevice("dev-no-ref", "ns") // password set inline; no secret ref
	noRef.Spec.CredentialSecretRef = nil

	r := reconcilerFor(t, matching, other, noRef)
	requests := r.mapSecretToCiscoDevices(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds-A", Namespace: "ns"},
	})

	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d: %+v", len(requests), requests)
	}
	if requests[0].Name != "dev-match" {
		t.Errorf("expected request for dev-match, got %s", requests[0].Name)
	}
}

func TestMapSecretToCiscoDevices_MatchesGNOIProvisioningSecretOnce(t *testing.T) {
	matching := deviceWithGNOIProvisioningSecret("dev-gnoi-match", "ns", "gnoi-certs")
	other := deviceWithGNOIProvisioningSecret("dev-gnoi-other", "ns", "other-certs")
	both := deviceWithGNOIProvisioningSecret("dev-both", "ns", "gnoi-certs")
	both.Spec.CredentialSecretRef = &corev1.LocalObjectReference{Name: "gnoi-certs"}

	r := reconcilerFor(t, matching, other, both)
	requests := r.mapSecretToCiscoDevices(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "gnoi-certs", Namespace: "ns"},
	})

	if len(requests) != 2 {
		t.Fatalf("expected 2 requests, got %d: %+v", len(requests), requests)
	}
	got := map[string]int{}
	for _, request := range requests {
		got[request.Name]++
	}
	if got["dev-gnoi-match"] != 1 || got["dev-both"] != 1 {
		t.Errorf("unexpected Secret fan-out: %v", got)
	}
}
