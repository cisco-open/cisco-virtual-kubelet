// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	"github.com/spf13/viper"
)

func TestRenderAppArtifactTrustRoundTrip(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	trust := &ciskov1.AppArtifactConfig{Mode: ciskov1.AppArtifactModeVerified, StoreURL: "https://artifacts.example.test:8443", ProducerPublicKeys: []string{string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))}, Platform: &ciskov1.AppArtifactPlatform{OS: "linux", Architecture: "amd64"}, MaxPackageBytes: 1024}
	spec := &ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverFAKE, AppArtifacts: trust}
	out, err := renderDeviceConfig(spec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(out), 0600); err != nil {
		t.Fatal(err)
	}
	viper.Reset()
	t.Cleanup(viper.Reset)
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Device.AppArtifacts, trust) {
		t.Fatal("rendered worker configuration lost or changed operator artifact trust")
	}
	if !reflect.DeepEqual(spec.AppArtifacts, trust) {
		t.Fatal("rendering changed operator configuration")
	}
}

func TestRenderRejectsInvalidAppArtifactTrust(t *testing.T) {
	if _, err := renderDeviceConfig(&ciskov1.DeviceSpec{AppArtifacts: &ciskov1.AppArtifactConfig{Mode: "Governed"}}); err == nil {
		t.Fatal("render accepted unknown mode")
	}
	out, err := renderDeviceConfig(&ciskov1.DeviceSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "appArtifacts") {
		t.Fatal("omitted Standard adds artifact configuration")
	}
}

func TestReconcileRejectsAppArtifactTrustBeforeMutations(t *testing.T) {
	device := newDevice("invalid-artifact", "default")
	device.Spec.AppArtifacts = &ciskov1.AppArtifactConfig{Mode: "Governed"}
	r := reconcilerFor(t, device)
	_, err := r.Reconcile(context.Background(), reconcileRequest(device.Namespace, device.Name))
	if err == nil || !strings.Contains(err.Error(), "appArtifacts") {
		t.Fatalf("reconcile error=%v", err)
	}
	var actual ciskov1.CiscoDevice
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: device.Namespace, Name: device.Name}, &actual); err != nil {
		t.Fatal(err)
	}
	if len(actual.Finalizers) != 0 {
		t.Fatal("invalid configuration caused finalizer mutation")
	}
	var deployments appsv1.DeploymentList
	var configs corev1.ConfigMapList
	var accounts corev1.ServiceAccountList
	if err := r.List(context.Background(), &deployments); err != nil {
		t.Fatal(err)
	}
	if err := r.List(context.Background(), &configs); err != nil {
		t.Fatal(err)
	}
	if err := r.List(context.Background(), &accounts); err != nil {
		t.Fatal(err)
	}
	if len(deployments.Items)+len(configs.Items)+len(accounts.Items) != 0 {
		t.Fatal("invalid configuration created worker resources")
	}
}

func TestStandardAppArtifactsAddsNoWorkerRequirements(t *testing.T) {
	var baseline *corev1.PodSpec
	for _, artifactConfig := range []*ciskov1.AppArtifactConfig{nil, {}, {Mode: ciskov1.AppArtifactModeStandard}} {
		device := newDevice("standard-artifact", "default")
		device.Spec.AppArtifacts = artifactConfig
		r := reconcilerFor(t, device)
		if _, err := r.Reconcile(context.Background(), reconcileRequest(device.Namespace, device.Name)); err != nil {
			t.Fatal(err)
		}
		var deployment appsv1.Deployment
		if err := r.Get(context.Background(), types.NamespacedName{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}, &deployment); err != nil {
			t.Fatal(err)
		}
		if baseline == nil {
			baseline = deployment.Spec.Template.Spec.DeepCopy()
		} else if !reflect.DeepEqual(*baseline, deployment.Spec.Template.Spec) {
			t.Fatal("Standard mode added worker mounts, resources, credentials or runtime options")
		}
		var configs corev1.ConfigMapList
		if err := r.List(context.Background(), &configs); err != nil {
			t.Fatal(err)
		}
		if len(configs.Items) != 1 {
			t.Fatalf("Standard mode requires %d ConfigMaps, want only the existing worker config", len(configs.Items))
		}
	}
}
