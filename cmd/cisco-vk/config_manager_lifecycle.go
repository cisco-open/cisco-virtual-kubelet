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

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	crmanager "sigs.k8s.io/controller-runtime/pkg/manager"
)

const networkManagerHealthProbeAddress = ":8081"

// configManagerLifecycle connects controller-runtime's internal lifecycle to
// both Kubernetes probes and the outer network-management process. A dedicated
// network worker must not be Ready merely because its bootstrap function
// returned: its cache and controllers must have started, and an unexpected
// manager exit must terminate the worker so Kubernetes can replace it.
type configManagerLifecycle struct {
	mu      sync.RWMutex
	ready   bool
	stopped bool
	runErr  error
	done    chan struct{}
	once    sync.Once
}

func newConfigManagerLifecycle() *configManagerLifecycle {
	return &configManagerLifecycle{done: make(chan struct{})}
}

func configManagerHealthProbeAddress(lifecycle *configManagerLifecycle) string {
	if lifecycle == nil {
		return "0"
	}
	return networkManagerHealthProbeAddress
}

func addConfigManagerLifecycleChecks(mgr crmanager.Manager, lifecycle *configManagerLifecycle) error {
	if lifecycle == nil {
		return nil
	}
	if err := mgr.AddHealthzCheck("config-manager", lifecycle.healthCheck); err != nil {
		return fmt.Errorf("add config manager health check: %w", err)
	}
	if err := mgr.AddReadyzCheck("config-manager", lifecycle.readyCheck); err != nil {
		return fmt.Errorf("add config manager readiness check: %w", err)
	}
	return nil
}

func startConfigManager(
	ctx context.Context,
	mgr crmanager.Manager,
	lifecycle *configManagerLifecycle,
	onUnexpectedExit func(error),
) {
	go func() {
		runErr := mgr.Start(ctx)
		if lifecycle != nil {
			lifecycle.markStopped(runErr)
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) && onUnexpectedExit != nil {
			onUnexpectedExit(runErr)
		}
	}()
	if lifecycle == nil {
		return
	}
	go func() {
		select {
		case <-mgr.Elected():
			// Leader election is disabled for per-device workers. In
			// controller-runtime this channel closes after the shared cache
			// has synced and the controller runnable group has started.
			lifecycle.markReady()
		case <-lifecycle.Done():
		case <-ctx.Done():
		}
	}()
}

func (l *configManagerLifecycle) markReady() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.stopped {
		l.ready = true
	}
}

func (l *configManagerLifecycle) markStopped(err error) {
	if l == nil {
		return
	}
	l.once.Do(func() {
		l.mu.Lock()
		l.ready = false
		l.stopped = true
		l.runErr = err
		l.mu.Unlock()
		close(l.done)
	})
}

func (l *configManagerLifecycle) Done() <-chan struct{} {
	return l.done
}

func (l *configManagerLifecycle) Err() error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.runErr
}

// healthCheck stays healthy while the manager is starting. Cache startup can
// legitimately take time, so only readiness is gated during that window.
func (l *configManagerLifecycle) healthCheck(_ *http.Request) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if !l.stopped {
		return nil
	}
	if l.runErr != nil {
		return fmt.Errorf("config manager stopped: %w", l.runErr)
	}
	return errors.New("config manager stopped")
}

func (l *configManagerLifecycle) readyCheck(_ *http.Request) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.ready && !l.stopped {
		return nil
	}
	if l.stopped && l.runErr != nil {
		return fmt.Errorf("config manager stopped: %w", l.runErr)
	}
	if l.stopped {
		return errors.New("config manager stopped")
	}
	return errors.New("config manager cache and controllers have not started")
}

func waitForNetworkManager(ctx context.Context, lifecycle *configManagerLifecycle) error {
	select {
	case <-ctx.Done():
		return nil
	case <-lifecycle.Done():
		// Normal process cancellation also stops the manager. If both signals
		// become observable together, cancellation wins over reporting a false
		// crash during graceful shutdown.
		if ctx.Err() != nil {
			return nil
		}
		if err := lifecycle.Err(); err != nil {
			return fmt.Errorf("network-management controller manager exited: %w", err)
		}
		return errors.New("network-management controller manager exited unexpectedly")
	}
}
