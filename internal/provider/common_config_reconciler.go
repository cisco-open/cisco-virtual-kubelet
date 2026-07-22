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

package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/yaml"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/intent"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/validation"
	enginewriters "github.com/cisco/virtual-kubelet-cisco/internal/configengine/writers"
)

// CommonConfigPlatform describes one CRD that uses CommonConfigSpec and
// CommonConfigStatus. It is intentionally small: platform-specific transport
// and writer mechanics still come from drivers.ConfigDriverContext.
type CommonConfigPlatform struct {
	Name              string
	Kind              string
	ControllerName    string
	SourceEnvelope    string
	ModelFormat       configv1alpha1.NetAsCodeModelFormat
	SupportedFamilies []string
	Finalizer         string
	PreserveEnvelope  bool
	SupportsRevisions bool
	SupportsRollback  bool
	// ValidateModelSource performs platform-specific model contract checks
	// before any source is loaded or device state is fetched. Nil preserves the
	// legacy metadata-only behavior.
	ValidateModelSource     func(*configv1alpha1.NetAsCodeModelSource) error
	ValidateModelDevicePair func(*configv1alpha1.NetAsCodeModelSource, string) error
	ValidateTargetVersion   func(targetVersion, deviceVersion string) error
	ValidateResolvedSource  func(config map[string]any, deviceName string) error
	NormalizeSource         func(config map[string]any, deviceName string) (map[string]any, error)

	NewObject func() client.Object
	NewList   func() client.ObjectList
	Items     func(client.ObjectList) []client.Object
	Spec      func(client.Object) *configv1alpha1.CommonConfigSpec
	Status    func(client.Object) *configv1alpha1.CommonConfigStatus
}

func (p CommonConfigPlatform) validate() error {
	switch {
	case p.Name == "":
		return fmt.Errorf("common config platform: empty name")
	case p.Kind == "":
		return fmt.Errorf("common config platform %s: empty kind", p.Name)
	case p.ControllerName == "":
		return fmt.Errorf("common config platform %s: empty controller name", p.Name)
	case p.SourceEnvelope == "":
		return fmt.Errorf("common config platform %s: empty source envelope", p.Name)
	case p.Finalizer == "":
		return fmt.Errorf("common config platform %s: empty finalizer", p.Name)
	case p.NewObject == nil || p.NewList == nil || p.Items == nil || p.Spec == nil || p.Status == nil:
		return fmt.Errorf("common config platform %s: incomplete object adapter", p.Name)
	default:
		return nil
	}
}

// CommonConfigReconciler is the reusable per-device reconciler for platform
// CRDs that embed CommonConfigSpec/CommonConfigStatus.
type CommonConfigReconciler struct {
	Client     client.Client
	DeviceName string
	// DeviceNamespace is the namespace of the CiscoDevice this reconciler
	// serves. CR.spec.deviceRef is same-namespace by contract (DeviceRef
	// carries no namespace), so the reconciler must only ever list and act
	// on CRs in this namespace — otherwise a per-device worker for
	// tenant-a/leaf-01 could reconcile tenant-b/leaf-01's config against
	// tenant-a's device. Empty disables the filter (unit tests / legacy
	// single-namespace topologies).
	DeviceNamespace         string
	Transport               transport.Interface
	Lookup                  func(family, release string) enginewriters.SectionWriter
	FamilyOrder             func([]string) []string
	DeviceVersion           string
	DefaultYANGVersion      string
	SupportedYANGVersions   map[string]struct{}
	FetchDeviceVersion      func(context.Context, transport.Interface) string
	ValidateDeviceVersion   enginewriters.VersionValidator
	IsUnsupportedVersion    enginewriters.VersionErrorClassifier
	ReleaseTagForVersion    func(string) (string, bool)
	RequireDeviceVersion    bool
	OperationValidator      validation.Validator
	OperationValidationMode validation.Mode
	Leaser                  *engine.FamilyLeaser
	Recorder                record.EventRecorder
	Interval                time.Duration
	RuntimeID               string
	Platform                CommonConfigPlatform

	// SubscribeNotify is the polling-loop fast path for transports that
	// support device-side change streams. Nil means polling-only.
	SubscribeNotify <-chan struct{}

	// SubscribeEvents is the controller-runtime equivalent of
	// SubscribeNotify. Each event requeues every CR for this platform that
	// targets DeviceName.
	SubscribeEvents <-chan event.GenericEvent

	// SubscribeNotifyTime lets thin platform facades own the timestamp while
	// constructing fresh CommonConfigReconciler values per method call.
	SubscribeNotifyTime *atomic.Int64

	transportSlot       atomic.Pointer[transport.Interface]
	subscribeNotifyTime atomic.Int64
	versionMu           sync.RWMutex
}

func (r *CommonConfigReconciler) SetTransport(t transport.Interface) {
	if t == nil {
		r.transportSlot.Store(nil)
		return
	}
	r.transportSlot.Store(&t)
}

func (r *CommonConfigReconciler) GetTransport() transport.Interface {
	if p := r.transportSlot.Load(); p != nil {
		return *p
	}
	return r.Transport
}

// SetDeviceVersion / SetDefaultYANGVersion are called after a deferred
// transport dial succeeds. They must be concurrency-safe against the
// reconcile loop reading the version during resolveIntent.
func (r *CommonConfigReconciler) SetDeviceVersion(v string) {
	r.versionMu.Lock()
	defer r.versionMu.Unlock()
	r.DeviceVersion = v
}

// refreshDeviceVersion mirrors the IOS XE version-aware reconcile boundary:
// re-read the live version before a device-facing tick and rebind the default
// release profile when the platform recognizes it. A platform that requires a
// live version fails closed on an empty/error response instead of continuing
// to write through a stale binding after an unobserved software upgrade.
func (r *CommonConfigReconciler) refreshDeviceVersion(ctx context.Context) {
	if r == nil || r.FetchDeviceVersion == nil || r.GetTransport() == nil {
		return
	}
	version := r.FetchDeviceVersion(ctx, r.GetTransport())
	if version == "" {
		if r.RequireDeviceVersion {
			r.SetDeviceVersion("")
			r.SetDefaultYANGVersion("")
		}
		return
	}
	if version == r.deviceVersion() {
		return
	}
	r.SetDeviceVersion(version)
	if r.ReleaseTagForVersion != nil {
		if tag, ok := r.ReleaseTagForVersion(version); ok {
			if len(r.SupportedYANGVersions) == 0 {
				r.SetDefaultYANGVersion(tag)
			} else if _, supported := r.SupportedYANGVersions[tag]; supported {
				r.SetDefaultYANGVersion(tag)
			}
		}
	}
}

