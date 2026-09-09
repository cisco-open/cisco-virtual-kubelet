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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func deviceWithGNOITLSSecret(name, namespace, secretName string) *ciskov1.CiscoDevice {
	device := newDevice(name, namespace)
	device.Spec.TLS = &ciskov1.TLSConfig{Enabled: true, InsecureSkipVerify: true}
	device.Spec.GNOI = &ciskov1.GNOIConfig{
		TransportSecurity: ciskov1.GNOITransportSecurityTLS,
		TLS: &ciskov1.GNOITLSConfig{
			SecretRef: &ciskov1.GNOITLSSecretReference{Name: secretName},
		},
	}
	return device
}

func TestReconcile_GNOITLSSecretProjectsVerifiedTrust(t *testing.T) {
	t.Setenv(envCVKGNOIDisabled, "false")
	caPEM, _, _ := gnoiTLSSecretMaterial(t)
	device := deviceWithGNOITLSSecret("router-gnoi-tls", "default", "router-gnoi-trust")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "router-gnoi-trust", Namespace: "default", ResourceVersion: "41"},
		Data: map[string][]byte{
			"ca.crt":    caPEM,
			"unrelated": []byte("must-not-be-projected-or-rendered"),
		},
	}
	r := reconcilerFor(t, device, secret)
	ctx := context.Background()
	if _, err := r.Reconcile(ctx, reconcileRequest(device.Namespace, device.Name)); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var deployment appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}, &deployment); err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	if got := deployment.Spec.Template.Annotations["cisco.vk/gnoi-tls-secret-resource-version"]; got != "41" {
		t.Fatalf("gNOI TLS Secret resourceVersion annotation=%q, want 41", got)
	}
	assertGNOITLSProjection(t, &deployment, secret.Name, []string{"ca.crt"})

	var configMap corev1.ConfigMap
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: device.Name + configMapSuffix}, &configMap); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	rendered := configMap.Data[configFileName]
	for _, forbidden := range []string{secret.Name, string(caPEM), string(secret.Data["unrelated"]), "secretRef:"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("rendered ConfigMap contains forbidden Secret material %q:\n%s", forbidden, rendered)
		}
	}
	if !strings.Contains(rendered, "caFile: "+gnoiTLSMountPath+"/ca.crt") {
		t.Fatalf("rendered ConfigMap does not contain resolved gNOI CA path:\n%s", rendered)
	}
}

func TestReconcile_GNOITLSSecretProjectsValidatedClientPair(t *testing.T) {
	t.Setenv(envCVKGNOIDisabled, "false")
	caPEM, certPEM, keyPEM := gnoiTLSSecretMaterial(t)
	device := deviceWithGNOITLSSecret("router-gnoi-mtls", "default", "router-gnoi-mtls")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "router-gnoi-mtls", Namespace: "default"},
		Data:       map[string][]byte{"ca.crt": caPEM, "tls.crt": certPEM, "tls.key": keyPEM},
	}
	r := reconcilerFor(t, device, secret)
	ctx := context.Background()
	if _, err := r.Reconcile(ctx, reconcileRequest(device.Namespace, device.Name)); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var deployment appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}, &deployment); err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	assertGNOITLSProjection(t, &deployment, secret.Name, []string{"ca.crt", "tls.crt", "tls.key"})
	var configMap corev1.ConfigMap
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: device.Name + configMapSuffix}, &configMap); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	rendered := configMap.Data[configFileName]
	for _, path := range []string{gnoiTLSMountPath + "/ca.crt", gnoiTLSMountPath + "/tls.crt", gnoiTLSMountPath + "/tls.key"} {
		if !strings.Contains(rendered, path) {
			t.Fatalf("rendered ConfigMap missing path %q:\n%s", path, rendered)
		}
	}
	if strings.Contains(rendered, string(keyPEM)) || strings.Contains(rendered, secret.Name) {
		t.Fatal("rendered ConfigMap exposed the Secret name or client private key")
	}
}

