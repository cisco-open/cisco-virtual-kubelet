// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package nxos

import (
	"context"
	"errors"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
)

func TestVerifiedRejectsNXOSForwardPathsBeforeDeviceAccess(t *testing.T) {
	d := &NXOSDriver{config: &v1alpha1.DeviceSpec{AppArtifacts: &v1alpha1.AppArtifactConfig{Mode: v1alpha1.AppArtifactModeVerified}}}
	ctx := context.Background()
	cfg := &nxosAppConfig{AppID: "app", ImagePath: "https://store.example/package.tar"}
	for name, run := range map[string]func() error{
		"deploy":                    func() error { return d.DeployPod(ctx, nxosTestPod(), nil, nil) },
		"update":                    func() error { return d.UpdatePod(ctx, nxosTestPod()) },
		"recovery create":           func() error { return d.createApp(ctx, cfg) },
		"configure":                 func() error { return d.ensureAppConfig(ctx, cfg) },
		"disable native signatures": func() error { return d.ConfigureSignVerification(ctx, false) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); !errors.Is(err, common.ErrVerifiedArtifactsUnsupported) {
				t.Fatalf("got %v, want unsupported Verified", err)
			}
		})
	}
	for _, state := range []string{"", "DEPLOYED", "ACTIVATED", "STOPPED", "RUNNING"} {
		if err := d.advanceAppState(ctx, cfg, state); !errors.Is(err, common.ErrVerifiedArtifactsUnsupported) {
			t.Fatalf("recovery state %q: got %v", state, err)
		}
	}
}