func (r *CommonConfigReconciler) deviceVersion() string {
	r.versionMu.RLock()
	defer r.versionMu.RUnlock()
	return r.DeviceVersion
}

func (r *CommonConfigReconciler) SetDefaultYANGVersion(v string) {
	r.versionMu.Lock()
	defer r.versionMu.Unlock()
	r.DefaultYANGVersion = v
}

func (r *CommonConfigReconciler) defaultYANGVersion() string {
	r.versionMu.RLock()
	defer r.versionMu.RUnlock()
	return r.DefaultYANGVersion
}

// inDeviceNamespace reports whether obj lives in this reconciler's device
// namespace. Empty DeviceNamespace disables the check (unit tests / legacy
// single-namespace topologies) and behaves as before.
func (r *CommonConfigReconciler) inDeviceNamespace(obj interface{ GetNamespace() string }) bool {
	return r.DeviceNamespace == "" || obj.GetNamespace() == r.DeviceNamespace
}

func (r *CommonConfigReconciler) notifyClock() *atomic.Int64 {
	if r.SubscribeNotifyTime != nil {
		return r.SubscribeNotifyTime
	}
	return &r.subscribeNotifyTime
}

func (r *CommonConfigReconciler) NotifySubscribeFired() {
	r.notifyClock().Store(time.Now().UnixNano())
}

func (r *CommonConfigReconciler) subscribeFiredSince(lastDeviceCheck *metav1.Time) bool {
	if lastDeviceCheck == nil {
		return false
	}
	notifyT := r.notifyClock().Load()
	if notifyT == 0 {
		return false
	}
	return notifyT > lastDeviceCheck.UnixNano()
}

func (r *CommonConfigReconciler) Run(ctx context.Context) error {
	if err := r.validate(); err != nil {
		return err
	}
	interval := r.Interval
	if interval <= 0 {
		interval = defaultConfigReconcileInterval
	}
	logger := log.G(ctx).
		WithField("component", strings.ToLower(r.Platform.Kind)+"-reconciler").
		WithField("device", r.DeviceName)
	logger.WithField("interval", interval).Info("starting common config reconcile loop")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var inflight sync.WaitGroup
	runTick := func(trigger reconcileTrigger) {
		inflight.Add(1)
		defer inflight.Done()
		r.reconcileAll(ctx, logger, trigger)
	}
	runTick(triggerPoll)
	notify := r.SubscribeNotify
	for {
		select {
		case <-ctx.Done():
			done := make(chan struct{})
			go func() {
				inflight.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(defaultGracefulShutdownTimeout):
				logger.Warn("drain budget exceeded; in-flight common config tick may abort")
			}
			return ctx.Err()
		case <-ticker.C:
			runTick(triggerPoll)
		case _, ok := <-notify:
			if !ok {
				notify = nil
				continue
			}
			logger.Debug("subscribe-notify fired; running off-cycle common config reconcile")
			r.NotifySubscribeFired()
			runTick(triggerSubscribe)
		}
	}
}

func (r *CommonConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := r.validate(); err != nil {
		return err
	}
	devicePredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool { return r.targetsDevice(e.Object) },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return r.targetsDevice(e.ObjectNew) || r.targetsDevice(e.ObjectOld)
		},
		DeleteFunc:  func(e event.DeleteEvent) bool { return r.targetsDevice(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return r.targetsDevice(e.Object) },
	}
	mapAll := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		list := r.Platform.NewList()
		if err := r.Client.List(ctx, list, client.InNamespace(r.DeviceNamespace)); err != nil {
			crlog.FromContext(ctx).Error(err, "mapAll list "+r.Platform.Kind)
			return nil
		}
		items := r.Platform.Items(list)
		out := make([]reconcile.Request, 0, len(items))
		for _, item := range items {
			spec := r.Platform.Spec(item)
			if spec == nil || spec.DeviceRef.Name != r.DeviceName || !r.inDeviceNamespace(item) {
				continue
			}
			out = append(out, reconcile.Request{
				NamespacedName: client.ObjectKey{Namespace: item.GetNamespace(), Name: item.GetName()},
			})
		}
		return out
	})
	b := ctrl.NewControllerManagedBy(mgr).
		Named(r.Platform.ControllerName+"-"+r.DeviceName).
		For(r.Platform.NewObject(), builder.WithPredicates(devicePredicate)).
		Watches(&corev1.ConfigMap{}, mapAll).
		Watches(&corev1.Secret{}, mapAll)
	if r.SubscribeEvents != nil {
		b = b.WatchesRawSource(source.Channel(r.SubscribeEvents,
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []reconcile.Request {
				r.NotifySubscribeFired()
				list := r.Platform.NewList()
				if err := r.Client.List(ctx, list, client.InNamespace(r.DeviceNamespace)); err != nil {
					crlog.FromContext(ctx).Error(err, "subscribe map list "+r.Platform.Kind)
					return nil
				}
				items := r.Platform.Items(list)
				out := make([]reconcile.Request, 0, len(items))
				for _, item := range items {
					spec := r.Platform.Spec(item)
					if spec == nil || spec.DeviceRef.Name != r.DeviceName || !r.inDeviceNamespace(item) {
						continue
					}
					out = append(out, reconcile.Request{
						NamespacedName: client.ObjectKey{Namespace: item.GetNamespace(), Name: item.GetName()},
					})
				}
				return out
			})))
	}
	return b.Complete(r)
}

