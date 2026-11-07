// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	v1 "k8s.io/api/core/v1"
)

func TestVerifiedProviderFailsBeforeLifecycleStarts(t *testing.T) {
	spec := &v1alpha1.DeviceSpec{AppArtifacts: &v1alpha1.AppArtifactConfig{Mode: v1alpha1.AppArtifactModeVerified}}
	ctx := context.Background()
	if _, err := NewAppHostingProvider(ctx, spec, nodeutil.ProviderConfig{}, nil, nil, nil); !errors.Is(err, common.ErrVerifiedArtifactsUnsupported) {
		t.Fatalf("provider construction: %v", err)
	}
	// Also protect callbacks when a provider is constructed directly or its
	// config is replaced. No driver or maintenance coordinator may be used.
	provider := &AppHostingProvider{deviceSpec: spec}
	for _, run := range []func(context.Context, *v1.Pod) error{provider.CreatePod, provider.UpdatePod} {
		if err := run(ctx, &v1.Pod{}); err == nil || !strings.Contains(err.Error(), "VerifiedArtifactsUnsupported") {
			t.Fatalf("provider callback: %v", err)
		}
	}
}
