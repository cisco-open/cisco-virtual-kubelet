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

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestReconcile_SignerRevocationWithInvalidPublicMaterial(t *testing.T) {
	for _, invalid := range []string{"malformed", "expired"} {
		for _, revoke := range []string{"remove-key", "disable-write-gate"} {
			t.Run(invalid+"/"+revoke, func(t *testing.T) {
				t.Setenv(envCVKEnableWriteClassGNOI, "true")
				t.Setenv(envCVKGNOIDisabled, "false")
				ctx := context.Background()
				device := newDevice("signer-cleanup", "default")
				configureXEGNOICertificateProvisioning(device, "signing-material")
				data := validGNOIProvisioningSecretData(t, device.Spec.Address)
				r := reconcilerFor(t, device, &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "signing-material", Namespace: device.Namespace},
					Data:       data,
				})
				request := reconcileRequest(device.Namespace, device.Name)
				if _, err := r.Reconcile(ctx, request); err != nil {
					t.Fatal(err)
				}
				key := types.NamespacedName{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}
				var initial appsv1.Deployment
				if err := r.Get(ctx, key, &initial); err != nil {
					t.Fatal(err)
				}
				if !podTemplateProjectsGNOIPrivateKey(&initial.Spec.Template.Spec) {
					t.Fatal("fixture did not project signer")
				}
				var secret corev1.Secret
				if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: "signing-material"}, &secret); err != nil {
					t.Fatal(err)
				}
				if invalid == "malformed" {
					secret.Data["tls.crt"] = []byte("not PEM")
				} else {
					secret.Data["tls.crt"] = expiredProvisioningLeaf(t, secret.Data)
				}
				if revoke == "remove-key" {
					delete(secret.Data, "ca.key")
				} else {
					t.Setenv(envCVKEnableWriteClassGNOI, "false")
				}
				if err := r.Update(ctx, &secret); err != nil {
					t.Fatal(err)
				}
				result, err := r.Reconcile(ctx, request)
				if err != nil {
					t.Fatalf("revocation reconcile: %v", err)
				}
				if result.RequeueAfter <= 0 {
					t.Fatal("invalid Secret did not schedule recovery")
				}
				after := assertInvalidGNOIWorker(t, r, device, "public material is invalid")
				if after.ResourceVersion == initial.ResourceVersion {
					t.Fatal("revocation did not trigger a rollout")
				}
				if after.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType || after.Annotations[gnoiSignerMountedAnnotation] != "true" {
					t.Fatal("revocation lost the non-overlapping signer cleanup lifecycle")
				}
			})
		}
	}
}

// Reissue the fixture's profile with an expired validity period but the same
// valid issuer signature, so the test exercises expiration rather than bad PEM.
func expiredProvisioningLeaf(t *testing.T, data map[string][]byte) []byte {
	t.Helper()
	leafBlock, _ := pem.Decode(data["tls.crt"])
	leaf, err := x509.ParseCertificate(leafBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	_, chain := pem.Decode(data["ca.crt"])
	issuerBlock, _ := pem.Decode(chain)
	issuer, err := x509.ParseCertificate(issuerBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	keyBlock, _ := pem.Decode(data["ca.key"])
	issuerKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	leaf.NotBefore = time.Now().Add(-2 * time.Hour)
	leaf.NotAfter = time.Now().Add(-time.Hour)
	der, err := x509.CreateCertificate(rand.Reader, leaf, issuer, leaf.PublicKey, issuerKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestReconcile_InvalidGNOIAllowsCredentialAndImageUpdatesAndRepair(t *testing.T) {
	t.Setenv(envCVKGNOIDisabled, "false")
	ctx := context.Background()
	caPEM, _, _ := gnoiTLSSecretMaterial(t)
	device := deviceWithGNOITLSSecret("gnoi-repair", "default", "gnoi-trust")
	device.Spec.CredentialSecretRef = &corev1.LocalObjectReference{Name: "credentials"}
	r := reconcilerFor(t, device,
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "gnoi-trust", Namespace: "default"}, Data: map[string][]byte{"ca.crt": caPEM}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "default"}, Data: map[string][]byte{"password": []byte("old-test-password")}},
	)
	request := reconcileRequest(device.Namespace, device.Name)
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}
	var trust, credential corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "gnoi-trust"}, &trust); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "credentials"}, &credential); err != nil {
		t.Fatal(err)
	}
	trust.Data["ca.crt"] = []byte("invalid")
	credential.Data["password"] = []byte("new-test-password")
	if err := r.Update(ctx, &trust); err != nil {
		t.Fatal(err)
	}
	if err := r.Update(ctx, &credential); err != nil {
		t.Fatal(err)
	}
	r.Image = "example.test/cvk:updated"
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}
	degraded := assertInvalidGNOIWorker(t, r, device, "no parseable certificates")
	if degraded.Spec.Template.Spec.Containers[0].Image != r.Image || degraded.Spec.Template.Annotations["cisco.vk/credential-resource-version"] != credential.ResourceVersion {
		t.Fatal("invalid gNOI trust blocked independent worker image/credential updates")
	}
	trust.Data["ca.crt"] = caPEM
	if err := r.Update(ctx, &trust); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}
	var repaired appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: device.Name + deploymentSuffix}, &repaired); err != nil {
		t.Fatal(err)
	}
	if disabled, exists := findEnvVar(repaired.Spec.Template.Spec.Containers[0].Env, envCVKGNOIDisabled); exists && (disabled.Value == "1" || disabled.Value == "true") {
		t.Fatal("gNOI remains disabled after Secret repair")
	}
	assertGNOITLSProjection(t, &repaired, trust.Name, []string{"ca.crt"})
	var current ciskov1.CiscoDevice
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: device.Name}, &current); err != nil {
		t.Fatal(err)
	}
	condition := meta.FindStatusCondition(current.Status.Conditions, ciskov1.CiscoDeviceConditionGNOIConfigurationReady)
	if condition == nil || condition.Status != metav1.ConditionTrue {
		t.Fatalf("repair condition=%+v", condition)
	}
}

type failingGNOISecretReader struct{ client.Client }

func (c failingGNOISecretReader) Get(ctx context.Context, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
	if _, secret := object.(*corev1.Secret); secret {
		return apierrors.NewTimeoutError("temporary Secret API outage", 1)
	}
	return c.Client.Get(ctx, key, object, options...)
}

func TestReconcile_SecretAPIFailureDoesNotDisableWorkingGNOI(t *testing.T) {
	t.Setenv(envCVKGNOIDisabled, "false")
	ctx := context.Background()
	caPEM, _, _ := gnoiTLSSecretMaterial(t)
	device := deviceWithGNOITLSSecret("gnoi-api-outage", "default", "gnoi-trust")
	r := reconcilerFor(t, device, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "gnoi-trust", Namespace: "default"}, Data: map[string][]byte{"ca.crt": caPEM}})
	request := reconcileRequest(device.Namespace, device.Name)
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}
	key := types.NamespacedName{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}
	var before, after appsv1.Deployment
	if err := r.Get(ctx, key, &before); err != nil {
		t.Fatal(err)
	}
	r.Client = failingGNOISecretReader{r.Client}
	if _, err := r.Reconcile(ctx, request); err == nil {
		t.Fatal("Secret API failure must be retried")
	}
	if err := r.Get(ctx, key, &after); err != nil {
		t.Fatal(err)
	}
	if before.ResourceVersion != after.ResourceVersion {
		t.Fatal("transient Secret API error altered working Deployment")
	}
}