func (r *CommonConfigReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	if err := r.validate(); err != nil {
		return reconcile.Result{}, err
	}
	logger := crlog.FromContext(ctx).WithValues("component", r.Platform.ControllerName, "device", r.DeviceName)
	cr := r.Platform.NewObject()
	if err := r.Client.Get(ctx, req.NamespacedName, cr); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("get %s: %w", r.Platform.Kind, err)
	}
	spec := r.Platform.Spec(cr)
	if spec == nil || spec.DeviceRef.Name != r.DeviceName || !r.inDeviceNamespace(cr) {
		return reconcile.Result{}, nil
	}
	proceed, result, err := r.prepareForReconcile(ctx, cr)
	if err != nil || !proceed {
		return result, err
	}
	r.refreshDeviceVersion(ctx)

	_, conflicts := r.cohort(ctx, logger)
	trigger := triggerEvent
	if status := r.Platform.Status(cr); status != nil && r.subscribeFiredSince(status.LastDeviceCheck) {
		trigger = triggerSubscribe
	}
	engineResult, err := r.reconcileOne(ctx, log.G(ctx), cr, conflicts, trigger)
	if err != nil {
		logger.Error(err, "reconcile "+r.Platform.Kind)
		return reconcile.Result{}, err
	}
	return reconcile.Result{RequeueAfter: r.requeueInterval(cr, engineResult.Phase)}, nil
}

func (r *CommonConfigReconciler) prepareForReconcile(ctx context.Context, cr client.Object) (bool, reconcile.Result, error) {
	if !cr.GetDeletionTimestamp().IsZero() {
		if containsFinalizer(cr.GetFinalizers(), r.Platform.Finalizer) {
			// Prune device-side owned keys before lease release +
			// finalizer removal. Without this, a deleted CR that owns
			// atomic-replace keys (e.g. a VLAN) orphans them on the
			// device. Relinquish failure is retryable: return the error
			// so controller-runtime requeues with the finalizer intact
			// and status.atomicReplaceOwnedKeys preserved for the retry.
			// Operators who accept the orphan set
			// config.cisco.vk/force-relinquish-skip=true.
			if spec := r.Platform.Spec(cr); spec != nil && spec.PruneOnRelinquish {
				if status := r.Platform.Status(cr); status != nil && len(status.AtomicReplaceOwnedKeys) > 0 {
					if cr.GetAnnotations()[ForceRelinquishSkipAnnotation] == "true" {
						if r.Recorder != nil {
							r.Recorder.Eventf(cr, corev1.EventTypeWarning, "RelinquishSkipped",
								"force-relinquish-skip annotation set; orphaning %v on device %q",
								status.AtomicReplaceOwnedKeys, r.DeviceName)
						}
					} else if err := r.relinquishOwnedKeys(ctx, cr); err != nil {
						if r.Recorder != nil {
							r.Recorder.Eventf(cr, corev1.EventTypeWarning, "RelinquishBlocked",
								"deletion blocked while pruneOnRelinquish cleanup retries: %v "+
									"(set annotation %s=true to give up)",
								err, ForceRelinquishSkipAnnotation)
						}
						return false, reconcile.Result{}, fmt.Errorf("relinquish: %w", err)
					}
				}
			}
			if err := r.releaseLeasesForObject(ctx, cr); err != nil {
				return false, reconcile.Result{}, fmt.Errorf("release %s leases: %w", r.Platform.Kind, err)
			}
			cr.SetFinalizers(removeFinalizer(cr.GetFinalizers(), r.Platform.Finalizer))
			if err := r.Client.Update(ctx, cr); err != nil && !apierrors.IsConflict(err) && !apierrors.IsNotFound(err) {
				return false, reconcile.Result{}, fmt.Errorf("remove %s finalizer: %w", r.Platform.Kind, err)
			}
		}
		return false, reconcile.Result{}, nil
	}
	if r.Leaser != nil && !containsFinalizer(cr.GetFinalizers(), r.Platform.Finalizer) {
		updated := cr.DeepCopyObject().(client.Object)
		updated.SetFinalizers(append(updated.GetFinalizers(), r.Platform.Finalizer))
		if err := r.Client.Update(ctx, updated); err != nil {
			// Fail closed: never mutate device state without a durable
			// finalizer, or a later delete bypasses relinquish/prune cleanup
			// and orphans owned device config. Conflict/NotFound are benign
			// (the object changed under us) — requeue and re-add next tick;
			// any other error (RBAC regression, API outage) is returned.
			if apierrors.IsConflict(err) || apierrors.IsNotFound(err) {
				return false, reconcile.Result{Requeue: true}, nil
			}
			return false, reconcile.Result{}, fmt.Errorf("add %s finalizer: %w", r.Platform.Kind, err)
		}
		cr.SetFinalizers(updated.GetFinalizers())
		cr.SetResourceVersion(updated.GetResourceVersion())
	}
	return true, reconcile.Result{}, nil
}

func (r *CommonConfigReconciler) reconcileAll(ctx context.Context, logger log.Logger, trigger reconcileTrigger) {
	r.refreshDeviceVersion(ctx)
	forDevice, conflicts := r.cohort(ctx, crlog.FromContext(ctx))
	for _, cr := range forDevice {
		proceed, _, err := r.prepareForReconcile(ctx, cr)
		if err != nil {
			logger.WithError(err).
				WithField("name", cr.GetName()).
				WithField("namespace", cr.GetNamespace()).
				Warn("common config lifecycle failed")
			continue
		}
		if !proceed {
			continue
		}
		if _, err := r.reconcileOne(ctx, logger, cr, conflicts, trigger); err != nil {
			logger.WithError(err).
				WithField("name", cr.GetName()).
				WithField("namespace", cr.GetNamespace()).
				Warn("reconcile common config failed")
		}
	}
}

func (r *CommonConfigReconciler) cohort(ctx context.Context, logger interface{ Error(error, string, ...any) }) ([]client.Object, map[string][]string) {
	list := r.Platform.NewList()
	if err := r.Client.List(ctx, list, client.InNamespace(r.DeviceNamespace)); err != nil {
		logger.Error(err, "list "+r.Platform.Kind)
		return nil, nil
	}
	all := r.Platform.Items(list)
	forDevice := make([]client.Object, 0, len(all))
	for _, cr := range all {
		spec := r.Platform.Spec(cr)
		if spec != nil && spec.DeviceRef.Name == r.DeviceName && r.inDeviceNamespace(cr) {
			forDevice = append(forDevice, cr)
		}
	}
	return forDevice, commonConflictCheck(r.Platform, r.DeviceName, forDevice)
}

