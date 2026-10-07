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

package nexusdashboard

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
)

const (
	defaultRequestTimeout = 30 * time.Second
	defaultHealthInterval = time.Minute
	defaultRPS            = 5
	defaultBurst          = 10
	defaultConcurrency    = 4
)

type adapter struct {
	key               types.NamespacedName
	client            *client
	interval          time.Duration
	rotation          <-chan struct{}
	probe             func(context.Context) error
	invalidate        func()
	now               func() time.Time
	list              func(context.Context) ([]InventoryItem, error)
	inventoryInterval time.Duration
	inventory         inventoryStore
	statusWriter      ctrlclient.Client // set in SetupWithManager
	adoption          *ciskov1.NetworkControllerDeviceAdoption
	uid               string
	devices           *deviceSyncer // nil unless adoption is enabled
}

// newAdapter validates local options only. It never dials and starts nothing.
func newAdapter(opts controlleradapter.Options) (controlleradapter.Adapter, error) {
	nc := opts.Controller
	conn := nc.Spec.Connection
	timeout := defaultRequestTimeout
	if conn.RequestTimeout != nil {
		timeout = conn.RequestTimeout.Duration
	}
	interval := defaultHealthInterval
	if conn.HealthCheckInterval != nil {
		interval = conn.HealthCheckInterval.Duration
	}
	rps, burst := defaultRPS, defaultBurst
	if rl := conn.RateLimit; rl != nil {
		rps, burst = int(rl.RequestsPerSecond), int(rl.Burst)
	}
	concurrency := int(conn.MaxConcurrentRequests)
	if concurrency < 1 {
		concurrency = defaultConcurrency
	}
	if ad := nc.Spec.DeviceAdoption; ad != nil && ad.Enabled && ad.Defaults == nil {
		return nil, fmt.Errorf("nexus-dashboard: deviceAdoption is enabled without defaults")
	}
	insecure := nc.Spec.TLS != nil && nc.Spec.TLS.InsecureSkipVerify
	if timeout <= 0 || interval <= 0 {
		return nil, fmt.Errorf("nexus-dashboard: non-positive timeout or health interval")
	}
	c := newClient(clientConfig{
		Endpoint:           nc.Spec.Endpoint,
		CredentialPath:     opts.CredentialPath,
		CAPath:             opts.CAPath,
		InsecureSkipVerify: insecure,
		RequestTimeout:     timeout,
		MaxSessionLifetime: opts.MaterialRotation.MaxSessionLifetime,
		MaxConcurrent:      concurrency,
		Limiter:            rate.NewLimiter(rate.Limit(rps), burst),
	})
	return &adapter{
		key:               types.NamespacedName{Namespace: nc.Namespace, Name: nc.Name},
		client:            c,
		interval:          interval,
		rotation:          opts.MaterialRotation.Changes,
		probe:             c.Probe,
		list:              c.ListInventory,
		inventoryInterval: defaultInventoryInterval,
		invalidate:        c.Invalidate,
		now:               time.Now,
		adoption:          nc.Spec.DeviceAdoption.DeepCopy(),
		uid:               string(nc.UID),
	}, nil
}

// SetupWithManager registers the health loop as a manager-owned Runnable.
func (a *adapter) SetupWithManager(mgr ctrl.Manager) error {
	a.statusWriter = mgr.GetClient()
	if a.adoption != nil && a.adoption.Enabled {
		a.devices = &deviceSyncer{client: a.statusWriter, namespace: a.key.Namespace, uid: a.uid, adoption: a.adoption, now: a.now}
	}
	if err := mgr.Add(&healthRunnable{adapter: a}); err != nil {
		return err
	}
	return mgr.Add(&inventoryRunnable{adapter: a})
}

type healthRunnable struct{ adapter *adapter }

// NeedLeaderElection is false: each worker owns exactly one endpoint.
func (*healthRunnable) NeedLeaderElection() bool { return false }

func (r *healthRunnable) Start(ctx context.Context) error {
	a := r.adapter
	a.check(ctx)
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-a.rotation:
			a.invalidate()
			a.check(ctx)
		case <-ticker.C:
			a.check(ctx)
		}
	}
}

// check probes ND and publishes the result; status write failures are logged
// by the next tick and never stop the manager.
func (a *adapter) check(ctx context.Context) {
	log := ctrl.Log.WithName("nexus-dashboard").WithValues("networkController", a.key)
	result := classify(a.probe(ctx))
	if ctx.Err() != nil {
		return
	}
	if err := a.publish(ctx, result); err != nil {
		log.Error(err, "publish NetworkController status")
	}
}

func (a *adapter) publish(ctx context.Context, res healthResult) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var nc ciskov1.NetworkController
		if err := a.statusWriter.Get(ctx, a.key, &nc); err != nil {
			return err
		}
		before := nc.DeepCopy()
		applyHealth(&nc, res, a.now())
		if equalStatus(before, &nc) {
			return nil
		}
		return a.statusWriter.Status().Update(ctx, &nc)
	})
}

func applyHealth(nc *ciskov1.NetworkController, res healthResult, now time.Time) {
	t := metav1.NewTime(now)
	st := &nc.Status
	st.ObservedGeneration = nc.Generation
	st.LastAttemptTime = &t
	if res.ready() {
		st.LastSuccessfulConnectionTime = &t
		st.APIVersion = "v1"
	}
	st.Phase = res.phase
	set := func(typ string, ok bool, okReason, failReason, msg string) {
		status, reason := metav1.ConditionTrue, okReason
		if !ok {
			status, reason = metav1.ConditionFalse, failReason
		}
		meta.SetStatusCondition(&st.Conditions, metav1.Condition{
			Type: typ, Status: status, Reason: reason, Message: msg, ObservedGeneration: nc.Generation,
		})
	}
	set(ciskov1.NetworkControllerConditionAuthenticated, res.authenticated, "LoginSucceeded", res.reason, res.authMessage())
	set(ciskov1.NetworkControllerConditionAPICompatible, res.compatible, "ManageAPIAvailable", res.reason, res.compatMessage())
	set(ciskov1.NetworkControllerConditionReady, res.ready(), "Connected", res.reason, res.message)
	setCapability(st, CapabilityHealth, res.ready(), res.message)
}
