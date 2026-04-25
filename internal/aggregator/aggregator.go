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

// Package aggregator hosts the single-manager topology option:
// a controller-runtime reconciler that watches CiscoDevices and
// runs an in-process ConfigReconciler per device, instead of
// spawning one cisco-vk pod per device.
//
// After Phase 9 the aggregator is fully platform-agnostic. The
// per-device worker pulls a `drivers.ConfigDriverContext` from the
// platform registry; transport, key rules, writer lookup, and
// Subscribe-watch paths all come from there. The aggregator never
// imports any platform-specific package.
//
// Adding a new platform never edits this file. Drop a register.go
// in the new platform package, blank-import it from cmd/cisco-vk/
// drivers_register.go, and the aggregator picks it up.
package aggregator

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider"
)

// AggregatedReconciler watches CiscoDevices and runs one in-process
// ConfigReconciler per device that has a registered config driver
// in the platform registry. Devices whose Driver kind isn't
// registered are silently skipped — that's how the per-pod
// topology coexists with this one for unsupported platforms.
type AggregatedReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	// LeaseNamespace is the shared namespace family leases land
	// in. Empty falls back to each device's own namespace.
	LeaseNamespace string

	mu      sync.Mutex
	managed map[string]*deviceWorker
	rootCtx context.Context
}

// deviceWorker owns one device's reconciler goroutine.
type deviceWorker struct {
	cancel   context.CancelFunc
	specHash string
}

// +kubebuilder:rbac:groups=cisco.vk,resources=ciscodevices,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *AggregatedReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	r.rootCtx = ctx
	if r.managed == nil {
		r.managed = map[string]*deviceWorker{}
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&ciskov1.CiscoDevice{}).
		Complete(r)
}