func TestReconcile_GNOITLSSecretRejectsInvalidMaterial(t *testing.T) {
	t.Setenv(envCVKGNOIDisabled, "false")
	caPEM, certPEM, keyPEM := gnoiTLSSecretMaterial(t)
	tests := []struct {
		name       string
		data       map[string][]byte
		omitSecret bool
		want       string
	}{
		{name: "missing Secret", omitSecret: true, want: "was not found"},
		{name: "missing CA", data: map[string][]byte{}, want: "requires non-empty key ca.crt"},
		{name: "malformed CA", data: map[string][]byte{"ca.crt": []byte("not PEM")}, want: "no parseable certificates"},
		{name: "certificate without key", data: map[string][]byte{"ca.crt": caPEM, "tls.crt": certPEM}, want: "must be configured together"},
		{name: "key without certificate", data: map[string][]byte{"ca.crt": caPEM, "tls.key": keyPEM}, want: "must be configured together"},
		{name: "invalid pair", data: map[string][]byte{"ca.crt": caPEM, "tls.crt": certPEM, "tls.key": []byte("not PEM")}, want: "invalid client certificate pair"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			device := deviceWithGNOITLSSecret("router-invalid", "default", "invalid-gnoi-tls")
			var r *CiscoDeviceReconciler
			if tt.omitSecret {
				r = reconcilerFor(t, device)
			} else {
				r = reconcilerFor(t, device, &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "invalid-gnoi-tls", Namespace: "default"},
					Data:       tt.data,
				})
			}
			recorder := record.NewFakeRecorder(2)
			r.Recorder = recorder
			_, err := r.Reconcile(context.Background(), reconcileRequest(device.Namespace, device.Name))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Reconcile error=%v, want substring %q", err, tt.want)
			}
			select {
			case event := <-recorder.Events:
				if !strings.Contains(event, "GNOITLSInvalid") {
					t.Fatalf("event=%q, want GNOITLSInvalid", event)
				}
			case <-time.After(time.Second):
				t.Fatal("expected GNOITLSInvalid warning event")
			}
			var configMap corev1.ConfigMap
			err = r.Get(context.Background(), types.NamespacedName{Namespace: device.Namespace, Name: device.Name + configMapSuffix}, &configMap)
			if err == nil {
				t.Fatal("invalid gNOI TLS material unexpectedly produced a worker ConfigMap")
			}
		})
	}
}

func TestReconcile_DisabledGNOIDoesNotReadOrProjectTLSSecret(t *testing.T) {
	t.Setenv(envCVKGNOIDisabled, "true")
	device := deviceWithGNOITLSSecret("router-gnoi-disabled-tls", "default", "does-not-exist")
	r := reconcilerFor(t, device)
	ctx := context.Background()
	if _, err := r.Reconcile(ctx, reconcileRequest(device.Namespace, device.Name)); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var deployment appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}, &deployment); err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.Name == gnoiTLSVolumeName {
			t.Fatalf("globally disabled gNOI projected TLS Secret: %+v", volume)
		}
	}
	if _, ok := deployment.Spec.Template.Annotations["cisco.vk/gnoi-tls-secret-resource-version"]; ok {
		t.Fatal("globally disabled gNOI stamped TLS Secret rollout annotation")
	}
}

func TestReconcile_GNOITLSSecretRotationRollsWorker(t *testing.T) {
	t.Setenv(envCVKGNOIDisabled, "false")
	caPEM, _, _ := gnoiTLSSecretMaterial(t)
	device := deviceWithGNOITLSSecret("router-gnoi-rotate", "default", "gnoi-trust")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "gnoi-trust", Namespace: "default", ResourceVersion: "1"},
		Data:       map[string][]byte{"ca.crt": caPEM},
	}
	r := reconcilerFor(t, device, secret)
	ctx := context.Background()
	request := reconcileRequest(device.Namespace, device.Name)
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	var first appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}, &first); err != nil {
		t.Fatalf("get first Deployment: %v", err)
	}
	firstVersion := first.Spec.Template.Annotations["cisco.vk/gnoi-tls-secret-resource-version"]

	var current corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: secret.Namespace, Name: secret.Name}, &current); err != nil {
		t.Fatalf("get Secret: %v", err)
	}
	current.Annotations = map[string]string{"rotation": "2"}
	if err := r.Update(ctx, &current); err != nil {
		t.Fatalf("rotate Secret: %v", err)
	}
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	var second appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}, &second); err != nil {
		t.Fatalf("get second Deployment: %v", err)
	}
	if got := second.Spec.Template.Annotations["cisco.vk/gnoi-tls-secret-resource-version"]; got == "" || got == firstVersion {
		t.Fatalf("Secret rotation did not change rollout annotation: first=%q second=%q", firstVersion, got)
	}
}

