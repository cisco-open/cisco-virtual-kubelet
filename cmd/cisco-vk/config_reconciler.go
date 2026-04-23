// Copyright © 2026 Cisco Systems, Inc.
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

package main

import (
	"context"
	"fmt"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider"
)

// startConfigReconciler wires a Phase-0 IOSXEConfig reconciler onto the
// running cisco-vk process. The reconciler runs as a goroutine tied to ctx;
// the caller retains ownership of ctx's lifecycle so shutting the provider
// down also stops the reconciler.
//
// The function returns an error only when the pre-requisites — scheme,
// client, non-empty device identity — cannot be established. A started
// reconciler that subsequently fails is handled inside the goroutine so
// apphosting is not brought down by a transient API-server issue.
func startConfigReconciler(ctx context.Context, cfg *rest.Config, deviceName string) error {
	if cfg == nil {
		return fmt.Errorf("nil rest.Config")
	}
	if deviceName == "" {
		return fmt.Errorf("empty device name")
	}

	scheme := k8sruntime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1alpha1.AddToScheme(scheme))

	c, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("build controller-runtime client: %w", err)
	}

	r := &provider.ConfigReconciler{
		Client:     c,
		DeviceName: deviceName,
		Driver:     configdriver.NewStubDriver(),
	}

	go func() {
		if runErr := r.Run(ctx); runErr != nil && runErr != context.Canceled {
			log.G(ctx).WithError(runErr).Warn("IOSXEConfig reconciler exited with error")
		}
	}()

	return nil
}
