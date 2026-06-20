// Copyright © 2026 Cisco Systems Inc.
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

package aggregator

import (
	"context"
	"fmt"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider"
)

func (r *AggregatedReconciler) startNXOSWorker(dev *ciskov1.CiscoDevice, password, hash string) error {
	dctx, err := drivers.NewConfigDriver(r.rootCtx, &dev.Spec, password, drivers.ConfigDriverOptions{})
	if err != nil && (dctx == nil || dctx.Transport == nil) {
		return fmt.Errorf("nxos config driver context: %w", err)
	}
	if dctx == nil {
		return fmt.Errorf("nxos config driver context: returned nil")
	}

	leaseNs := r.LeaseNamespace
	if leaseNs == "" {
		leaseNs = dev.Namespace
	}
	notify := startSubscribeWatcher(r.rootCtx, dctx)
	devCtx, cancel := context.WithCancel(r.rootCtx)
	rec := &provider.NXOSConfigReconciler{
		Client:             r.Client,
		DeviceName:         dev.Name,
		// DeviceNamespace scopes reconciliation to this device's namespace.
		// deviceRef is same-namespace by contract; without this the worker
		// would reconcile NXOSConfig objects from any namespace whose
		// deviceRef.name matches, crossing the tenant/device boundary and
		// pushing another namespace's intent to this physical device.
		DeviceNamespace:    dev.Namespace,
		Transport:          dctx.Transport,
		Lookup:             dctx.LookupWriter,
		FamilyOrder:        dctx.FamilyOrder,
		DeviceVersion:      dctx.DeviceVersion,
		DefaultYANGVersion: dctx.DefaultYANGVersion,
		Leaser:             &engine.FamilyLeaser{Client: r.Client, Namespace: leaseNs},
		Recorder:           r.Recorder,
		SubscribeNotify:    notify,
		RuntimeID:          newWorkerRuntimeID(),
	}

	r.mu.Lock()
	r.managed[devKey(dev)] = &deviceWorker{cancel: cancel, specHash: hash}
	r.mu.Unlock()

	go func() {
		defer func() {
			cancel()
			if dctx.Transport != nil {
				_ = dctx.Transport.Close()
			}
		}()
		_ = rec.Run(devCtx)
	}()
	return nil
}
