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

// Package maintenance coordinates ordinary device writes with disruptive
// operations. Reads do not participate, and existing pods are never drained.
package maintenance

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/mutationguard"
)

const (
	TaintKey            = "cisco.vk/device-maintenance"
	TaintValue          = "gnoi"
	routineHolderPrefix = "device-write/"
	writeLeaseTTL       = 31 * time.Minute
	apiTimeout          = 10 * time.Second
	maxWriteDuration    = 30 * time.Minute
)

// Coordinator observes every IOS-XE per-device runtime, including after a
// mutation gate is disabled. Client must be uncached: Lease decisions and Node
// patches must observe current API state, including another process's writes.
type Coordinator struct {
	Client         client.Client
	Namespace      string
	DeviceName     string
	DeviceUID      string
	NodeName       string
	LeaseNamespace string
	// ManagedTopology replaces direct worker Node-spec writes with the
	// durable Lease request / manager acknowledgement protocol.
	ManagedTopology bool
	// MutationsEnabled enables routine-write acquisition before new gNOI
	// operations can start. When false, existing leases still fence writes and
	// retain taints, but an idle legacy worker performs only Kubernetes reads.
	MutationsEnabled bool

	nodeMu sync.Mutex
	// Private timing hooks keep cancellation and renewal tests deterministic.
	leaseTTL      time.Duration
	renewInterval time.Duration
}

// AcquireWrite claims the same Lease held by disruptive gNOI operations for
// one ordinary write/transaction. Every call has a unique owner, including two
// concurrent callbacks for the same Pod. The caller must use the returned
// context and invoke finish only after all device work has stopped.
func (c *Coordinator) AcquireWrite(ctx context.Context) (context.Context, func(error), error) {
	if c == nil {
		return ctx, func(error) {}, nil
	}
	if c.Client == nil || c.Namespace == "" || c.DeviceName == "" || c.LeaseNamespace == "" {
		return ctx, nil, fmt.Errorf("device maintenance coordinator is incomplete")
	}
	if err := c.checkManagedWriteSession(ctx); err != nil {
		return ctx, nil, err
	}
	key := devicecoordination.DeviceKey(c.Namespace, c.DeviceName)
	if !c.MutationsEnabled && !c.ManagedTopology {
		readCtx, readCancel := context.WithTimeout(ctx, apiTimeout)
		var existing coordv1.Lease
		err := c.Client.Get(readCtx, types.NamespacedName{Namespace: c.LeaseNamespace,
			Name: engine.LeaseName(key, devicecoordination.MutationLeaseFamily)}, &existing)
		readCancel()
		if apierrors.IsNotFound(err) {
			riskCtx, riskCancel := context.WithTimeout(ctx, apiTimeout)
			risk, riskErr := mutationguard.FindCanonicalRisk(riskCtx, c.Client, c.Namespace, c.DeviceName, time.Now())
			riskCancel()
			if riskErr != nil {
				return ctx, nil, fmt.Errorf("device maintenance safety scan: %w", riskErr)
			}
			if risk == nil {
				return ctx, func(error) {}, nil
			}
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return ctx, nil, fmt.Errorf("read device maintenance lease: %w", err)
		}
	}
	identity := routineHolderPrefix + uuid.NewString()
	ttl := c.leaseTTL
	if ttl <= 0 {
		ttl = writeLeaseTTL
	}
	leaser := &engine.FamilyLeaser{Client: c.Client, Namespace: c.LeaseNamespace, TTL: ttl, RequireExisting: c.ManagedTopology}
	acquireCtx, acquireCancel := context.WithTimeout(ctx, apiTimeout)
	defer acquireCancel()
	guard, err := mutationguard.EnsureCanonicalQuarantine(acquireCtx, c.Client, leaser,
		c.Namespace, c.DeviceName, key, identity, time.Now())
	if err != nil {
		return ctx, nil, fmt.Errorf("device maintenance safety scan: %w", err)
	}
	if guard.Risk != nil {
		return ctx, nil, fmt.Errorf("device maintenance: waiting for %s", guard.Risk)
	}
	result, err := leaser.Acquire(acquireCtx, key, devicecoordination.MutationLeaseFamily, identity)
	if err != nil {
		return ctx, nil, fmt.Errorf("device maintenance lease: %w", err)
	}
	if !result.Owned {
		return ctx, nil, fmt.Errorf("device maintenance: mutation lease held by %s", result.Holder)
	}
	// The previous operation can release its holder between the first session
	// read and acquisition. Recheck under our Lease before any device write.
	if err := c.checkManagedWriteSession(ctx); err != nil {
		_ = leaser.Release(acquireCtx, key, devicecoordination.MutationLeaseFamily, identity)
		return ctx, nil, err
	}
	writeCtx, cancel := context.WithTimeout(ctx, maxWriteDuration)
	stop := make(chan struct{})
	done := make(chan struct{})
	interval := c.renewInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-writeCtx.Done():
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(writeCtx, apiTimeout)
				renewed, renewErr := leaser.Acquire(renewCtx, key, devicecoordination.MutationLeaseFamily, identity)
				renewCancel()
				if renewErr != nil || !renewed.Owned {
					log.G(ctx).WithError(renewErr).Warn("device maintenance lease renewal lost; cancelling device write")
					cancel()
					return
				}
			}
		}
	}()
	var once sync.Once
	finish := func(outcome error) {
		once.Do(func() {
			close(stop)
			<-done
			// Cancellation can leave device-side work in progress. Retain the
			// remaining lease rather than immediately authorizing a disruption.
			cancelled := writeCtx.Err() != nil || outcome != nil
			cancel()
			if cancelled {
				return
			}
			releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), apiTimeout)
			defer releaseCancel()
			if err := leaser.Release(releaseCtx, key, devicecoordination.MutationLeaseFamily, identity); err != nil {
				log.G(ctx).WithError(err).Warn("device maintenance write lease release failed; waiting for expiry")
			}
		})
	}
	return writeCtx, finish, nil
}