func (r *CommonConfigReconciler) reconcileOne(
	ctx context.Context,
	logger log.Logger,
	cr client.Object,
	conflicts map[string][]string,
	trigger reconcileTrigger,
) (engine.Result, error) {
	if blocked, reason, msg := r.deviceVersionBlocked(); blocked {
		recordErr := r.recordDeviceVersionBlocked(ctx, cr, reason, msg)
		return engine.Result{Phase: engine.PhasePending, Err: recordErr}, recordErr
	}
	if err := r.validateFeatureIntent(cr); err != nil {
		recordErr := r.recordFailure(ctx, cr, err.Error())
		return engine.Result{Phase: engine.PhaseFailed, Err: err}, errors.Join(err, recordErr)
	}
	resolved, err := r.resolveIntent(ctx, cr)
	if err != nil {
		recordErr := r.recordFailure(ctx, cr, fmt.Sprintf("resolve: %v", err))
		return engine.Result{Phase: engine.PhaseFailed, Err: err}, errors.Join(err, recordErr)
	}
	hash, err := intent.CanonicalHash(resolved)
	if err != nil {
		recordErr := r.recordFailure(ctx, cr, fmt.Sprintf("hash: %v", err))
		return engine.Result{Phase: engine.PhaseFailed, Err: err}, errors.Join(err, recordErr)
	}
	status := r.Platform.Status(cr)
	if trigger != triggerSubscribe &&
		status != nil &&
		status.ObservedGeneration == cr.GetGeneration() &&
		status.LastAppliedHash == hash &&
		status.Phase == engine.PhaseInSync &&
		!r.dueForDriftCheck(cr) {
		return engine.Result{Phase: status.Phase}, nil
	}
	t := r.GetTransport()
	if t == nil {
		recordErr := r.recordPending(ctx, cr)
		return engine.Result{Phase: engine.PhasePending}, recordErr
	}
	leased, leaseConflicts := r.acquireLeases(ctx, resolved, cr)
	lookup := r.Lookup
	if lookup == nil {
		return engine.Result{Phase: engine.PhaseFailed, Err: fmt.Errorf("%s reconciler: nil writer lookup", r.Platform.Kind)}, nil
	}
	eng := &engine.Engine{
		Platform:           r.Platform.Name,
		Transport:          t,
		Lookup:             lookup,
		DeviceVersion:      r.deviceVersion(),
		FamilyOrder:        r.FamilyOrder,
		YANGValidator:      r.OperationValidator,
		YANGValidationMode: r.OperationValidationMode,
	}
	var result engine.Result
	if len(leaseConflicts) > 0 && len(leased.ManagedFamilies) == 0 {
		result = engine.Result{Phase: engine.PhaseLeaseBlocked}
	} else {
		result = eng.Reconcile(ctx, leased)
		if len(leaseConflicts) > 0 && result.Phase == engine.PhaseInSync {
			result.Phase = engine.PhaseLeaseBlocked
		}
	}
	for family, holder := range leaseConflicts {
		result.FamilyStatuses = append(result.FamilyStatuses, engine.FamilyStatus{
			Name: family, State: "Skipped", Message: fmt.Sprintf("family leased by %q", holder),
		})
	}
	recordErr := r.recordResult(ctx, cr, result, hash, conflicts)
	if recordErr != nil {
		return result, recordErr
	}
	if result.Phase == engine.PhaseFailed && result.Err != nil {
		logger.WithError(result.Err).Warn(r.Platform.Kind + " engine failed")
		return result, result.Err
	}
	return result, nil
}

func (r *CommonConfigReconciler) deviceVersionBlocked() (bool, string, string) {
	version := r.deviceVersion()
	if version == "" {
		if r.RequireDeviceVersion {
			return true, "DeviceVersionPending", "waiting for device software version before running config writers"
		}
		return false, "", ""
	}
	if r.ValidateDeviceVersion == nil {
		return false, "", ""
	}
	if err := r.ValidateDeviceVersion(version); err != nil {
		reason := "MalformedDeviceVersion"
		if r.IsUnsupportedVersion != nil && r.IsUnsupportedVersion(err) {
			reason = "UnsupportedDeviceVersion"
		}
		return true, reason, fmt.Sprintf("device version %q rejected by writers: %v", version, err)
	}
	return false, "", ""
}

func (r *CommonConfigReconciler) validateFeatureIntent(cr client.Object) error {
	spec := r.Platform.Spec(cr)
	if spec == nil {
		return fmt.Errorf("nil %s spec", r.Platform.Kind)
	}
	seenFamilies := make(map[string]struct{}, len(spec.ManagedFamilies))
	for i, family := range spec.ManagedFamilies {
		family = strings.TrimSpace(family)
		if family == "" {
			return fmt.Errorf("%s %s/%s: spec.managedFamilies[%d] must not be empty",
				r.Platform.Kind, cr.GetNamespace(), cr.GetName(), i)
		}
		if _, ok := seenFamilies[family]; ok {
			return fmt.Errorf("%s %s/%s: spec.managedFamilies contains duplicate family %q",
				r.Platform.Kind, cr.GetNamespace(), cr.GetName(), family)
		}
		seenFamilies[family] = struct{}{}
	}
	if strings.TrimSpace(spec.RollbackTo) != "" && !r.Platform.SupportsRollback {
		return fmt.Errorf("%s %s/%s: spec.rollbackTo is not supported by this platform runtime yet",
			r.Platform.Kind, cr.GetNamespace(), cr.GetName())
	}
	if spec.RevisionHistoryLimit != nil && *spec.RevisionHistoryLimit > 0 && !r.Platform.SupportsRevisions {
		return fmt.Errorf("%s %s/%s: spec.revisionHistoryLimit is not supported by this platform runtime yet",
			r.Platform.Kind, cr.GetNamespace(), cr.GetName())
	}
	if len(r.Platform.SupportedFamilies) > 0 {
		supported := make(map[string]struct{}, len(r.Platform.SupportedFamilies))
		for _, family := range r.Platform.SupportedFamilies {
			supported[family] = struct{}{}
		}
		unsupported := make([]string, 0)
		for _, family := range spec.ManagedFamilies {
			if _, ok := supported[family]; !ok {
				unsupported = append(unsupported, family)
			}
		}
		if len(unsupported) > 0 {
			sort.Strings(unsupported)
			return fmt.Errorf("%s %s/%s: spec.managedFamilies contains unsupported families %q for this platform runtime",
				r.Platform.Kind, cr.GetNamespace(), cr.GetName(), unsupported)
		}
	}
	return nil
}

