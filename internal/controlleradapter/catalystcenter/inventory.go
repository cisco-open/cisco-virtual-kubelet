// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"fmt"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const inventoryInterval = 5 * time.Minute

type adapter struct {
	key          types.NamespacedName
	client       *client
	interval     time.Duration
	rotation     <-chan struct{}
	statusWriter ctrlclient.Client
}

func newAdapter(opts controlleradapter.Options) (controlleradapter.Adapter, error) {
	nc := opts.Controller
	timeout := 30 * time.Second
	if nc.Spec.Connection.RequestTimeout != nil {
		timeout = nc.Spec.Connection.RequestTimeout.Duration
	}
	interval := time.Minute
	if nc.Spec.Connection.HealthCheckInterval != nil {
		interval = nc.Spec.Connection.HealthCheckInterval.Duration
	}
	rps, burst := 5, 10
	if rl := nc.Spec.Connection.RateLimit; rl != nil {
		rps, burst = int(rl.RequestsPerSecond), int(rl.Burst)
	}
	concurrency := int(nc.Spec.Connection.MaxConcurrentRequests)
	if concurrency < 1 {
		concurrency = 4
	}
	if timeout <= 0 || interval <= 0 || rps <= 0 || burst <= 0 {
		return nil, fmt.Errorf("catalyst-center: invalid connection settings")
	}
	c := newClient(clientConfig{Endpoint: nc.Spec.Endpoint, CredentialPath: opts.CredentialPath, CAPath: opts.CAPath, InsecureSkipVerify: nc.Spec.TLS != nil && nc.Spec.TLS.InsecureSkipVerify, RequestTimeout: timeout, MaxSessionLifetime: opts.MaterialRotation.MaxSessionLifetime, MaxConcurrent: concurrency, Limiter: rate.NewLimiter(rate.Limit(rps), burst)})
	return &adapter{key: types.NamespacedName{Namespace: nc.Namespace, Name: nc.Name}, client: c, interval: interval, rotation: opts.MaterialRotation.Changes}, nil
}

func (a *adapter) SetupWithManager(mgr ctrl.Manager) error {
	a.statusWriter = mgr.GetClient()
	if err := mgr.Add(&healthRunnable{a}); err != nil {
		return err
	}
	return mgr.Add(&inventoryRunnable{a})
}

type healthRunnable struct{ a *adapter }

func (*healthRunnable) NeedLeaderElection() bool { return false }
func (r *healthRunnable) Start(ctx context.Context) error {
	r.a.check(ctx)
	t := time.NewTicker(r.a.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.a.rotation:
			r.a.client.Invalidate()
			r.a.check(ctx)
		case <-t.C:
			r.a.check(ctx)
		}
	}
}
func (a *adapter) check(ctx context.Context) {
	res := classify(a.client.Probe(ctx))
	if ctx.Err() != nil {
		return
	}
	_ = a.publishHealth(ctx, res)
}
func (a *adapter) publishHealth(ctx context.Context, res healthResult) error {
	return ctrlclient.IgnoreNotFound(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var nc ciskov1.NetworkController
		if err := a.statusWriter.Get(ctx, a.key, &nc); err != nil {
			return err
		}
		before := nc.DeepCopy()
		now := metav1.Now()
		nc.Status.ObservedGeneration = nc.Generation
		nc.Status.LastAttemptTime = &now
		if res.ready() {
			nc.Status.LastSuccessfulConnectionTime = &now
			nc.Status.APIVersion = "v1"
		}
		nc.Status.Phase = res.phase
		meta.SetStatusCondition(&nc.Status.Conditions, condition(ciskov1.NetworkControllerConditionAuthenticated, res.authenticated, res.reason, res.message, nc.Generation))
		meta.SetStatusCondition(&nc.Status.Conditions, condition(ciskov1.NetworkControllerConditionAPICompatible, res.compatible, res.reason, res.message, nc.Generation))
		meta.SetStatusCondition(&nc.Status.Conditions, condition(ciskov1.NetworkControllerConditionReady, res.ready(), res.reason, res.message, nc.Generation))
		setCapability(&nc.Status, CapabilityHealth, res.ready(), res.message)
		if equalStatus(before, &nc) {
			return nil
		}
		return a.statusWriter.Status().Update(ctx, &nc)
	}))
}

type inventoryRunnable struct{ a *adapter }

func (*inventoryRunnable) NeedLeaderElection() bool { return false }
func (r *inventoryRunnable) Start(ctx context.Context) error {
	r.a.syncInventory(ctx)
	t := time.NewTicker(inventoryInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			r.a.syncInventory(ctx)
		}
	}
}
func (a *adapter) syncInventory(ctx context.Context) {
	items, err := a.client.ListDevices(ctx)
	images, imageErr := a.client.ListImages(ctx)
	if ctx.Err() != nil {
		return
	}
	_ = a.publishInventory(ctx, items, err, images, imageErr)
}
func (a *adapter) publishInventory(ctx context.Context, items []Device, err error, images []Image, imageErr error) error {
	return ctrlclient.IgnoreNotFound(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var nc ciskov1.NetworkController
		if e := a.statusWriter.Get(ctx, a.key, &nc); e != nil {
			return e
		}
		before := nc.DeepCopy()
		msg := fmt.Sprintf("%d devices returned", len(items))
		ok := err == nil
		if err != nil {
			msg = "inventory refresh failed"
		}
		setCapability(&nc.Status, CapabilityInventory, ok, msg)
		imageMessage := fmt.Sprintf("%d imported images returned; no SWIM operation controller is registered", len(images))
		if imageErr != nil {
			imageMessage = "image inventory refresh failed"
		}
		setCapability(&nc.Status, CapabilitySWIM, false, imageMessage)
		if equalStatus(before, &nc) {
			return nil
		}
		return a.statusWriter.Status().Update(ctx, &nc)
	}))
}
