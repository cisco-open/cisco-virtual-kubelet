// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestDeviceTLSProjectsOnlyPublicCA(t *testing.T) {
	device := newDevice("device-ca", "default")
	device.Spec.TLS = &ciskov1.TLSConfig{}
	if err := json.Unmarshal([]byte(`{"enabled":true,"caSecretRef":{"name":"device-trust"}}`), device.Spec.TLS); err != nil {
		t.Fatal(err)
	}
	ca, _, key := gnoiTLSSecretMaterial(t)
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "device-trust", Namespace: device.Namespace},
		Data: map[string][]byte{"ca.crt": ca, "ca.key": key, "tls.key": key}}
	r := reconcilerFor(t, device, secret)
	ctx := context.Background()
	if _, err := r.Reconcile(ctx, reconcileRequest(device.Namespace, device.Name)); err != nil {
		t.Fatal(err)
	}
	var deployment appsv1.Deployment
	if err := r.Get(ctx, client.ObjectKey{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}, &deployment); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.Name != "device-tls-ca" {
			continue
		}
		found = true
		p := volume.Projected
		if p == nil || len(p.Sources) != 1 || p.Sources[0].ConfigMap == nil || p.Sources[0].Secret != nil {
			t.Fatalf("invalid CA projection: %+v", volume)
		}
		ref := p.Sources[0].ConfigMap
		if ref.Name != device.Name+configMapSuffix || len(ref.Items) != 1 || ref.Items[0].Key != "device-ca.crt" || ref.Items[0].Path != "ca.crt" {
			t.Fatalf("CA projection exposed extra keys: %+v", ref)
		}
	}
	if !found {
		t.Fatal("device public CA was not projected")
	}
	var cm corev1.ConfigMap
	if err := r.Get(ctx, client.ObjectKey{Namespace: device.Namespace, Name: device.Name + configMapSuffix}, &cm); err != nil {
		t.Fatal(err)
	}
	config := cm.Data[configFileName]
	if !strings.Contains(config, "caFile: /var/run/secrets/cisco-vk/device-tls-ca/ca.crt") || strings.Contains(config, "caSecretRef") || strings.Contains(config, string(key)) || strings.Contains(config, string(ca)) {
		t.Fatal("device trust was not resolved to a public-CA-only file reference")
	}
	if cm.Data["device-ca.crt"] != string(ca) || strings.Contains(cm.Data["device-ca.crt"], string(key)) {
		t.Fatal("validated public CA snapshot was not isolated from the mutable Secret")
	}
}

func TestDeviceTLSCARotationAndRemovalReplaceTrust(t *testing.T) {
	ctx := context.Background()
	device := newDevice("device-rotation", "default")
	device.Spec.TLS = &ciskov1.TLSConfig{Enabled: true, CASecretRef: &ciskov1.DeviceTLSCASecretReference{Name: "ca"}}
	ca, leaf, _ := gnoiTLSSecretMaterial(t)
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "ca", Namespace: device.Namespace}, Data: map[string][]byte{"ca.crt": ca}}
	r := reconcilerFor(t, device, secret)
	var previous string
	for _, scenario := range []string{"valid", "rotated", "invalid", "leaf-in-ca", "private-key-in-ca", "missing", "recreated"} {
		t.Run(scenario, func(t *testing.T) {
			switch scenario {
			case "rotated", "invalid", "leaf-in-ca", "private-key-in-ca":
				if err := r.Get(ctx, client.ObjectKeyFromObject(secret), secret); err != nil {
					t.Fatal(err)
				}
				secret.Data["ca.crt"] = ca
				secret.Annotations = map[string]string{"rotation": scenario}
				if scenario == "invalid" {
					secret.Data["ca.crt"] = []byte("not a certificate")
				}
				if scenario == "leaf-in-ca" {
					secret.Data["ca.crt"] = leaf
				}
				if scenario == "private-key-in-ca" {
					secret.Data["ca.crt"] = append(append([]byte{}, ca...), []byte("-----BEGIN PRIVATE KEY-----\naccidental-private-material\n-----END PRIVATE KEY-----\n")...)
				}
				if err := r.Update(ctx, secret); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := r.Delete(ctx, secret); err != nil {
					t.Fatal(err)
				}
			case "recreated":
				secret.ResourceVersion, secret.UID = "", "replacement"
				secret.Data["ca.crt"] = ca
				if err := r.Create(ctx, secret); err != nil {
					t.Fatal(err)
				}
			}
			_, err := r.Reconcile(ctx, reconcileRequest(device.Namespace, device.Name))
			invalid := scenario == "invalid" || scenario == "missing" || scenario == "private-key-in-ca" || scenario == "leaf-in-ca"
			if (err != nil) != invalid {
				t.Fatalf("reconcile error=%v invalid=%v", err, invalid)
			}
			var d appsv1.Deployment
			if err := r.Get(ctx, client.ObjectKey{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}, &d); err != nil {
				t.Fatal(err)
			}
			revision := d.Spec.Template.Annotations[managedprotocol.AnnotationDeviceTLSCARevision]
			if revision == "" || revision == previous {
				t.Fatalf("trust revision not changed: %q", revision)
			}
			previous = revision
			if invalid {
				var currentConfig corev1.ConfigMap
				if err := r.Get(ctx, client.ObjectKey{Namespace: device.Namespace, Name: device.Name + configMapSuffix}, &currentConfig); err != nil {
					t.Fatal(err)
				}
				if _, exists := currentConfig.Data[deviceTLSCAConfigKey]; exists {
					t.Fatal("invalid/private source material survived in the public snapshot")
				}
				for _, volume := range d.Spec.Template.Spec.Volumes {
					if volume.Name == deviceTLSCAVolume && (volume.Projected != nil || volume.EmptyDir == nil) {
						t.Fatal("invalid/private CA material was still projected")
					}
				}
			}
			if d.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
				t.Fatal("trust replacement must not retain old worker while invalid new trust waits")
			}
			if requests := r.mapSecretToCiscoDevices(ctx, secret); len(requests) != 1 || requests[0].Name != device.Name {
				t.Fatalf("rotation event not mapped: %v", requests)
			}
			foreign := secret.DeepCopy()
			foreign.Namespace = "other"
			if len(r.mapSecretToCiscoDevices(ctx, foreign)) != 0 {
				t.Fatal("cross-namespace secret matched")
			}
		})
	}
}