func (r *CommonConfigReconciler) resolveIntent(ctx context.Context, cr client.Object) (*intent.ResolvedIntent, error) {
	spec := r.Platform.Spec(cr)
	if spec == nil {
		return nil, fmt.Errorf("nil %s spec", r.Platform.Kind)
	}
	status := r.Platform.Status(cr)
	var ownedKeys map[string][]string
	if status != nil {
		ownedKeys = copyOwnedKeys(status.AtomicReplaceOwnedKeys)
	}
	device := spec.DeviceRef.Name
	if device == "" {
		return nil, fmt.Errorf("spec.deviceRef.name is empty")
	}
	if spec.ModelSource != nil && spec.ModelSource.Format != "" &&
		r.Platform.ModelFormat != "" && spec.ModelSource.Format != r.Platform.ModelFormat {
		return nil, fmt.Errorf("modelSource.format %q does not match %s format %q",
			spec.ModelSource.Format, r.Platform.Kind, r.Platform.ModelFormat)
	}
	if r.Platform.ValidateModelSource != nil {
		if err := r.Platform.ValidateModelSource(spec.ModelSource); err != nil {
			return nil, fmt.Errorf("%s %s/%s: invalid spec.modelSource: %w",
				r.Platform.Kind, cr.GetNamespace(), cr.GetName(), err)
		}
	}
	if r.Platform.ValidateModelDevicePair != nil {
		if err := r.Platform.ValidateModelDevicePair(spec.ModelSource, r.deviceVersion()); err != nil {
			return nil, fmt.Errorf("%s %s/%s: model/device contract mismatch: %w",
				r.Platform.Kind, cr.GetNamespace(), cr.GetName(), err)
		}
	}
	if r.Platform.ValidateTargetVersion != nil {
		if err := r.Platform.ValidateTargetVersion(spec.TargetYangVersion, r.deviceVersion()); err != nil {
			return nil, fmt.Errorf("%s %s/%s: invalid spec.targetYangVersion: %w",
				r.Platform.Kind, cr.GetNamespace(), cr.GetName(), err)
		}
	}
	sourceEnvelope := r.Platform.SourceEnvelope
	if r.Platform.PreserveEnvelope {
		sourceEnvelope = ""
	}
	config, err := intent.LoadPlatformSource(ctx, r.Client, cr.GetNamespace(), device, sourceEnvelope, spec.Source)
	if err != nil {
		return nil, fmt.Errorf("%s %s/%s: %w", r.Platform.Kind, cr.GetNamespace(), cr.GetName(), err)
	}
	if spec.ModelSource != nil && spec.ModelSource.Resolved && r.Platform.ValidateResolvedSource != nil {
		if err := r.Platform.ValidateResolvedSource(config, device); err != nil {
			return nil, fmt.Errorf("%s %s/%s: modelSource.resolved payload is not flattened: %w",
				r.Platform.Kind, cr.GetNamespace(), cr.GetName(), err)
		}
	}
	if r.Platform.NormalizeSource != nil {
		config, err = r.Platform.NormalizeSource(config, device)
		if err != nil {
			return nil, fmt.Errorf("%s %s/%s: normalize source: %w",
				r.Platform.Kind, cr.GetNamespace(), cr.GetName(), err)
		}
	}
	managedFamilies := append([]string(nil), spec.ManagedFamilies...)
	managedSet := map[string]struct{}{}
	for _, fam := range managedFamilies {
		managedSet[fam] = struct{}{}
	}
	for i, sr := range spec.SecretRefs {
		if _, ok := managedSet[sr.Family]; !ok {
			return nil, fmt.Errorf("%s %s/%s: secretRefs[%d]: family %q not in managedFamilies",
				r.Platform.Kind, cr.GetNamespace(), cr.GetName(), i, sr.Family)
		}
		snippet, err := r.loadSecretSnippet(ctx, cr.GetNamespace(), sr)
		if err != nil {
			return nil, fmt.Errorf("%s %s/%s: secretRefs[%d]: %w",
				r.Platform.Kind, cr.GetNamespace(), cr.GetName(), i, err)
		}
		config = commonAsMap(intent.Merge(config, map[string]any{sr.Family: snippet}))
	}
	policy := spec.DriftPolicy
	if policy == "" {
		policy = configv1alpha1.DriftPolicyRevert
	}
	targetVersion := spec.TargetYangVersion
	if targetVersion != "" && len(r.SupportedYANGVersions) > 0 {
		if _, ok := r.SupportedYANGVersions[targetVersion]; !ok {
			return nil, fmt.Errorf("%s %s/%s: spec.targetYangVersion %q is not in the supported set",
				r.Platform.Kind, cr.GetNamespace(), cr.GetName(), targetVersion)
		}
	}
	if targetVersion == "" {
		targetVersion = r.defaultYANGVersion()
	}
	if targetVersion == "" {
		targetVersion = r.deviceVersion()
	}
	modelVersion := ""
	if spec.ModelSource != nil {
		modelVersion = spec.ModelSource.ModelVersion
	}
	intent.FixYAML11BoolKeys(config)
	return &intent.ResolvedIntent{
		DeviceName:             device,
		ManagedFamilies:        managedFamilies,
		Configuration:          config,
		ModelVersion:           modelVersion,
		Transactional:          spec.Transactional,
		DriftPolicy:            policy,
		WriteStartup:           spec.WriteStartup,
		PruneOnRelinquish:      spec.PruneOnRelinquish,
		TargetYangVersion:      targetVersion,
		ConfirmTimeoutSeconds:  spec.ConfirmTimeoutSeconds,
		AtomicReplace:          spec.AtomicReplace,
		AtomicReplaceOwnedKeys: ownedKeys,
	}, nil
}

func (r *CommonConfigReconciler) loadSecretSnippet(ctx context.Context, ns string, ref configv1alpha1.FamilySecretRef) (map[string]any, error) {
	var sec corev1.Secret
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: ns, Name: ref.Name}, &sec); err != nil {
		return nil, fmt.Errorf("get Secret %s/%s: %w", ns, ref.Name, err)
	}
	raw, ok := sec.Data[ref.Key]
	if !ok {
		return nil, fmt.Errorf("Secret %s/%s does not contain key %q", ns, ref.Name, ref.Key)
	}
	var decoded any
	if err := yaml.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("parse Secret %s/%s key %q: %w", ns, ref.Name, ref.Key, err)
	}
	m, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Secret %s/%s key %q must decode to a mapping, got %T", ns, ref.Name, ref.Key, decoded)
	}
	return m, nil
}

