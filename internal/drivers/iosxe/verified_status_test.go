// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package iosxe

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/appartifact"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/openconfig/ygot/ygot"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestVerifiedRunningStatusDoesNotAcquireWriteFenceOrStagePackage(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	public := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	manifest := appartifact.Digest([]byte("manifest"))
	descriptor := appartifact.Descriptor{APIVersion: appartifact.APIVersion, Kind: "AppHostingArtifact", CreatedAt: "2026-11-02T00:00:00Z", Source: appartifact.Source{Registry: "registry.example.test", Repository: "agent", RequestedDigest: manifest, ManifestDigest: manifest, ConfigDigest: appartifact.Digest([]byte("config")), Platform: appartifact.Platform{OS: "linux", Architecture: "amd64"}}, Package: appartifact.Package{Format: "docker-save-v1", MediaType: "application/x-tar", Digest: appartifact.Digest([]byte("package")), SizeBytes: 7}, Producer: appartifact.Producer{Name: "fixture", Version: "1", BuildDigest: appartifact.Digest([]byte("build")), RecipeDigest: appartifact.Digest([]byte("recipe"))}, Provenance: appartifact.Evidence{Digest: appartifact.Digest([]byte("provenance")), MediaType: "application/vnd.in-toto+json"}}
	raw, _ := json.Marshal(descriptor)
	sum := sha256.Sum256(raw)
	signature, _ := ecdsa.SignASN1(rand.Reader, key, sum[:])
	bundle, _ := json.Marshal(map[string]any{"mediaType": "application/vnd.dev.sigstore.bundle.v0.3+json", "verificationMaterial": map[string]any{"publicKey": map[string]string{"hint": "fixture"}}, "messageSignature": map[string]any{"messageDigest": map[string]string{"algorithm": "SHA2_256", "digest": base64.StdEncoding.EncodeToString(sum[:])}, "signature": base64.StdEncoding.EncodeToString(signature)}})
	packageRequests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".sigstore.json"):
			_, _ = w.Write(bundle)
		case strings.HasSuffix(r.URL.Path, ".json"):
			_, _ = w.Write(raw)
		default:
			packageRequests++
			t.Error("healthy status fetched package")
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	resolver, err := appartifact.NewResolver(appartifact.Config{Origin: server.URL, ProducerPublicKeys: []string{public}, Platform: descriptor.Source.Platform, TLSConfig: &tls.Config{RootCAs: roots}})
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.CloseIdleConnections()
	pod := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "agent", Namespace: "apps", UID: "12345678-1234-1234-1234-123456789012", Annotations: map[string]string{appartifact.BindingAnnotation: `{"agent":{"descriptorDigest":"` + appartifact.Digest(raw) + `"}}`}}, Spec: core.PodSpec{Containers: []core.Container{{Name: "agent", Image: server.URL + "/v1/packages/sha256/" + descriptor.Package.Digest[7:] + "/package.tar"}}}}
	kube := fake.NewClientset(&core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "journal", Namespace: "operators"}})
	journal := appartifact.Journal{Maps: kube.CoreV1().ConfigMaps("operators"), Name: "journal"}
	state := "RUNNING"
	appID := common.GenerateContainerAppIDs(pod)["agent"]
	client := &fakeNetworkClient{getHook: func(_ string, result any) error {
		root := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
		*root = *operResponse(appID, state)
		root.App[appID].NetworkInterfaces = &Cisco_IOS_XEAppHostingOper_AppHostingOperData_App_NetworkInterfaces{NetworkInterface: map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App_NetworkInterfaces_NetworkInterface{"00:11:22:33:44:55": {Ipv4Address: ygot.String("192.0.2.10")}}}
		return nil
	}, postHook: func(_ string, _ any) error { t.Fatal("status sent native mutation"); return nil }, postWithResultHook: func(_ string, _, _ any) error { t.Fatal("status sent lifecycle RPC"); return nil }}
	d := &XEDriver{config: &v1alpha1.DeviceSpec{Address: "192.0.2.1"}, client: client, verified: &VerifiedRuntime{Resolver: resolver, Journal: journal, Delivery: &checksumTestDelivery{}}}
	apps, artifacts, err := d.prepareVerifiedAppsLocked(context.Background(), pod, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	identity, _, err := d.verifiedIdentity(&apps[0], artifacts["agent"])
	if err != nil {
		t.Fatal(err)
	}
	entry := "app-" + appartifact.Digest([]byte(appID))[7:]
	_, version, _ := journal.Load(context.Background(), entry)
	record := appartifact.Installation{Identity: identity, Phase: "running", SourceImage: "fixture@" + manifest}
	if err = journal.Save(context.Background(), entry, version, record); err != nil {
		t.Fatal(err)
	}
	guardCalls := 0
	pod.Status.Reason = "ProviderFailed"
	pod.Status.Message = "previous mutation lease warning"
	d.SetMaintenanceMutationGuard(func(ctx context.Context) (context.Context, func(error), error) {
		guardCalls++
		return ctx, nil, fmt.Errorf("guarded recovery required")
	})
	for i := 0; i < 3; i++ {
		status, err := d.verifiedPodStatus(context.Background(), pod)
		if err != nil || status.Status.Phase != core.PodRunning || status.Status.PodIP != "192.0.2.10" || status.Status.HostIP != "192.0.2.1" {
			t.Fatalf("healthy observed status=%+v err=%v", status, err)
		}
		if status.Status.Reason != "" || status.Status.Message != "" {
			t.Fatalf("healthy status retained failure text: %+v", status.Status)
		}
	}
	if pod.Status.Reason != "ProviderFailed" || pod.Status.Message != "previous mutation lease warning" {
		t.Fatal("status observation mutated the informer Pod")
	}
	if guardCalls != 0 || packageRequests != 0 {
		t.Fatal("healthy status acquired mutation authority")
	}
	changed := pod.DeepCopy()
	changed.Spec.Containers[0].Resources.Requests = core.ResourceList{core.ResourceMemory: resource.MustParse("256Mi")}
	if e := d.deployVerified(context.Background(), changed, nil, nil); e == nil || !devicecoordination.IsNoDeviceMutation(e) {
		t.Fatalf("changed configuration must be a read-only rejection: %v", e)
	}
	d.verified.mu.Lock()
	busy, busyErr := d.verifiedPodStatus(context.Background(), pod)
	d.verified.mu.Unlock()
	if busyErr != nil || busy.Status.Phase != core.PodPending || guardCalls != 0 {
		t.Fatal("busy observer acquired a write lease or blocked behind installation")
	}
	if busy.Status.Reason != "VerifiedInstallationBlocked" || busy.Status.Message != "Verified installation or recovery is in progress" || busy.Status.Message != busy.Status.ContainerStatuses[0].State.Waiting.Message {
		t.Fatalf("busy status did not replace stale failure text: %+v", busy.Status)
	}
	recoveredInput := pod.DeepCopy()
	recoveredInput.Status = busy.Status
	recovered, _ := d.verifiedPodStatus(context.Background(), recoveredInput)
	if recovered.Status.Phase != core.PodRunning || recovered.Status.Reason != "" || recovered.Status.Message != "" {
		t.Fatalf("recovery retained blocked diagnostic text: %+v", recovered.Status)
	}
	// A native RUNNING snapshot cannot complete a pending journal outside a guard.
	_, version, _ = journal.Load(context.Background(), entry)
	record.Phase = "installPending"
	_ = journal.Save(context.Background(), entry, version, record)
	status, _ := d.verifiedPodStatus(context.Background(), pod)
	if guardCalls != 1 || status.Status.Phase != core.PodPending {
		t.Fatal("pending operation bypassed guarded recovery")
	}
	if status.Status.Reason != "VerifiedInstallationBlocked" || status.Status.Message != "guarded recovery required" {
		t.Fatalf("pending status did not report current recovery failure: %+v", status.Status)
	}
	_, version, _ = journal.Load(context.Background(), entry)
	record.Phase = "running"
	_ = journal.Save(context.Background(), entry, version, record)
	state = "STOPPED"
	status, _ = d.verifiedPodStatus(context.Background(), pod)
	if guardCalls != 2 || status.Status.Phase != core.PodPending {
		t.Fatal("stopped app bypassed guarded restart")
	}
	// An invalid binding remains blocked even when the old native app is RUNNING.
	state = "RUNNING"
	pod.Spec.Containers[0].Image = server.URL + "/different.tar"
	status, _ = d.verifiedPodStatus(context.Background(), pod)
	if guardCalls != 2 || status.Status.Phase != core.PodPending {
		t.Fatal("mismatched artifact was accepted by read-only status")
	}
}

// Verified receives the original Pod in lifecycle callbacks and informer-based
// recovery. Bulk expansion must fail explicitly instead of installing an app
// with silently missing environment variables.
func TestVerifiedRejectsEnvFromBeforeResolvingOrMutating(t *testing.T) {
	d := &XEDriver{verified: &VerifiedRuntime{}}
	pod := &core.Pod{ObjectMeta: metav1.ObjectMeta{UID: "uid"}, Spec: core.PodSpec{Containers: []core.Container{{Name: "agent", EnvFrom: []core.EnvFromSource{{ConfigMapRef: &core.ConfigMapEnvSource{LocalObjectReference: core.LocalObjectReference{Name: "environment"}}}}}}}}
	_, _, err := d.prepareVerifiedAppsLocked(context.Background(), pod, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "envFrom is unsupported") {
		t.Fatalf("unexpected error: %v", err)
	}
}
