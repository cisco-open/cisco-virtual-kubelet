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
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider"
	log "github.com/virtual-kubelet/virtual-kubelet/log"
	vktrace "github.com/virtual-kubelet/virtual-kubelet/trace"
)

var deviceVersionRetryInterval = 30 * time.Second

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
// Wave 1D — when running in aggregator mode, the controller's own
// service account drives the in-process per-device ConfigReconciler.
// That reconciler reads the scope-resolution chain (defaults, device
// groups, interface groups, templates), reads/writes per-device
// IOSXEConfig CRs and their status subresource, appends to apply-log
// CRs, and acquires per-(device, family) coordination.k8s.io leases.
// All of these were previously absent from the controller ClusterRole
// (only the VK pod's ClusterRole carried them), so enabling the
// aggregator caused permission failures on realistic CR shapes.
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iseconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iseconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=fmcconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=fmcconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=apicconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=apicconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigdefaults,verbs=get;list;watch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxedevicegroupconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeinterfacegroupconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxetemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigapplylogs,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigapplylogs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigrevisions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigrevisions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *AggregatedReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	r.rootCtx = ctx
	if r.managed == nil {
		r.managed = map[string]*deviceWorker{}
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&ciskov1.CiscoDevice{}).
		// Wave 6B (external-review-followup Finding #5): watch
		// credential Secrets so a rotation re-enters Reconcile,
		// resolvePassword reads the new value, specHash changes
		// (passwordDigest now includes the SHA-256 of the
		// password), and the worker restarts.
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.mapSecretToCiscoDevices)).
		Complete(r)
}

// mapSecretToCiscoDevices fans a credential-Secret event out to
// every CiscoDevice in the same namespace whose
// spec.credentialSecretRef.name matches. The Reconcile callback
// then re-resolves the password and (if it changed) restarts the
// affected worker.
func (r *AggregatedReconciler) mapSecretToCiscoDevices(ctx context.Context, obj client.Object) []ctrl.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}
	var devices ciskov1.CiscoDeviceList
	if err := r.List(ctx, &devices, client.InNamespace(secret.Namespace)); err != nil {
		return nil
	}
	out := make([]ctrl.Request, 0)
	for i := range devices.Items {
		dev := &devices.Items[i]
		if dev.Spec.CredentialSecretRef == nil {
			continue
		}
		if dev.Spec.CredentialSecretRef.Name != secret.Name {
			continue
		}
		out = append(out, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: dev.Namespace,
				Name:      dev.Name,
			},
		})
	}
	return out
}