func TestDeviceTLSCARejectsConflictingConfiguration(t *testing.T) {
	for _, scenario := range []string{"disabled", "insecure", "file", "empty", "invalid-name", "aggregator"} {
		t.Run(scenario, func(t *testing.T) {
			device := newDevice("device-invalid", "default")
			device.Spec.TLS = &ciskov1.TLSConfig{Enabled: true, CASecretRef: &ciskov1.DeviceTLSCASecretReference{Name: "ca"}}
			switch scenario {
			case "disabled":
				device.Spec.TLS.Enabled = false
			case "insecure":
				device.Spec.TLS.InsecureSkipVerify = true
			case "file":
				device.Spec.TLS.CAFile = "/tmp/other-ca"
			case "empty":
				device.Spec.TLS.CASecretRef.Name = ""
			case "invalid-name":
				device.Spec.TLS.CASecretRef.Name = "../ca"
			}
			r := reconcilerFor(t, device)
			r.AggregatorEnabled = scenario == "aggregator"
			if _, err := r.Reconcile(context.Background(), reconcileRequest(device.Namespace, device.Name)); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestDeviceTLSCAPreservedInBothManagedPlanes(t *testing.T) {
	t.Setenv(envCVKGNOIDisabled, "true")
	ctx := context.Background()
	device := newDevice("two-planes-ca", "edge")
	device.UID = "two-planes-ca-uid"
	device.Spec.PhysicalIdentity = "serial-two-planes-ca"
	device.Labels = map[string]string{managedprotocol.AnnotationManaged: "true", topology.CiscoTopologyLabelPrefix + "site": "site-a"}
	device.Spec.TLS = &ciskov1.TLSConfig{Enabled: true, CASecretRef: &ciskov1.DeviceTLSCASecretReference{Name: "ca"}}
	appUser := "system:serviceaccount:edge:" + managedprotocol.AppHostingServiceAccount
	networkUser := "system:serviceaccount:edge:" + managedprotocol.NetworkManagementServiceAccount
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: device.Name, UID: "node-uid", Labels: map[string]string{topology.LabelType: topology.TypeVirtualKubelet}, Annotations: map[string]string{
		managedprotocol.AnnotationManaged: "true", managedprotocol.AnnotationDeviceNamespace: device.Namespace,
		managedprotocol.AnnotationDeviceName: device.Name, managedprotocol.AnnotationDeviceUID: string(device.UID),
		managedprotocol.AnnotationNodeName: device.Name, managedprotocol.AnnotationNodeUID: "node-uid",
		managedprotocol.AnnotationWorkerUsername: appUser, managedprotocol.AnnotationAppWorkerUsername: appUser,
		managedprotocol.AnnotationNetworkWorkerUsername: networkUser, managedprotocol.AnnotationWorkerProtocol: managedprotocol.Version,
	}}}
	ca, _, key := gnoiTLSSecretMaterial(t)
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "ca", Namespace: device.Namespace}, Data: map[string][]byte{"ca.crt": ca, "ca.key": key}}
	policy, ledger := managedPolicyAndLedger(t, nil)
	r := reconcilerFor(t, device, node, policy, ledger, secret)
	if err := ops.AddToScheme(r.Scheme); err != nil {
		t.Fatal(err)
	}
	r.Client = leaseUIDAssigningClient{Client: &serviceAccountUIDAssigningClient{Client: r.Client}}
	r.APIReader = r.Client
	r.ManagedTopology, r.TopologyPolicyNamespace, r.TopologyPolicyName = true, policy.Namespace, policy.Name
	r.WorkerServiceAccountPolicyEpoch = "sha256:public-ca-test"
	for attempt := 0; attempt < 3; attempt++ {
		_, err := r.Reconcile(ctx, reconcileRequest(device.Namespace, device.Name))
		var fence *managedWorkerRevisionFence
		if err != nil && !errors.As(err, &fence) {
			t.Fatal(err)
		}
	}
	for _, name := range []string{device.Name + deploymentSuffix, networkDeploymentName(string(device.UID))} {
		var d appsv1.Deployment
		if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: name}, &d); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, volume := range d.Spec.Template.Spec.Volumes {
			if volume.Name == deviceTLSCAVolume {
				found = true
				if volume.Projected.Sources[0].Secret != nil || volume.Projected.Sources[0].ConfigMap == nil || !reflect.DeepEqual(volume.Projected.Sources[0].ConfigMap.Items, []corev1.KeyToPath{{Key: deviceTLSCAConfigKey, Path: "ca.crt"}}) {
					t.Fatal("private material exposed")
				}
			}
		}
		if !found {
			t.Fatalf("%s missing device CA", name)
		}
		revision, err := managedWorkerPodTemplateRevision(&d.Spec.Template)
		if err != nil || revision != d.Spec.Template.Annotations[managedprotocol.AnnotationWorkerConfigRevision] {
			t.Fatalf("%s template is not revision-bound: %v", name, err)
		}
	}
}