// BeforeMutation is called only after gNOI owns its disruptive Lease. A failed
// Node patch blocks dispatch. NoSchedule affects new placement without evicting
// running workloads; AcquireWrite also protects explicit nodeName placements.
func (c *Coordinator) BeforeMutation(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if c.ManagedTopology {
		return fmt.Errorf("managed topology permits disruptive mutation only through a campaign-owned software-upgrade maintenance session")
	}
	c.nodeMu.Lock()
	defer c.nodeMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	return c.patchTaint(ctx, true)
}

// GuardRecovery decides whether status-triggered asynchronous app recovery
// needs lifecycle fencing at startup. An idle worker with both mutation gates
// disabled preserves the existing status behavior. Gate changes roll the
// single per-device worker; an existing or unreadable Lease fails closed.
func (c *Coordinator) GuardRecovery(ctx context.Context) bool {
	if c == nil {
		return false
	}
	if c.MutationsEnabled {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	var lease coordv1.Lease
	err := c.Client.Get(ctx, types.NamespacedName{Namespace: c.LeaseNamespace,
		Name: engine.LeaseName(devicecoordination.DeviceKey(c.Namespace, c.DeviceName), devicecoordination.MutationLeaseFamily)}, &lease)
	if !apierrors.IsNotFound(err) {
		return true
	}
	risk, err := mutationguard.FindCanonicalRisk(ctx, c.Client, c.Namespace, c.DeviceName, time.Now())
	return err != nil || risk != nil
}

// Run restores scheduling only after the disruptive Lease has been released
// or expired, including a retained quarantine. Read errors preserve the taint.
func (c *Coordinator) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if err := c.Sync(ctx); err != nil && ctx.Err() == nil {
			log.G(ctx).WithError(err).Warn("device maintenance scheduling sync failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Coordinator) Sync(ctx context.Context) error {
	if c != nil && c.ManagedTopology {
		// The manager owns Node spec in managed mode. Lease and leaf watches
		// drive its durable guard/session reconciliation; the worker must never
		// race that ownership from this background loop.
		return nil
	}
	c.nodeMu.Lock()
	defer c.nodeMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	var lease coordv1.Lease
	err := c.Client.Get(ctx, types.NamespacedName{
		Namespace: c.LeaseNamespace,
		Name:      engine.LeaseName(devicecoordination.DeviceKey(c.Namespace, c.DeviceName), devicecoordination.MutationLeaseFamily),
	}, &lease)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	active := false
	if err == nil && lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "" &&
		!strings.HasPrefix(*lease.Spec.HolderIdentity, routineHolderPrefix) {
		active = true
		if lease.Spec.RenewTime != nil && lease.Spec.LeaseDurationSeconds != nil && *lease.Spec.LeaseDurationSeconds > 0 {
			active = time.Now().Before(lease.Spec.RenewTime.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second))
		}
	}
	if !active {
		risk, riskErr := mutationguard.FindCanonicalRisk(ctx, c.Client, c.Namespace, c.DeviceName, time.Now())
		if riskErr != nil {
			return riskErr
		}
		active = risk != nil
	}
	err = c.patchTaint(ctx, active)
	// A newly started idle worker may not have registered its Node yet.
	if !active && apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// Taints returns a copy preserving every operator-owned taint. Only this
// controller's exact key/value/effect tuple is managed here.
func Taints(existing []corev1.Taint, active bool) []corev1.Taint {
	out := make([]corev1.Taint, 0, len(existing)+1)
	for _, taint := range existing {
		if taint.Key == TaintKey && taint.Value == TaintValue && taint.Effect == corev1.TaintEffectNoSchedule {
			continue
		}
		out = append(out, taint)
	}
	if active {
		out = append(out, corev1.Taint{Key: TaintKey, Value: TaintValue, Effect: corev1.TaintEffectNoSchedule})
	}
	return out
}

func (c *Coordinator) patchTaint(ctx context.Context, active bool) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var node corev1.Node
		if err := c.Client.Get(ctx, types.NamespacedName{Name: c.NodeName}, &node); err != nil {
			return err
		}
		desired := Taints(node.Spec.Taints, active)
		if reflect.DeepEqual(desired, node.Spec.Taints) || len(desired) == 0 && len(node.Spec.Taints) == 0 {
			return nil
		}
		before := node.DeepCopy()
		node.Spec.Taints = desired
		return c.Client.Patch(ctx, &node, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	})
}
