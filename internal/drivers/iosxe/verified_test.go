// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package iosxe

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/appartifact"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
	commonpb "github.com/openconfig/gnoi/types"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

type testVerifiedFiles struct {
	data    []byte
	badHash bool
	reads   int
}

func (f *testVerifiedFiles) Put(_ context.Context, _ string, r io.Reader, _ gnoi.PutOpts) error {
	f.data, _ = io.ReadAll(r)
	return nil
}
func (f *testVerifiedFiles) Get(_ context.Context, _ string, w io.Writer) (*commonpb.HashType, error) {
	f.reads++
	if _, e := w.Write(f.data); e != nil {
		return nil, e
	}
	h := sha256.Sum256(f.data)
	if f.badHash {
		h[0] ^= 1
	}
	return &commonpb.HashType{Method: commonpb.HashType_SHA256, Hash: h[:]}, nil
}
func TestStagedPackageChecksExactBytesSizeAndServerHash(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		bad  bool
		ok   bool
	}{{"valid", "package", false, true}, {"same size tamper", "packagX", false, false}, {"oversize", "package!", false, false}, {"truncated", "packag", false, false}, {"lying terminal hash", "package", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			files := &testVerifiedFiles{data: []byte(tc.data), badHash: tc.bad}
			err := verifyStagedPackage(context.Background(), files, "flash:test.tar", appartifact.Package{Digest: appartifact.Digest([]byte("package")), SizeBytes: 7})
			if (err == nil) != tc.ok {
				t.Fatalf("unexpected verification result %v", err)
			}
		})
	}
}
func TestVerifiedRestartObservesPendingInstallWithoutReplaying(t *testing.T) {
	for _, lostOperation := range []string{"install", "activate", "start"} {
		t.Run(lostOperation, func(t *testing.T) { testVerifiedRestart(t, lostOperation) })
	}
}
func testVerifiedRestart(t *testing.T, lostOperation string) {
	ctx := context.Background()
	app := minimalAppConfig("https://store/package", core.PullAlways, 0)
	a := &appartifact.VerifiedArtifact{DescriptorDigest: appartifact.Digest([]byte("descriptor")), Descriptor: &appartifact.Descriptor{Package: appartifact.Package{Digest: appartifact.Digest([]byte("package")), SizeBytes: 7}}}
	cfg, _ := json.Marshal(app.Spec.DeviceConfig)
	identity := appartifact.Digest([]byte("device\x00" + app.PodUID() + "\x00" + app.ContainerName() + "\x00" + a.DescriptorDigest + "\x00" + appartifact.Digest(cfg)))
	key := "app-" + appartifact.Digest([]byte(app.AppName()))[7:]
	raw, _ := json.Marshal(appartifact.Installation{Version: 1, Identity: identity, Phase: "verified"})
	kube := fake.NewClientset(&core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "journal", Namespace: "operators"}, Data: map[string]string{key: string(raw)}})
	journal := appartifact.Journal{Maps: kube.CoreV1().ConfigMaps("operators"), Name: "journal"}
	state := ""
	installCalls := 0
	calls := map[string]int{}
	lost := false
	files := &testVerifiedFiles{data: []byte("package")}
	client := &fakeNetworkClient{getHook: func(_ string, result any) error {
		root := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
		if state != "" {
			*root = *operResponse(app.AppName(), state)
		}
		return nil
	}, postWithResultHook: func(_ string, payload, result any) error {
		input := payload.(map[string]any)["Cisco-IOS-XE-rpc:app-hosting"].(map[string]any)
		if input["install"] != nil {
			installCalls++
		}
		for op, target := range map[string]string{"install": "DEPLOYED", "activate": "ACTIVATED", "start": "RUNNING"} {
			if input[op] != nil {
				calls[op]++
				state = target
				if op == lostOperation && !lost {
					lost = true
					return errors.New("connection lost after acceptance")
				}
				suffix := map[string]string{"install": "installed", "activate": "activated", "start": "started"}[op]
				out := result.(*appHostingRPCOutput)
				out.Present = true
				out.Result = app.AppName() + " " + suffix + " successfully Current state is: " + target
				return nil
			}
		}

		return errors.New("unexpected operation")
	}}
	newDriver := func() *XEDriver {
		return &XEDriver{config: &v1alpha1.DeviceSpec{Address: "device"}, client: client, verified: &VerifiedRuntime{Journal: journal, Files: func(context.Context) (VerifiedFiles, error) { return files, nil }}}
	}

	files.data = []byte("tamper!")
	if err := newDriver().installVerified(ctx, app, a); err == nil || installCalls != 0 {
		t.Fatalf("corrupt staged bytes reached install: %v", err)
	}
	files.data = []byte("package")
	outage := true
	kube.PrependReactor("update", "configmaps", func(ktesting.Action) (bool, kruntime.Object, error) {
		if outage {
			return true, nil, errors.New("API unavailable")
		}
		return false, nil, nil
	})
	if err := newDriver().installVerified(ctx, app, a); err == nil || installCalls != 0 {
		t.Fatalf("undurable intent reached install: %v", err)
	}
	outage = false
	if err := newDriver().installVerified(ctx, app, a); err == nil {
		t.Fatal("expected ambiguous response error")
	}
	pending, _, _ := journal.Load(ctx, key)
	if pending.Phase != lostOperation+"Pending" {
		t.Fatalf("intent not durable: %+v", pending)
	}
	if err := newDriver().installVerified(ctx, app, a); err != nil {
		t.Fatal(err)
	}
	completed, _, _ := journal.Load(ctx, key)
	if completed.Phase != "running" || installCalls != 1 || calls[lostOperation] != 1 {
		t.Fatalf("recovery replayed or did not complete: %+v installs=%d", completed, installCalls)
	}
	state = "STOPPED"
	if e := newDriver().installVerified(ctx, app, a); e != nil || installCalls != 1 || state != "RUNNING" {
		t.Fatalf("stopped app was not safely restarted: %v installs=%d state=%s", e, installCalls, state)
	}
	current, version, e := journal.Load(ctx, key)
	if e != nil {
		t.Fatal(e)
	}
	current.Phase = "deleted"
	if e = journal.Save(ctx, key, version, current); e != nil {
		t.Fatal(e)
	}
	if e = newDriver().installVerified(ctx, app, a); e == nil {
		t.Fatal("deleted application was resumed")
	}
	// A restart cannot adopt a different descriptor under the same app ID.
	a.DescriptorDigest = appartifact.Digest([]byte("changed"))
	if err := newDriver().installVerified(ctx, app, a); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("identity change accepted: %v", err)
	}
}