func (r *CommonConfigReconciler) acquireLeases(
	ctx context.Context,
	resolved *intent.ResolvedIntent,
	cr client.Object,
) (*intent.ResolvedIntent, map[string]string) {
	if r.Leaser == nil {
		return resolved, nil
	}
	identity := cr.GetNamespace() + "/" + cr.GetName()
	if r.RuntimeID != "" {
		identity += "#" + r.RuntimeID
	}
	owned := make([]string, 0, len(resolved.ManagedFamilies))
	conflicts := map[string]string{}
	for _, family := range resolved.ManagedFamilies {
		res, err := r.Leaser.Acquire(ctx, r.DeviceName, family, identity)
		if err != nil {
			conflicts[family] = fmt.Sprintf("lease error: %v", err)
			continue
		}
		if !res.Owned {
			conflicts[family] = stripRuntimeIDSuffix(res.Holder)
			continue
		}
		owned = append(owned, family)
	}
	filtered := *resolved
	filtered.ManagedFamilies = owned
	return &filtered, conflicts
}

func (r *CommonConfigReconciler) releaseLeasesForObject(ctx context.Context, cr client.Object) error {
	if r.Leaser == nil {
		return nil
	}
	spec := r.Platform.Spec(cr)
	if spec == nil {
		return nil
	}
	identity := cr.GetNamespace() + "/" + cr.GetName()
	if r.RuntimeID != "" {
		identity += "#" + r.RuntimeID
	}
	for _, fam := range spec.ManagedFamilies {
		if err := r.Leaser.Release(ctx, r.DeviceName, fam, identity); err != nil {
			return fmt.Errorf("release lease for %s: %w", fam, err)
		}
	}
	return nil
}

