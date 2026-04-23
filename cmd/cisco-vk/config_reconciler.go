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
	"os"
	"sync"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	coordv1 "k8s.io/api/coordination/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/intent"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider"
)

// configReconcilerOptions is what startConfigReconciler needs from the
// surrounding cisco-vk-run setup: device spec (for transport build) and
// resolved password. Kept as a struct so signatures don't grow.
type configReconcilerOptions struct {
	Spec     *ciskov1.DeviceSpec
	Password string
	// SessionLock optionally serialises config-driver RESTCONF traffic
	// against the apphosting driver. Recommended in production.
	SessionLock *sync.Mutex
}

// startConfigReconciler builds a controller-runtime client, assembles a
// transport according to CiscoDevice.spec.transport, and starts the
// IOSXEConfig reconciler goroutine tied to ctx. Failure to build any
// piece is returned to the caller — apphosting continues without the
// config driver rather than taking the process down.
func startConfigReconciler(ctx context.Context, cfg *rest.Config, deviceName string, opts configReconcilerOptions) error {
	if cfg == nil {
		return fmt.Errorf("nil rest.Config")
	}
	if deviceName == "" {
		return fmt.Errorf("empty device name")
	}
	if opts.Spec == nil {
		return fmt.Errorf("nil DeviceSpec")
	}

	scheme := k8sruntime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1alpha1.AddToScheme(scheme))
	utilruntime.Must(ciskov1.AddToScheme(scheme))
	utilruntime.Must(coordv1.AddToScheme(scheme))

	c, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("build controller-runtime client: %w", err)
	}

	// Transport construction failure is not fatal: the reconciler can
	// still run in scaffold mode (status=Pending, condition NoTransport).
	t, tErr := transport.For(opts.Spec, opts.Password, transport.FactoryOptions{
		SessionLock: opts.SessionLock,
	})
	if tErr != nil {
		log.G(ctx).WithError(tErr).Warn("IOSXEConfig transport unavailable; driver will report Pending")
	}

	// Lease namespace tracks the cisco-vk run pod's namespace so the
	// leases land alongside the process that owns them. In-cluster
	// deployments always have POD_NAMESPACE; out-of-cluster dev falls
	// back to "default".
	leaseNamespace := os.Getenv("POD_NAMESPACE")
	if leaseNamespace == "" {
		leaseNamespace = "default"
	}

	r := &provider.ConfigReconciler{
		Client:     c,
		DeviceName: deviceName,
		Transport:  t, // may be nil
		KeyRules:   keyRulesForPhase1(),
		Leaser: &engine.FamilyLeaser{
			Client:    c,
			Namespace: leaseNamespace,
		},
	}

	go func() {
		if runErr := r.Run(ctx); runErr != nil && runErr != context.Canceled {
			log.G(ctx).WithError(runErr).Warn("IOSXEConfig reconciler exited with error")
		}
	}()
	return nil
}

// keyRulesForPhase1 returns the path → key-field rules the merger uses
// for YANG-keyed lists in the Phase-1 families. Phase-4 replaces this
// with a rule set derived from schema/families.yaml.
func keyRulesForPhase1() intent.KeyRules {
	return intent.KeyRules{
		"vlan.vlans":                      "id",
		"vrf.vrfs":                        "name",
		"interface_ethernet.interfaces":   "name",
		"interface_loopback.interfaces":   "name",
		"interface_virtual_port_group.interfaces": "id",
		"dhcp.pools":                      "name",
		"access_list_extended.extended":   "name",
	}
}
