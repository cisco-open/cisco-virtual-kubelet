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
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver"
)

// defaultConfigReconcileInterval is the Phase-0 poll cadence. Phase-1
// replaces polling with an informer-backed watch; the interval is retained
// as the fallback when the informer cache has not yet synced.
const defaultConfigReconcileInterval = 5 * time.Second

// ConfigReconciler observes IOSXEConfig CRs that target a single device and
// drives the per-device configdriver.Driver through its state machine.
//
// In Phase-0 the reconciler only records an initial Pending phase on each
// matching CR so operators and GitOps agents see the CR has been picked up.
// No device I/O happens yet; the driver stub's write-path methods return
// configdriver.ErrNotImplemented and status continues to reflect Pending.
type ConfigReconciler struct {
	// Client is a controller-runtime client already wired to a scheme that
	// has config.cisco.vk/v1alpha1 registered.
	Client client.Client

	// DeviceName is the CiscoDevice the enclosing cisco-vk run process
	// owns. The reconciler ignores any IOSXEConfig whose spec.deviceRef.name
	// does not match.
	DeviceName string

	// Driver is the per-device config driver. The reconciler never closes
	// the driver; lifecycle is the caller's responsibility.
	Driver configdriver.Driver

	// Interval is the poll cadence. Zero means defaultConfigReconcileInterval.
	Interval time.Duration
}

// Run blocks until ctx is cancelled. It returns ctx.Err() on exit so a
// caller running it in an errgroup observes the cause.
func (r *ConfigReconciler) Run(ctx context.Context) error {
	if r.Client == nil {
		return errors.New("ConfigReconciler: nil Client")
	}
	if r.DeviceName == "" {
		return errors.New("ConfigReconciler: empty DeviceName")
	}
	if r.Driver == nil {
		return errors.New("ConfigReconciler: nil Driver")
	}

	interval := r.Interval
	if interval <= 0 {
		interval = defaultConfigReconcileInterval
	}

	logger := log.G(ctx).WithField("component", "config-reconciler").
		WithField("device", r.DeviceName)
	logger.WithField("interval", interval).Info("starting IOSXEConfig reconcile loop")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run one pass immediately so status appears before the first tick.
	r.reconcileOnce(ctx, logger)

	for {
		select {
		case <-ctx.Done():
			logger.Info("stopping IOSXEConfig reconcile loop")
			return ctx.Err()
		case <-ticker.C:
			r.reconcileOnce(ctx, logger)
		}
	}
}

// reconcileOnce lists every IOSXEConfig in the cluster and dispatches those
// targeting this reconciler's device. List failures are logged and the
// tick is skipped — a transient API-server outage must not terminate the
// loop because the provider process has no supervisor to restart it.
func (r *ConfigReconciler) reconcileOnce(ctx context.Context, logger log.Logger) {
	var list configv1alpha1.IOSXEConfigList
	if err := r.Client.List(ctx, &list); err != nil {
		logger.WithError(err).Warn("list IOSXEConfig failed; skipping tick")
		return
	}

	for i := range list.Items {
		cr := &list.Items[i]
		if cr.Spec.DeviceRef.Name != r.DeviceName {
			continue
		}
		if err := r.handle(ctx, cr); err != nil {
			logger.WithError(err).
				WithField("name", cr.Name).
				WithField("namespace", cr.Namespace).
				Warn("handle IOSXEConfig failed")
		}
	}
}

// handle is invoked per matching CR. Phase-0 behaviour: if the CR has no
// phase yet, record Pending so operators see it has been picked up. Later
// phases invoke Driver.Validate → Plan → Apply here.
func (r *ConfigReconciler) handle(ctx context.Context, cr *configv1alpha1.IOSXEConfig) error {
	if cr.Status.Phase != "" && cr.Status.ObservedGeneration == cr.Generation {
		// Already acknowledged at this generation; nothing to do in Phase 0.
		return nil
	}

	// Copy so the write to status does not mutate the listed object.
	updated := cr.DeepCopy()
	updated.Status.Phase = "Pending"
	updated.Status.ObservedGeneration = cr.Generation

	if err := r.Client.Status().Update(ctx, updated); err != nil {
		// Conflict is expected and harmless — the next tick re-observes.
		if apierrors.IsConflict(err) {
			return nil
		}
		return fmt.Errorf("update status: %w", err)
	}
	return nil
}
