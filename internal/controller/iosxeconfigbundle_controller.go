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

package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation"
)

// IOSXEConfigBundleReconciler fans an IOSXEConfigBundle out into
// one IOSXEConfig per targeted CiscoDevice. Children are owned by
// the bundle so deleting the bundle GC's the children; an in-place
// update of spec.template propagates through to every child on the
// next reconcile.
//
// This controller deliberately does no per-family or device-side
// work — that's the per-device cisco-vk pod's job. It only owns
// the create/update/delete of child IOSXEConfig CRs.
type IOSXEConfigBundleReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigbundles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigbundles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=config.cisco.vk,resources=iosxeconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cisco.vk,resources=ciscodevices,verbs=get;list;watch

func (r *IOSXEConfigBundleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	var bundle configv1alpha1.IOSXEConfigBundle
	if err := r.Get(ctx, req.NamespacedName, &bundle); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	now := time.Now()
	ctx, _ = correlation.ApplyAnnotations(ctx, bundle.Annotations, now)
	ctx, span := correlation.Start(
		ctx,
		otel.Tracer("cisco-virtual-kubelet/iosxeconfigbundle-controller"),
		"cvk.iosxeconfigbundle.reconcile",
		oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
		oteltrace.WithAttributes(
			attribute.String("config.cisco.vk.iosxeconfigbundle.name", req.Name),
			attribute.String("config.cisco.vk.iosxeconfigbundle.namespace", req.Namespace),
		),
	)
	defer func() {
		span.SetAttributes(attribute.String("cvk.reconcile.result", reconcileResultAttribute(result)))
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, "reconcile")
		}
		span.End()
	}()

	devices, err := r.targetDevices(ctx, &bundle)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolve devices: %w", err)
	}

	// Garbage-collect children for devices that fell out of scope.
	// Children are namespaced and have an owner-ref pointing at the
	// bundle, but the controller still walks them explicitly so an
	// out-of-scope-but-not-yet-deleted child gets a clean removal
	// rather than waiting for the bundle to be deleted entirely.
	if err := r.pruneOrphans(ctx, &bundle, devices); err != nil {
		return ctrl.Result{}, fmt.Errorf("prune orphans: %w", err)
	}

	// Create or update one IOSXEConfig per matched device.
	generated := make([]configv1alpha1.GeneratedCR, 0, len(devices))
	for _, dev := range devices {
		child, err := r.upsertChild(ctx, &bundle, dev)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("upsert %s: %w", dev.Name, err)
		}
		generated = append(generated, configv1alpha1.GeneratedCR{
			Name:   child.Name,
			Device: dev.Name,
			Phase:  child.Status.Phase,
		})
	}
	sort.Slice(generated, func(i, j int) bool { return generated[i].Name < generated[j].Name })

	bundle.Status.ObservedGeneration = bundle.Generation
	bundle.Status.MemberDevices = int32(len(devices))
	bundle.Status.GeneratedCRs = generated
	setBundleReady(&bundle, len(devices))
	if err := r.Status().Update(ctx, &bundle); err != nil {
		return ctrl.Result{}, fmt.Errorf("update status: %w", err)
	}
	return ctrl.Result{}, nil
}

// targetDevices computes the union of DeviceRefs and the
// DeviceSelector matches in the bundle's namespace. Duplicates
// (a device named in both lists) are de-duplicated.
func (r *IOSXEConfigBundleReconciler) targetDevices(
	ctx context.Context, b *configv1alpha1.IOSXEConfigBundle,
) ([]ciskov1.CiscoDevice, error) {
	seen := map[string]struct{}{}
	out := []ciskov1.CiscoDevice{}

	for _, ref := range b.Spec.DeviceRefs {
		var dev ciskov1.CiscoDevice
		if err := r.Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: ref.Name}, &dev); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("get device %q: %w", ref.Name, err)
		}
		if _, dup := seen[dev.Name]; dup {
			continue
		}
		seen[dev.Name] = struct{}{}
		out = append(out, dev)
	}

	if b.Spec.DeviceSelector != nil {
		sel, err := metav1.LabelSelectorAsSelector(b.Spec.DeviceSelector)
		if err != nil {
			return nil, fmt.Errorf("parse selector: %w", err)
		}
		var list ciskov1.CiscoDeviceList
		if err := r.List(ctx, &list, client.InNamespace(b.Namespace), client.MatchingLabelsSelector{Selector: sel}); err != nil {
			return nil, fmt.Errorf("list devices: %w", err)
		}
		for i := range list.Items {
			dev := list.Items[i]
			if _, dup := seen[dev.Name]; dup {
				continue
			}
			// Defence-in-depth: re-check the selector against the
			// device's labels, in case the cache returns a stale
			// label set during a label update.
			if !sel.Matches(labels.Set(dev.Labels)) {
				continue
			}
			seen[dev.Name] = struct{}{}
			out = append(out, dev)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// upsertChild creates or updates the IOSXEConfig CR for one
// device. The child's spec is bundle.Spec.Template with DeviceRef
// filled in. An ownerRef makes the bundle the GC root for the
// child: deleting the bundle removes every child without an
// extra controller pass.
func (r *IOSXEConfigBundleReconciler) upsertChild(
	ctx context.Context,
	b *configv1alpha1.IOSXEConfigBundle,
	dev ciskov1.CiscoDevice,
) (*configv1alpha1.IOSXEConfig, error) {
	name := bundleChildName(b.Name, dev.Name)
	child := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: b.Namespace},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, child, func() error {
		// Preserve any prior ownerRef set we may have lost; mark
		// ourselves controller=true so the cascading-delete path
		// works.
		if err := controllerutil.SetControllerReference(b, child, r.Scheme); err != nil {
			return err
		}
		child.Spec = configv1alpha1.IOSXEConfigSpec{
			DeviceRef:               configv1alpha1.DeviceRef{Name: dev.Name},
			IOSXEConfigTemplateSpec: b.Spec.Template,
		}
		// Carry the bundle name as a label so an operator can
		// `kubectl get iosxeconfigs -l config.cisco.vk/bundle=<name>`
		// without inspecting ownerRefs by hand.
		if child.Labels == nil {
			child.Labels = map[string]string{}
		}
		child.Labels["config.cisco.vk/bundle"] = b.Name
		child.Annotations = propagatedCorrelationAnnotations(child.Annotations, b.Annotations, time.Now())
		return nil
	})
	_ = op
	return child, err
}