func TestMapSecretToCiscoDevices_MatchesGNOITLSOnce(t *testing.T) {
	matching := deviceWithGNOITLSSecret("matching", "ns", "gnoi-trust")
	matching.Spec.CredentialSecretRef = &corev1.LocalObjectReference{Name: "gnoi-trust"}
	other := deviceWithGNOITLSSecret("other", "ns", "other-trust")
	r := reconcilerFor(t, matching, other)
	requests := r.mapSecretToCiscoDevices(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "gnoi-trust", Namespace: "ns"},
	})
	if len(requests) != 1 || requests[0].Name != matching.Name {
		t.Fatalf("Secret mapping requests=%+v, want matching device once", requests)
	}
}

func assertGNOITLSProjection(t *testing.T, deployment *appsv1.Deployment, secretName string, wantKeys []string) {
	t.Helper()
	foundMount := false
	for _, mount := range deployment.Spec.Template.Spec.Containers[0].VolumeMounts {
		if mount.Name == gnoiTLSVolumeName {
			foundMount = true
			if mount.MountPath != gnoiTLSMountPath || !mount.ReadOnly {
				t.Fatalf("gNOI TLS mount=%+v, want path %q read-only", mount, gnoiTLSMountPath)
			}
		}
	}
	if !foundMount {
		t.Fatal("gNOI TLS volume mount is missing")
	}
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.Name != gnoiTLSVolumeName {
			continue
		}
		if volume.Projected == nil || volume.Projected.DefaultMode == nil || *volume.Projected.DefaultMode != 0o440 {
			t.Fatalf("gNOI TLS volume is not a mode-0440 projected volume: %+v", volume)
		}
		if len(volume.Projected.Sources) != 1 || volume.Projected.Sources[0].Secret == nil {
			t.Fatalf("gNOI TLS volume sources=%+v, want one Secret", volume.Projected.Sources)
		}
		projection := volume.Projected.Sources[0].Secret
		if projection.Name != secretName || len(projection.Items) != len(wantKeys) {
			t.Fatalf("gNOI TLS projection=%+v, want Secret %q keys %v", projection, secretName, wantKeys)
		}
		for i, key := range wantKeys {
			if projection.Items[i].Key != key || projection.Items[i].Path != key {
				t.Fatalf("gNOI TLS projection item %d=%+v, want %q", i, projection.Items[i], key)
			}
		}
		return
	}
	t.Fatal("gNOI TLS projected volume is missing")
}

func gnoiTLSSecretMaterial(t *testing.T) (caPEM, certPEM, keyPEM []byte) {
	t.Helper()
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "CVK gNOI test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "CVK gNOI client"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caTemplate, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create client certificate: %v", err)
	}
	clientKeyDER, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		t.Fatalf("marshal client key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: clientKeyDER})
}

func validGNOIProvisioningSecretData(t *testing.T, address string) map[string][]byte {
	t.Helper()
	now := time.Now()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate provisioning root key: %v", err)
	}
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(101),
		Subject:               pkix.Name{CommonName: "CVK test root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		MaxPathLen:            1,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("create provisioning root certificate: %v", err)
	}

	intermediateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate provisioning intermediate key: %v", err)
	}
	intermediateTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(102),
		Subject:               pkix.Name{CommonName: "CVK dedicated test intermediate"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	intermediateDER, err := x509.CreateCertificate(
		rand.Reader,
		intermediateTemplate,
		rootTemplate,
		&intermediateKey.PublicKey,
		rootKey,
	)
	if err != nil {
		t.Fatalf("create provisioning intermediate certificate: %v", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate provisioning leaf key: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(103),
		Subject: pkix.Name{
			CommonName:         address,
			Country:            []string{"US"},
			Province:           []string{"California"},
			Organization:       []string{"Cisco"},
			OrganizationalUnit: []string{"CVK"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP(address)},
	}
	leafDER, err := x509.CreateCertificate(
		rand.Reader,
		leafTemplate,
		intermediateTemplate,
		&leafKey.PublicKey,
		intermediateKey,
	)
	if err != nil {
		t.Fatalf("create provisioning leaf certificate: %v", err)
	}
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})
	intermediatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: intermediateDER})
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	return map[string][]byte{
		"tls.crt":       leafPEM,
		"ca.crt":        append(rootPEM, intermediatePEM...),
		"ca.key":        pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(intermediateKey)}),
		"bootstrap.crt": leafPEM,
	}
}
