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

// Package aggregator hosts the single-manager topology option
// (Phase 7): a controller-runtime reconciler that watches
// CiscoDevices and runs an in-process ConfigReconciler per device,
// instead of spawning one cisco-vk pod per device. Two trade-offs:
//
//   - Pro: one process means one /metrics, one log stream, one
//     pull on cluster resources. Better operational ergonomics
//     for fleets in the hundreds.
//   - Con: blast radius — if the aggregator pod crashes, every
//     device's reconcile pauses until restart. The per-pod
//     topology distributes that.
//
// Operators choose via the Helm `aggregator.enabled` value. The
// per-pod topology stays the default; this package is the opt-in
// path.
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

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/intent"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider"
)

// AggregatedReconciler watches CiscoDevices and runs one in-process
// ConfigReconciler per matched device. It owns the device→reconciler
// registry and the per-device goroutine lifecycle.
type AggregatedReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	// LeaseNamespace is the shared namespace family leases land
	// in. Empty falls back to each device's own namespace —
	// matches the per-pod default (CONFIG_LEASE_NAMESPACE unset).
	LeaseNamespace string

	// SupportedYANGVersions / DefaultYANGVersion are wired from
	// schema/yang-versions.yaml at startup.
	SupportedYANGVersions map[string]struct{}
	DefaultYANGVersion    string

	// KeyRules is the per-family path → key map. Same value the
	// per-pod topology uses; loaded once and shared.
	KeyRules intent.KeyRules

	mu       sync.Mutex
	managed  map[string]*deviceWorker // key: namespace/name
	rootCtx  context.Context
}

// deviceWorker owns one device's reconciler goroutine. Cancel
// closes the context bound to the goroutine; the reconciler exits
// cleanly and any in-flight RPCs honour the cancel.
type deviceWorker struct {
	cancel    context.CancelFunc
	transport transport.Interface
	specHash  string // detect spec edits that need a transport rebuild
}

// +kubebuilder:rbac:groups=cisco.vk,resources=ciscodevices,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// SetupWithManager wires the aggregator. The root context (used to
// derive per-device contexts) is captured here so a manager
// shutdown propagates to every per-device worker.
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

	// IOS-XE only — every other driver type stays per-pod for
	// now. The aggregator's scope is deliberately narrow.
	if dev.Spec.Driver != ciskov1.DeviceDriverXE {
		r.stopWorker(req.String())
		return ctrl.Result{}, nil
	}

	pwd, err := r.resolvePassword(ctx, &dev)
	if err != nil {
		// Without a credential we can't dial the device. Log via
		// event and back off — a Secret update will trigger a
		// fresh reconcile via the watched Secret informer (a
		// Phase-7.5 nicety; for now we wait for the next
		// CiscoDevice event).
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
		// No-op: worker already running with this exact
		// transport-relevant spec. The per-device reconciler
		// runs its own loop independent of CiscoDevice events.
		return ctrl.Result{}, nil
	}
	// Spec changed (or no worker yet) — rebuild.
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

// startWorker builds the device's Transport and launches the
// ConfigReconciler.Run goroutine. The goroutine closes its own
// transport on exit.
func (r *AggregatedReconciler) startWorker(dev *ciskov1.CiscoDevice, password, hash string) error {
	t, err := transport.For(&dev.Spec, password, transport.FactoryOptions{})
	if err != nil {
		return fmt.Errorf("build transport: %w", err)
	}
	leaseNs := r.LeaseNamespace
	if leaseNs == "" {
		leaseNs = dev.Namespace
	}
	leaser := &engine.FamilyLeaser{Client: r.Client, Namespace: leaseNs}

	notify := startSubscribeFor(r.rootCtx, t)

	devCtx, cancel := context.WithCancel(r.rootCtx)
	rec := &provider.ConfigReconciler{
		Client:                r.Client,
		DeviceName:            dev.Name,
		Transport:             t,
		KeyRules:              r.KeyRules,
		SupportedYANGVersions: r.SupportedYANGVersions,
		DefaultYANGVersion:    r.DefaultYANGVersion,
		Leaser:                leaser,
		Recorder:              r.Recorder,
		SubscribeNotify:       notify,
	}

	r.mu.Lock()
	r.managed[devKey(dev)] = &deviceWorker{
		cancel:    cancel,
		transport: t,
		specHash:  hash,
	}
	r.mu.Unlock()

	go func() {
		defer func() {
			if t != nil {
				_ = t.Close()
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
// inline password (dev/test) or a SecretKeyRef. Mirrors the
// per-pod path's behaviour so a CR works identically under either
// topology.
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

// devKey is the registry key for a CiscoDevice. Namespace-scoped
// so two devices with the same name in different namespaces are
// distinct workers.
func devKey(dev *ciskov1.CiscoDevice) string {
	return dev.Namespace + "/" + dev.Name
}

// specHash compresses the transport-relevant spec fields into a
// stable string. The aggregator restarts a worker only when this
// changes — every other spec edit (labels, taints, log level)
// passes through to the running ConfigReconciler unchanged.
func specHash(dev *ciskov1.CiscoDevice, password string) string {
	return fmt.Sprintf("%s|%s|%d|%s|%t|%v",
		dev.Spec.Address,
		dev.Spec.Username,
		dev.Spec.Port,
		dev.Spec.Transport,
		dev.Spec.TLS != nil && dev.Spec.TLS.Enabled,
		password != "",
	)
}

// startSubscribeFor wires the gNMI Subscribe drift watcher when
// the transport supports it; returns nil otherwise so the
// reconciler stays on its periodic ticker.
func startSubscribeFor(ctx context.Context, t transport.Interface) <-chan struct{} {
	if t == nil || !t.Capabilities().SupportsSubscribe {
		return nil
	}
	paths := unionWriterPaths()
	if len(paths) == 0 {
		return nil
	}
	notify, err := provider.StartSubscribeWatcher(ctx, t, paths, 0)
	if err != nil {
		return nil
	}
	return notify
}

func unionWriterPaths() []string {
	seen := map[string]struct{}{}
	for _, fam := range writers.Families() {
		w := writers.Get(fam)
		if w == nil {
			continue
		}
		for _, p := range w.YANGPaths() {
			seen[p] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	return out
}

// scheme imports just to make goimports happy when this file is
// added in isolation. The aggregator participates in the controller
// manager's shared scheme; references here keep the imports honest.
var _ configv1alpha1.IOSXEConfigList // ensure the API types compile in
