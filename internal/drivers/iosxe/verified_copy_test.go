// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package iosxe

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/appartifact"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type checksumTestDelivery struct {
	data           []byte
	stages, checks int
	corrupt        bool
}

func (d *checksumTestDelivery) Stage(_ context.Context, _, _ string, r io.Reader) error {
	d.stages++
	var err error
	d.data, err = io.ReadAll(r)
	if d.corrupt && len(d.data) > 0 {
		d.data[0] ^= 1
	}
	return err
}
func (d *checksumTestDelivery) Verify(_ context.Context, _ string, p VerifiedPackageChecksums) error {
	d.checks++
	h := sha512.Sum512(d.data)
	if p.Package.SizeBytes != int64(len(d.data)) || p.SHA512 != hex.EncodeToString(h[:]) {
		return fmt.Errorf("native SHA512 mismatch")
	}
	return nil
}

func TestCopyDeliveryDerivesNativeHashOnlyFromValidatedArchiveAndRecovers(t *testing.T) {
	for _, fault := range []string{"none", "download", "device"} {
		t.Run(fault, func(t *testing.T) {
			downloads := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				downloads++
				if fault == "download" {
					_, _ = w.Write([]byte("packagX"))
				} else {
					_, _ = w.Write([]byte("package"))
				}
			}))
			defer server.Close()
			key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			resolver, err := appartifact.NewResolver(appartifact.Config{Origin: server.URL, ProducerPublicKeys: []string{string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))}, Platform: appartifact.Platform{OS: "linux", Architecture: "amd64"}, TLSConfig: &tls.Config{RootCAs: roots}})
			if err != nil {
				t.Fatal(err)
			}
			defer resolver.CloseIdleConnections()
			app := minimalAppConfig("https://store/package", core.PullAlways, 0)
			a := &appartifact.VerifiedArtifact{DescriptorDigest: appartifact.Digest([]byte("descriptor")), PackageURL: server.URL + "/v1/packages/sha256/" + appartifact.Digest([]byte("package"))[7:] + "/package.tar", Descriptor: &appartifact.Descriptor{Package: appartifact.Package{Digest: appartifact.Digest([]byte("package")), SizeBytes: 7}}}
			cfg, _ := json.Marshal(app.Spec.DeviceConfig)
			identity := appartifact.Digest([]byte("device\x00" + app.PodUID() + "\x00" + app.ContainerName() + "\x00" + a.DescriptorDigest + "\x00" + appartifact.Digest(cfg)))
			record, _ := json.Marshal(appartifact.Installation{Version: 1, Identity: identity, Phase: "staging"})
			entry := "app-" + appartifact.Digest([]byte(app.AppName()))[7:]
			kube := fake.NewClientset(&core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "journal", Namespace: "operators"}, Data: map[string]string{entry: string(record)}})
			state := ""
			installs := 0
			mutations := 0
			client := &fakeNetworkClient{getHook: func(_ string, result any) error {
				root := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
				if state != "" {
					*root = *operResponse(app.AppName(), state)
				}
				return nil
			}, postHook: func(_ string, _ any) error { mutations++; return nil }, postWithResultHook: func(_ string, payload, result any) error {
				input := payload.(map[string]any)["Cisco-IOS-XE-rpc:app-hosting"].(map[string]any)
				for op, target := range map[string]string{"install": "DEPLOYED", "activate": "ACTIVATED", "start": "RUNNING"} {
					if input[op] != nil {
						if op == "install" {
							installs++
						}
						state = target
						out := result.(*appHostingRPCOutput)
						out.Present = true
						out.Result = app.AppName() + " " + map[string]string{"install": "installed", "activate": "activated", "start": "started"}[op] + " successfully Current state is: " + state
						return nil
					}
				}
				return fmt.Errorf("unexpected operation")
			}}
			delivery := &checksumTestDelivery{corrupt: fault == "device"}
			driver := func() *XEDriver {
				return &XEDriver{config: &v1alpha1.DeviceSpec{Address: "device"}, client: client, verified: &VerifiedRuntime{Resolver: resolver, Journal: appartifact.Journal{Maps: kube.CoreV1().ConfigMaps("operators"), Name: "journal"}, Delivery: delivery}}
			}
			err = driver().installVerified(context.Background(), app, a)
			if fault != "none" {
				if err == nil || installs != 0 || mutations != 0 {
					t.Fatalf("invalid bytes reached config/install: %v installs=%d config=%d", err, installs, mutations)
				}
				if fault == "download" && delivery.stages != 0 {
					t.Fatal("corrupt download reached device transfer")
				}
				return
			}
			if err != nil || installs != 1 || state != "RUNNING" {
				t.Fatalf("valid archive deployment: %v installs=%d state=%s", err, installs, state)
			}
			previous := downloads
			if err = driver().installVerified(context.Background(), app, a); err != nil || installs != 1 || downloads != previous {
				t.Fatalf("healthy replacement worker reinstalled/downloaded: %v", err)
			}
			state = "STOPPED"
			delivery.data[0] ^= 1
			if err = driver().installVerified(context.Background(), app, a); err == nil || state != "STOPPED" {
				t.Fatal("recovery started tampered package")
			}
			delivery.data = []byte("package")
			if err = driver().installVerified(context.Background(), app, a); err != nil || installs != 1 || state != "RUNNING" {
				t.Fatalf("verified recovery failed: %v", err)
			}
		})
	}
}