// pruneOrphans deletes child IOSXEConfigs labelled with this
// bundle that target devices no longer in the resolved set.
// Doing this before the create/update pass means an
// in-place selector edit ("drop role=staging") removes
// out-of-scope children on the same reconcile.
func (r *IOSXEConfigBundleReconciler) pruneOrphans(
	ctx context.Context,
	b *configv1alpha1.IOSXEConfigBundle,
	keep []ciskov1.CiscoDevice,
) error {
	keepSet := make(map[string]struct{}, len(keep))
	for _, d := range keep {
		keepSet[d.Name] = struct{}{}
	}
	var children configv1alpha1.IOSXEConfigList
	if err := r.List(ctx, &children, client.InNamespace(b.Namespace),
		client.MatchingLabels{"config.cisco.vk/bundle": b.Name}); err != nil {
		return err
	}
	for i := range children.Items {
		c := &children.Items[i]
		if _, kept := keepSet[c.Spec.DeviceRef.Name]; kept {
			continue
		}
		if err := r.Delete(ctx, c); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func bundleChildName(bundle, device string) string {
	return fmt.Sprintf("%s-%s", bundle, device)
}

func setBundleReady(b *configv1alpha1.IOSXEConfigBundle, deviceCount int) {
	now := metav1.Now()
	cond := metav1.Condition{
		Type: "Ready", LastTransitionTime: now,
		ObservedGeneration: b.Generation,
	}
	if deviceCount == 0 {
		cond.Status = metav1.ConditionFalse
		cond.Reason = "NoMatchingDevices"
		cond.Message = "spec.deviceRefs and spec.deviceSelector together matched zero devices"
	} else {
		cond.Status = metav1.ConditionTrue
		cond.Reason = "Reconciled"
		cond.Message = fmt.Sprintf("fanned out to %d device(s)", deviceCount)
	}
	// Replace the existing Ready condition (if any) in-place so
	// the listType=map invariant on conditions stays satisfied.
	for i, existing := range b.Status.Conditions {
		if existing.Type == "Ready" {
			b.Status.Conditions[i] = cond
			return
		}
	}
	b.Status.Conditions = append(b.Status.Conditions, cond)
}

// SetupWithManager wires the bundle reconciler. Three watches:
//
//  1. IOSXEConfigBundle — primary CR.
//  2. IOSXEConfig children (Owns) — keeps bundle status fresh when
//     a child's phase or status changes.
//  3. CiscoDevice (Wave 3A — external-review Finding #9) — selector
//     membership is dynamic. A new CiscoDevice that matches a
//     bundle's selector, or a label-change that moves a device in
//     or out of selector membership, must trigger fan-out / prune.
//     Without this watch the bundle's children list goes stale
//     until some other event requeues the bundle.
func (r *IOSXEConfigBundleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&configv1alpha1.IOSXEConfigBundle{}).
		Owns(&configv1alpha1.IOSXEConfig{}).
		Watches(&ciskov1.CiscoDevice{}, handler.EnqueueRequestsFromMapFunc(r.mapDeviceToBundles)).
		Complete(r)
}

// mapDeviceToBundles fans a CiscoDevice event out to every
// IOSXEConfigBundle in the same namespace. The reconciler then
// re-evaluates each bundle's selector + deviceRefs against the
// current device set; bundles whose membership genuinely changed
// produce work, the rest no-op via the upsert path's hash check.
//
// Why broad (every bundle in the namespace) rather than indexed:
// IOSXEConfigBundle.spec.deviceSelector is a label selector, not a
// label match — building an indexer that maps a single device's
// labels to the matching bundles requires evaluating every bundle's
// selector against the device anyway. The broad mapper is simpler,
// correct, and — given a typical fleet has tens of bundles, not
// thousands — cheap. If that ratio inverts later, swap in
// fieldindexer-backed lookup.
func (r *IOSXEConfigBundleReconciler) mapDeviceToBundles(ctx context.Context, obj client.Object) []reconcile.Request {
	dev, ok := obj.(*ciskov1.CiscoDevice)
	if !ok {
		return nil
	}
	var bundles configv1alpha1.IOSXEConfigBundleList
	if err := r.List(ctx, &bundles, client.InNamespace(dev.Namespace)); err != nil {
		crlog.FromContext(ctx).Error(err, "list IOSXEConfigBundles for device-event mapping",
			"device", dev.Name, "namespace", dev.Namespace)
		return nil
	}
	out := make([]reconcile.Request, 0, len(bundles.Items))
	for i := range bundles.Items {
		out = append(out, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: bundles.Items[i].Namespace,
				Name:      bundles.Items[i].Name,
			},
		})
	}
	return out
}