func (r *AggregatedReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var dev ciskov1.CiscoDevice
	if err := r.Get(ctx, req.NamespacedName, &dev); err != nil {
		if apierrors.IsNotFound(err) {
			r.stopWorker(req.String())
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Platforms without a registered config driver: silent skip.
	// Operators see the device through the existing per-pod flow
	// (or no flow at all if the platform isn't registered for
	// apphosting either).
	if !drivers.ConfigDriverRegistered(dev.Spec.Driver) {
		r.stopWorker(req.String())
		return ctrl.Result{}, nil
	}

	pwd, err := r.resolvePassword(ctx, &dev)
	if err != nil {
		if r.Recorder != nil {
			r.Recorder.Eventf(&dev, corev1.EventTypeWarning, "AggregatorCredentialFailed",
				"could not resolve credential: %v", err)
		}
		return ctrl.Result{}, nil
	}

	hash := specHash(&dev, pwd)
	r.mu.Lock()
	existing, ok := r.managed[req.String()]
	r.mu.Unlock()
	if ok && existing.specHash == hash {
		return ctrl.Result{}, nil
	}
	r.stopWorker(req.String())
	if err := r.startWorker(&dev, pwd, hash); err != nil {
		if r.Recorder != nil {
			r.Recorder.Eventf(&dev, corev1.EventTypeWarning, "AggregatorWorkerFailed",
				"could not start in-process reconciler: %v", err)
		}
		return ctrl.Result{}, fmt.Errorf("start worker %s: %w", req.String(), err)
	}
	return ctrl.Result{}, nil
}

// startWorker asks the platform registry for a
// `drivers.ConfigDriverContext` matching dev.Spec.Driver, then
// spins a `provider.ConfigReconciler.Run` goroutine bound to a
// per-device context derived from rootCtx.
func (r *AggregatedReconciler) startWorker(dev *ciskov1.CiscoDevice, password, hash string) error {
	dctx, err := drivers.NewConfigDriver(r.rootCtx, &dev.Spec, password, drivers.ConfigDriverOptions{})
	if err != nil && (dctx == nil || dctx.Transport == nil) {
		return fmt.Errorf("config driver context: %w", err)
	}
	if dctx == nil {
		return fmt.Errorf("config driver context: returned nil for kind %q", dev.Spec.Driver)
	}

	leaseNs := r.LeaseNamespace
	if leaseNs == "" {
		leaseNs = dev.Namespace
	}
	leaser := &engine.FamilyLeaser{Client: r.Client, Namespace: leaseNs}

	notify := startSubscribeWatcher(r.rootCtx, dctx)

	devCtx, cancel := context.WithCancel(r.rootCtx)
	rec := &provider.ConfigReconciler{
		Client:                r.Client,
		DeviceName:            dev.Name,
		Transport:             dctx.Transport,
		KeyRules:              dctx.KeyRules,
		SupportedYANGVersions: dctx.SupportedYANGVersions,
		DefaultYANGVersion:    dctx.DefaultYANGVersion,
		Lookup:                dctx.LookupWriter,
		Leaser:                leaser,
		Recorder:              r.Recorder,
		SubscribeNotify:       notify,
	}

	r.mu.Lock()
	r.managed[devKey(dev)] = &deviceWorker{
		cancel:   cancel,
		specHash: hash,
	}
	r.mu.Unlock()

	transport := dctx.Transport
	go func() {
		defer func() {
			if transport != nil {
				_ = transport.Close()
			}
		}()
		if err := rec.Run(devCtx); err != nil && err != context.Canceled {
			if r.Recorder != nil {
				r.Recorder.Eventf(dev, corev1.EventTypeWarning, "AggregatorWorkerExit",
					"in-process reconciler exited: %v", err)
			}
		}
	}()
	return nil
}

func (r *AggregatedReconciler) stopWorker(key string) {
	r.mu.Lock()
	w, ok := r.managed[key]
	if ok {
		delete(r.managed, key)
	}
	r.mu.Unlock()
	if !ok {
		return
	}
	w.cancel()
}

// resolvePassword reads the CiscoDevice's credential — either an
// inline password (dev/test) or a CredentialSecretRef. Mirrors the
// per-pod path so a CR works identically under either topology.
func (r *AggregatedReconciler) resolvePassword(ctx context.Context, dev *ciskov1.CiscoDevice) (string, error) {
	if dev.Spec.Password != "" {
		return dev.Spec.Password, nil
	}
	if dev.Spec.CredentialSecretRef == nil || dev.Spec.CredentialSecretRef.Name == "" {
		return "", fmt.Errorf("no password and no credentialSecretRef set")
	}
	var sec corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: dev.Namespace,
		Name:      dev.Spec.CredentialSecretRef.Name,
	}, &sec); err != nil {
		return "", fmt.Errorf("get Secret %s/%s: %w", dev.Namespace, dev.Spec.CredentialSecretRef.Name, err)
	}
	pwd, ok := sec.Data["password"]
	if !ok {
		return "", fmt.Errorf("Secret %s/%s missing 'password' key", dev.Namespace, dev.Spec.CredentialSecretRef.Name)
	}
	return string(pwd), nil
}

func devKey(dev *ciskov1.CiscoDevice) string {
	return dev.Namespace + "/" + dev.Name
}

// specHash compresses the transport-relevant spec fields into a
// stable string. The aggregator restarts a worker only when this
// changes; non-transport edits (labels, taints, log level) pass
// through to the running ConfigReconciler unchanged.
func specHash(dev *ciskov1.CiscoDevice, password string) string {
	return fmt.Sprintf("%s|%s|%s|%d|%s|%t|%v",
		dev.Spec.Driver,
		dev.Spec.Address,
		dev.Spec.Username,
		dev.Spec.Port,
		dev.Spec.Transport,
		dev.Spec.TLS != nil && dev.Spec.TLS.Enabled,
		password != "",
	)
}

// startSubscribeWatcher wires the gNMI Subscribe drift watcher
// when the per-driver context advertises it. Platform-agnostic:
// the path set comes from the registered ConfigDriverContext, not
// from a writers package directly.
func startSubscribeWatcher(ctx context.Context, dctx *drivers.ConfigDriverContext) <-chan struct{} {
	if dctx == nil || dctx.Transport == nil {
		return nil
	}
	if !dctx.Transport.Capabilities().SupportsSubscribe {
		return nil
	}
	if len(dctx.SubscribePaths) == 0 {
		return nil
	}
	notify, err := provider.StartSubscribeWatcher(ctx, dctx.Transport, dctx.SubscribePaths, 0)
	if err != nil {
		return nil
	}
	return notify
}