// relinquishOwnedKeys drops every list-key this CR owned (per
// status.atomicReplaceOwnedKeys) from the device before the finalizer is
// removed. Without it, deleting a CR that configured e.g. VLAN 4001 leaves
// 4001 stranded on the device with no Kubernetes object tracking it. This
// generalizes the IOS-XE relinquish path (config_reconciler_controller.go)
// onto the shared reconciler so NX-OS and any future platform get it too.
//
// Lease discipline mirrors IOS-XE: AcquireIfFree (never takeover, so a
// deleting CR can't prune a family whose holder is mid-write), and release
// every acquired family on success OR failure so a stuck terminating CR
// doesn't pin families for other CRs while it retries.
func (r *CommonConfigReconciler) relinquishOwnedKeys(ctx context.Context, cr client.Object) (returnedErr error) {
	spec := r.Platform.Spec(cr)
	status := r.Platform.Status(cr)
	if spec == nil || status == nil {
		return nil
	}
	t := r.GetTransport()
	if t == nil {
		return fmt.Errorf("relinquish: transport not yet available")
	}
	r.refreshDeviceVersion(ctx)
	if blocked, reason, msg := r.deviceVersionBlocked(); blocked {
		return fmt.Errorf("relinquish: %s: %s", reason, msg)
	}
	lookup := r.Lookup
	if lookup == nil {
		return fmt.Errorf("relinquish: nil writer lookup")
	}
	identity := cr.GetNamespace() + "/" + cr.GetName()
	if r.RuntimeID != "" {
		identity += "#" + r.RuntimeID
	}
	// Only families that actually own atomic-replace list-keys can be
	// relinquished. Map-shaped families (e.g. NX-OS "system") carry no owned
	// keys; pushing them an empty-list desired makes the writer Diff fail
	// ("want map, got []"). Restrict the prune set to families present in
	// status.atomicReplaceOwnedKeys.
	ownedKeys := status.AtomicReplaceOwnedKeys
	candidates := make([]string, 0, len(ownedKeys))
	for _, fam := range spec.ManagedFamilies {
		if len(ownedKeys[fam]) > 0 {
			candidates = append(candidates, fam)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	owned := make([]string, 0, len(candidates))
	leaseBlocked := make([]string, 0, len(candidates))
	if r.Leaser != nil {
		for _, fam := range candidates {
			res, err := r.Leaser.AcquireIfFree(ctx, r.DeviceName, fam, identity)
			if err != nil {
				r.releaseAcquiredFamilies(ctx, owned, identity)
				return fmt.Errorf("relinquish: acquire lease for %s: %w", fam, err)
			}
			if !res.Owned {
				leaseBlocked = append(leaseBlocked, fam)
				continue
			}
			owned = append(owned, fam)
		}
	} else {
		owned = append(owned, candidates...)
	}
	defer func() {
		if r.Leaser != nil {
			r.releaseAcquiredFamilies(ctx, owned, identity)
		}
	}()
	if len(owned) == 0 {
		if len(leaseBlocked) > 0 {
			return fmt.Errorf("relinquish: every managed family is held by another CR: %v; will retry", leaseBlocked)
		}
		return nil
	}
	// Empty desired for each owned family. The engine's prune path then
	// scopes deletion to {priorOwned ∪ desired} = {priorOwned}, so only
	// this CR's owned keys are removed; baseline device state is untouched.
	conf := make(map[string]any, len(owned))
	for _, fam := range owned {
		conf[fam] = []any{}
	}
	resIntent := &intent.ResolvedIntent{
		DeviceName:             spec.DeviceRef.Name,
		ManagedFamilies:        owned,
		Configuration:          conf,
		DriftPolicy:            configv1alpha1.DriftPolicyRevert,
		PruneOnRelinquish:      true,
		AtomicReplaceOwnedKeys: status.AtomicReplaceOwnedKeys,
	}
	if spec.ModelSource != nil {
		resIntent.ModelVersion = spec.ModelSource.ModelVersion
	}
	eng := &engine.Engine{
		Platform:           r.Platform.Name,
		Transport:          t,
		Lookup:             lookup,
		DeviceVersion:      r.deviceVersion(),
		FamilyOrder:        r.FamilyOrder,
		YANGValidator:      r.OperationValidator,
		YANGValidationMode: r.OperationValidationMode,
	}
	out := eng.Reconcile(ctx, resIntent)
	if out.Phase == engine.PhaseFailed {
		for _, fs := range out.FamilyStatuses {
			if fs.State == "ApplyError" || fs.State == "Unsupported" {
				return fmt.Errorf("relinquish reconcile: family %s (%s): %s", fs.Name, fs.State, fs.Message)
			}
		}
		return fmt.Errorf("relinquish reconcile failed: phase=%s", out.Phase)
	}
	if len(leaseBlocked) > 0 {
		return fmt.Errorf("relinquish: %d/%d managed families relinquished; "+
			"families still held by another CR: %v; will retry",
			len(owned), len(owned)+len(leaseBlocked), leaseBlocked)
	}
	return nil
}

// releaseAcquiredFamilies releases every lease in families held by identity.
// Best-effort: a per-family release error is logged, never returned.
func (r *CommonConfigReconciler) releaseAcquiredFamilies(ctx context.Context, families []string, identity string) {
	if r.Leaser == nil {
		return
	}
	logger := crlog.FromContext(ctx)
	for _, fam := range families {
		if err := r.Leaser.Release(ctx, r.DeviceName, fam, identity); err != nil {
			logger.Error(err, "release lease after relinquish attempt", "family", fam)
		}
	}
}

func (r *CommonConfigReconciler) recordPending(ctx context.Context, cr client.Object) error {
	updated := cr.DeepCopyObject().(client.Object)
	status := r.Platform.Status(updated)
	status.Phase = engine.PhasePending
	status.ObservedGeneration = cr.GetGeneration()
	setCommonCondition(status, metav1.Condition{
		Type: "Ready", Status: metav1.ConditionFalse, Reason: "NoTransport",
		Message: r.Platform.Kind + " config driver has no device transport configured",
	})
	if r.Recorder != nil {
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "NoTransport", "%s config transport is not available", r.Platform.Kind)
	}
	return ignoreConflict(r.Client.Status().Update(ctx, updated))
}

func (r *CommonConfigReconciler) recordDeviceVersionBlocked(ctx context.Context, cr client.Object, reason, msg string) error {
	updated := cr.DeepCopyObject().(client.Object)
	status := r.Platform.Status(updated)
	status.Phase = engine.PhasePending
	status.ObservedGeneration = cr.GetGeneration()
	setCommonCondition(status, metav1.Condition{
		Type: "Ready", Status: metav1.ConditionFalse, Reason: reason, Message: msg,
	})
	return ignoreConflict(r.Client.Status().Update(ctx, updated))
}

func (r *CommonConfigReconciler) recordFailure(ctx context.Context, cr client.Object, msg string) error {
	updated := cr.DeepCopyObject().(client.Object)
	status := r.Platform.Status(updated)
	status.Phase = engine.PhaseFailed
	status.ObservedGeneration = cr.GetGeneration()
	setCommonCondition(status, metav1.Condition{
		Type: "Ready", Status: metav1.ConditionFalse, Reason: "ReconcileFailed", Message: msg,
	})
	if r.Recorder != nil {
		r.Recorder.Eventf(cr, corev1.EventTypeWarning, "ReconcileFailed", "%s", msg)
	}
	return ignoreConflict(r.Client.Status().Update(ctx, updated))
}

func (r *CommonConfigReconciler) recordResult(
	ctx context.Context,
	cr client.Object,
	result engine.Result,
	hash string,
	conflicts map[string][]string,
) error {
	updated := cr.DeepCopyObject().(client.Object)
	status := r.Platform.Status(updated)
	status.Phase = result.Phase
	status.ObservedGeneration = cr.GetGeneration()
	now := metav1.Now()
	if result.DeviceTouched {
		status.LastDeviceCheck = &now
	}
	if result.Phase == engine.PhaseInSync {
		status.LastAppliedHash = hash
		status.LastAppliedTime = &now
		status.SourceYangVersion = result.YangVersion
	}
	status.PlannedOps = int32(result.PlannedOps)
	status.AppliedOps = int32(result.AppliedOps)
	status.PostApplyObservedHash = result.PostApplyObservedHash
	status.VerifiedFamilies = append(status.VerifiedFamilies[:0], result.VerifiedFamilies...)
	status.TransportFallbacks = status.TransportFallbacks[:0]
	if result.ConfirmedCommitFallback != "" {
		status.TransportFallbacks = append(status.TransportFallbacks, configv1alpha1.TransportFallbackStatus{
			Type:    "ConfirmedCommit",
			Reason:  result.ConfirmedCommitFallback,
			Message: "spec.confirmTimeoutSeconds set but auto-revert path is unavailable on this transport",
		})
	}
	status.FamilyStatus = status.FamilyStatus[:0]
	for _, fs := range result.FamilyStatuses {
		status.FamilyStatus = append(status.FamilyStatus, configv1alpha1.FamilyStatus{
			Name: fs.Name, State: fs.State, Entries: fs.Entries, OpCount: int32(fs.OpCount), Message: fs.Message,
		})
	}
	if len(result.AtomicReplaceOwnedKeys) > 0 {
		if status.AtomicReplaceOwnedKeys == nil {
			status.AtomicReplaceOwnedKeys = map[string][]string{}
		}
		for fam, fresh := range result.AtomicReplaceOwnedKeys {
			status.AtomicReplaceOwnedKeys[fam] = mergeOwnedKeys(status.AtomicReplaceOwnedKeys[fam], fresh)
		}
	}
	status.Drift = status.Drift[:0]
	capped, _ := engine.CapDrift(result.Drift)
	for _, d := range capped {
		status.Drift = append(status.Drift, configv1alpha1.DriftEntry{
			Family: d.Family, Path: d.Path, Desired: d.Desired, Observed: d.Observed, Detected: metav1.Now(),
		})
	}
	readyStatus := metav1.ConditionTrue
	readyReason := "Succeeded"
	readyMsg := "device reconciled to declared intent"
	if result.Phase != engine.PhaseInSync {
		readyStatus = metav1.ConditionFalse
		readyReason = result.Phase
		readyMsg = "not in sync"
		if result.Err != nil {
			readyMsg = result.Err.Error()
		}
	}
	setCommonCondition(status, metav1.Condition{
		Type: "Ready", Status: readyStatus, Reason: readyReason, Message: readyMsg,
	})
	if overlap := commonConflictMessage(r.Platform, cr, conflicts); overlap != "" {
		setCommonCondition(status, metav1.Condition{
			Type: "Conflict", Status: metav1.ConditionTrue, Reason: "FamilyOverlap", Message: overlap,
		})
	} else {
		setCommonCondition(status, metav1.Condition{
			Type: "Conflict", Status: metav1.ConditionFalse, Reason: "NoOverlap",
			Message: "no other " + r.Platform.Kind + " claims this CR's managed families",
		})
	}
	if r.Recorder != nil {
		switch result.Phase {
		case engine.PhaseInSync:
			r.Recorder.Eventf(cr, corev1.EventTypeNormal, "AppliedSuccess", "%s config reconciled", r.Platform.Kind)
		case engine.PhaseFailed:
			r.Recorder.Eventf(cr, corev1.EventTypeWarning, "ReconcileFailed", "%s", readyMsg)
		case engine.PhaseDrifted:
			r.Recorder.Eventf(cr, corev1.EventTypeNormal, "DriftDetected", "%s config drift detected", r.Platform.Kind)
		}
		switch {
		case result.SaveStartupErr != nil:
			r.Recorder.Eventf(cr, corev1.EventTypeWarning, "SaveStartupFailed",
				"startup-config save failed (apply itself succeeded): %v", result.SaveStartupErr)
		case result.SaveStartupOK:
			r.Recorder.Eventf(cr, corev1.EventTypeNormal, "SaveStartupOK", "startup-config saved")
		}
		if result.ConfirmedCommitFallback != "" {
			r.Recorder.Eventf(cr, corev1.EventTypeWarning, "ConfirmedCommitFallback",
				"spec.confirmTimeoutSeconds set but auto-revert path unavailable: %s; continuing without transport auto-revert",
				result.ConfirmedCommitFallback)
		} else if result.ConfirmedCommitUsed {
			r.Recorder.Eventf(cr, corev1.EventTypeNormal, "ConfirmedCommitUsed", "confirmed-commit auto-revert path used")
		}
	}
	return ignoreConflict(r.Client.Status().Update(ctx, updated))
}

func (r *CommonConfigReconciler) targetsDevice(obj client.Object) bool {
	if obj == nil {
		return false
	}
	spec := r.Platform.Spec(obj)
	return spec != nil && spec.DeviceRef.Name == r.DeviceName && r.inDeviceNamespace(obj)
}

func (r *CommonConfigReconciler) dueForDriftCheck(cr client.Object) bool {
	status := r.Platform.Status(cr)
	if status == nil || status.LastDeviceCheck == nil {
		return true
	}
	return time.Since(status.LastDeviceCheck.Time) >= r.driftDetectInterval(cr)
}

func (r *CommonConfigReconciler) requeueInterval(cr client.Object, phase string) time.Duration {
	full := r.driftDetectInterval(cr)
	if phase == engine.PhaseLeaseBlocked {
		const leaseBlockedRequeue = 15 * time.Second
		if leaseBlockedRequeue < full {
			return leaseBlockedRequeue
		}
	}
	return full
}

func (r *CommonConfigReconciler) driftDetectInterval(cr client.Object) time.Duration {
	spec := r.Platform.Spec(cr)
	if spec == nil || spec.DriftDetectInterval == "" {
		return defaultDriftDetectInterval
	}
	d, err := time.ParseDuration(spec.DriftDetectInterval)
	if err != nil {
		return defaultDriftDetectInterval
	}
	if d < minDriftDetectInterval {
		return minDriftDetectInterval
	}
	return d
}

func (r *CommonConfigReconciler) validate() error {
	if r.Client == nil {
		return fmt.Errorf("CommonConfigReconciler: nil Client")
	}
	if r.DeviceName == "" {
		return fmt.Errorf("CommonConfigReconciler: empty DeviceName")
	}
	return r.Platform.validate()
}

func commonConflictCheck(platform CommonConfigPlatform, deviceName string, allForDevice []client.Object) map[string][]string {
	seen := map[string][]string{}
	for _, cr := range allForDevice {
		spec := platform.Spec(cr)
		if spec == nil || spec.DeviceRef.Name != deviceName {
			continue
		}
		for _, fam := range spec.ManagedFamilies {
			seen[fam] = append(seen[fam], cr.GetNamespace()+"/"+cr.GetName())
		}
	}
	out := map[string][]string{}
	for family, owners := range seen {
		if len(owners) > 1 {
			out[family] = owners
		}
	}
	return out
}

func commonAsMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func commonConflictMessage(platform CommonConfigPlatform, cr client.Object, conflicts map[string][]string) string {
	spec := platform.Spec(cr)
	if spec == nil || len(spec.ManagedFamilies) == 0 {
		return ""
	}
	byOwner := map[string]map[string]struct{}{}
	for _, fam := range spec.ManagedFamilies {
		for _, owner := range conflicts[fam] {
			if _, ok := byOwner[owner]; !ok {
				byOwner[owner] = map[string]struct{}{}
			}
			byOwner[owner][fam] = struct{}{}
		}
	}
	if len(byOwner) == 0 {
		return ""
	}
	owners := make([]string, 0, len(byOwner))
	for owner := range byOwner {
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	parts := make([]string, 0, len(owners))
	for _, owner := range owners {
		families := make([]string, 0, len(byOwner[owner]))
		for family := range byOwner[owner] {
			families = append(families, family)
		}
		sort.Strings(families)
		parts = append(parts, fmt.Sprintf("%s on [%s]", owner, strings.Join(families, ",")))
	}
	return "overlaps with " + strings.Join(parts, "; ")
}

func setCommonCondition(status *configv1alpha1.CommonConfigStatus, c metav1.Condition) {
	if c.LastTransitionTime.IsZero() {
		c.LastTransitionTime = metav1.Now()
	}
	for i := range status.Conditions {
		if status.Conditions[i].Type == c.Type {
			if status.Conditions[i].Status == c.Status {
				c.LastTransitionTime = status.Conditions[i].LastTransitionTime
			}
			status.Conditions[i] = c
			return
		}
	}
	status.Conditions = append(status.Conditions, c)
}