func (r *AggregatedReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	ctx, span := vktrace.StartSpan(ctx, "cvk.aggregated.reconcile")
	ctx = span.WithField(ctx, "cisco.device.name", req.Name)
	ctx = span.WithField(ctx, "cisco.device.namespace", req.Namespace)
	defer func() {
		span.WithField(ctx, "cvk.reconcile.result", aggregatedReconcileResultAttribute(result))
		if retErr != nil {
			span.SetStatus(retErr)
		}
		span.End()
	}()

	var dev ciskov1.CiscoDevice
	if err := r.Get(ctx, req.NamespacedName, &dev); err != nil {
		if apierrors.IsNotFound(err) {
			r.stopWorker(req.String())
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	ctx = span.WithField(ctx, "cvk.driver.kind", string(dev.Spec.Driver))

	// Platforms without a registered config driver: silent skip.
	// Operators see the device through the existing per-pod flow
	// (or no flow at all if the platform isn't registered for
	// apphosting either).
	if !drivers.ConfigDriverRegistered(dev.Spec.Driver) {
		r.stopWorker(req.String())
		return ctrl.Result{}, nil
	}
	owning := meta.FindStatusCondition(dev.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorOwning)
	if owning != nil && owning.Status == metav1.ConditionTrue {
		r.stopWorker(req.String())
		return ctrl.Result{}, nil
	}
	owned := meta.FindStatusCondition(dev.Status.Conditions, ciskov1.CiscoDeviceConditionAggregatorOwned)
	if owned == nil || owned.Status != metav1.ConditionTrue {
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

func aggregatedReconcileResultAttribute(result ctrl.Result) string {
	if result.RequeueAfter > 0 {
		return "requeue-after:" + result.RequeueAfter.String()
	}
	if result.Requeue {
		return "requeue"
	}
	return "done"
}

// startWorker asks the platform registry for a
// `drivers.ConfigDriverContext` matching dev.Spec.Driver, then
// spins a `provider.ConfigReconciler.Run` goroutine bound to a
// per-device context derived from rootCtx.
func (r *AggregatedReconciler) startWorker(dev *ciskov1.CiscoDevice, password, hash string) error {
	if dev.Spec.Driver == ciskov1.DeviceDriverISE {
		return r.startISEWorker(dev, password, hash)
	}
	if dev.Spec.Driver == ciskov1.DeviceDriverFMC {
		return r.startFMCWorker(dev, password, hash)
	}
	if dev.Spec.Driver == ciskov1.DeviceDriverACI {
		return r.startAPICWorker(dev, password, hash)
	}
	dctx, err := drivers.NewConfigDriver(r.rootCtx, &dev.Spec, password, drivers.ConfigDriverOptions{})
	if err != nil && (dctx == nil || dctx.Transport == nil) {
		return fmt.Errorf("config driver context: %w", err)
	}
	if dctx == nil {
		return fmt.Errorf("config driver context: returned nil for kind %q", dev.Spec.Driver)
	}

	// Validate the device version before spawning a reconciler
	// goroutine. Unsupported or malformed versions fail closed so
	// this device stays Pending and no IOSXEConfig write path can run.
	if verr := dctx.ValidateDeviceVersion(); verr != nil {
		reason := "AggregatorMalformedDeviceVersion"
		msg := "device version %q failed to parse: %v"
		if drivers.IsUnsupportedDeviceVersionError(verr) {
			reason = "AggregatorUnsupportedDeviceVersion"
			msg = "device version %q is not in the supported release set: %v"
		}
		if r.Recorder != nil {
			r.Recorder.Eventf(dev, corev1.EventTypeWarning, reason, msg, dctx.DeviceVersion, verr)
		}
		return fmt.Errorf("%s: %w", reason, verr)
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
		DeviceVersion:         dctx.DeviceVersion,
		FetchDeviceVersion:    dctx.FetchDeviceVersion,
		RequireDeviceVersion:  true,
		KeyRules:              dctx.KeyRules,
		SupportedYANGVersions: dctx.SupportedYANGVersions,
		DefaultYANGVersion:    dctx.DefaultYANGVersion,
		Lookup:                dctx.LookupWriter,
		FamilyOrder:           dctx.FamilyOrder,
		YANGValidator:         dctx.YANGValidator,
		YANGValidationMode:    dctx.YANGValidationMode,
		Leaser:                leaser,
		Recorder:              r.Recorder,
		SubscribeNotify:       notify,
		// Wave 7A.3 — unique runtime identity per worker start.
		// Two aggregator workers reconciling the same CR (manager
		// restart, sequenced rollout) get distinct lease holders
		// and cannot both renew the same lease.
		RuntimeID: newWorkerRuntimeID(),
	}

	r.mu.Lock()
	r.managed[devKey(dev)] = &deviceWorker{
		cancel:   cancel,
		specHash: hash,
	}
	r.mu.Unlock()

	transport := dctx.Transport
	if dctx.Transport != nil && dctx.DeviceVersion == "" && dctx.FetchDeviceVersion != nil {
		go retryDeviceVersion(devCtx, rec, dctx)
	}
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

func retryDeviceVersion(ctx context.Context, r *provider.ConfigReconciler, dctx *drivers.ConfigDriverContext) {
	if dctx == nil || dctx.Transport == nil || dctx.FetchDeviceVersion == nil {
		return
	}
	tick := time.NewTicker(deviceVersionRetryInterval)
	defer tick.Stop()
	for attempt := 1; ; attempt++ {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		ver := dctx.FetchDeviceVersion(ctx, dctx.Transport)
		if ver == "" {
			log.G(ctx).
				WithField("attempt", attempt).
				Info("aggregator device version still unavailable; config writes remain blocked")
			continue
		}
		dctx.DeviceVersion = ver
		if err := dctx.ValidateDeviceVersion(); err != nil {
			r.SetDeviceVersionState(ver, err)
			log.G(ctx).WithError(err).WithField("version", ver).
				Warn("aggregator device version retry produced rejected version; config writes remain blocked")
			if drivers.IsRetryableDeviceVersionError(err) {
				continue
			}
			return
		}
		r.SetDeviceVersionState(ver, nil)
		log.G(ctx).WithField("version", ver).
			Info("aggregator device version bound to writers after retry")
		return
	}
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
//
// Wave 6B (external-review-followup Finding #5): the password
// itself is folded into the hash via a SHA-256 digest so a
// credentialSecretRef rotation actually changes the hash and
// triggers a worker restart. Pre-fix the hash recorded only
// "password is non-empty" (a bool), so a password change went
// undetected and the worker kept using the stale credential
// indefinitely. Hashing rather than embedding the raw password
// keeps the secret out of the in-memory deviceWorker struct's
// observable state (tests, logs, debug dumps).
func specHash(dev *ciskov1.CiscoDevice, password string) string {
	return fmt.Sprintf("%s|%s|%s|%d|%s|%t|%s",
		dev.Spec.Driver,
		dev.Spec.Address,
		dev.Spec.Username,
		dev.Spec.Port,
		dev.Spec.Transport,
		dev.Spec.TLS != nil && dev.Spec.TLS.Enabled,
		passwordDigest(password),
	)
}

// newWorkerRuntimeID returns a fresh hex-encoded random identifier
// for an aggregator worker instance. Used as the RuntimeID suffix
// in the worker's lease holder string so two workers reconciling
// the same CR (manager restart with overlap, sequenced rollout)
// cannot both renew the same lease. 16 bytes of crypto-rand entropy
// is plenty — collisions across the lifetime of a fleet are
// astronomically unlikely.
//
// Wave 7A.3 — paired with the per-pod POD_UID injection so both
// topologies build process-unique lease holders.
func newWorkerRuntimeID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should never fail on supported platforms;
		// the time-based fallback is just defence in depth so a
		// degenerate environment doesn't produce duplicate IDs.
		return fmt.Sprintf("worker-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// passwordDigest returns a stable, non-reversible identifier of a
// password value. Used by specHash so credential rotation produces
// a different hash without persisting the cleartext anywhere on
// the aggregator's state.
func passwordDigest(password string) string {
	if password == "" {
		return "empty"
	}
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
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
