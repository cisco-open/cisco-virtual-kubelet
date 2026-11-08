// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package iosxe

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	v1 "k8s.io/api/core/v1"
)

func TestVerifiedRejectsIOSXEForwardPathsBeforeDeviceAccess(t *testing.T) {
	// A nil client makes any accidental device access fail this test.
	d := &XEDriver{config: &v1alpha1.DeviceSpec{AppArtifacts: &v1alpha1.AppArtifactConfig{Mode: v1alpha1.AppArtifactModeVerified}}}
	ctx := context.Background()
	app := minimalAppConfig("https://store.example/package.tar", v1.PullAlways, 0)
	for name, run := range map[string]func() error{
		"deploy":                    func() error { return d.DeployPod(ctx, lifecycleTestPod(), nil, nil) },
		"update":                    func() error { return d.UpdatePod(ctx, lifecycleTestPod()) },
		"recovery create":           func() error { return d.CreateAppHostingApp(ctx, app) },
		"install":                   func() error { return d.appHostingRPC(ctx, "install", "app", nil) },
		"activate":                  func() error { return d.appHostingRPC(ctx, "activate", "app", nil) },
		"start":                     func() error { return d.appHostingRPC(ctx, "start", "app", nil) },
		"disable native signatures": func() error { return d.ConfigureSignVerification(ctx, false) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); !errors.Is(err, common.ErrVerifiedArtifactsUnsupported) {
				t.Fatalf("got %v, want unsupported Verified", err)
			}
		})
	}
	d.ReconcileApp(ctx, app)
	if app.Status.Phase != AppPhaseError || !strings.Contains(app.Status.Message, "VerifiedArtifactsUnsupported") {
		t.Fatalf("recovery did not fail closed: %+v", app.Status)
	}
}

func TestVerifiedDoesNotBlockIOSXEStop(t *testing.T) {
	called := false
	d := newTestDriver(&fakeNetworkClient{postWithResultHook: func(_ string, _, result any) error {
		called = true
		return errors.New("sentinel: reached device stop")
	}})
	d.config = &v1alpha1.DeviceSpec{AppArtifacts: &v1alpha1.AppArtifactConfig{Mode: v1alpha1.AppArtifactModeVerified}}
	err := d.appHostingRPC(context.Background(), "stop", "app", nil)
	if !called || errors.Is(err, common.ErrVerifiedArtifactsUnsupported) {
		t.Fatalf("stop was blocked: %v", err)
	}
}
