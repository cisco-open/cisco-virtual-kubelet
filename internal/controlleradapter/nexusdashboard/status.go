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
	"errors"
	"sync"

	"k8s.io/client-go/util/retry"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// errBindingInactive means the NetworkController this worker was started for
// was replaced, paused, deleted or has a newer generation. The worker then
// publishes nothing; the manager rolls it for the new generation.
var errBindingInactive = errors.New("NetworkController binding is no longer active")

type capState struct {
	ok  bool
	msg string
}

// observed holds the latest result of each independent loop. The loops keep
// their own schedules and only update their own piece here; publishStatus is
// the single writer that turns the pieces into status.capabilities.
type observed struct {
	mu        sync.Mutex
	health    *healthResult
	inventory *capState
	adoption  *capState
	version   string // ND product version; last known value is kept
}

func (o *observed) setHealth(r healthResult) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.health = &r
}

func (o *observed) setVersion(v string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.version = v
}

func (o *observed) controllerVersion() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.version
}

func (o *observed) setCapability(name string, ok bool, msg string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := &capState{ok: ok, msg: bound(msg)}
	switch name {
	case CapabilityInventory:
		o.inventory = s
	case CapabilityDeviceAdoption:
		o.adoption = s
	}
}

// capabilities composes the full list, in a fixed order. An entry exists only
// once its loop has reported, so a disabled feature never leaves one behind.
func (o *observed) capabilities() []ciskov1.NetworkControllerCapabilityStatus {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []ciskov1.NetworkControllerCapabilityStatus
	if o.health != nil {
		out = append(out, ciskov1.NetworkControllerCapabilityStatus{Name: CapabilityHealth, Supported: o.health.ready(), Message: bound(o.health.message)})
	}
	for _, c := range []struct {
		name string
		s    *capState
	}{{CapabilityInventory, o.inventory}, {CapabilityDeviceAdoption, o.adoption}} {
		if c.s != nil {
			out = append(out, ciskov1.NetworkControllerCapabilityStatus{Name: c.name, Supported: c.s.ok, Message: c.s.msg})
		}
	}
	return out
}

func (o *observed) healthResult() (healthResult, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.health == nil {
		return healthResult{}, false
	}
	return *o.health, true
}

// stillBound reports whether nc is the object this worker was started for.
// Checks that need a value the worker does not have (tests) are skipped.
func (a *adapter) stillBound(nc *ciskov1.NetworkController) error {
	switch {
	case a.uid != "" && string(nc.UID) != a.uid,
		a.generation != 0 && nc.Generation != a.generation,
		nc.Spec.Paused,
		nc.DeletionTimestamp != nil:
		return errBindingInactive
	}
	return nil
}

// publishStatus writes the composed capabilities and, when withHealth is set,
// the health pass's phase, conditions and timestamps. It is the only code that
// writes status.capabilities, and it replaces the whole list.
func (a *adapter) publishStatus(ctx context.Context, withHealth bool) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var nc ciskov1.NetworkController
		if err := a.statusWriter.Get(ctx, a.key, &nc); err != nil {
			return err
		}
		if err := a.stillBound(&nc); err != nil {
			return err
		}
		before := nc.DeepCopy()
		if res, ok := a.obs.healthResult(); ok && withHealth {
			applyHealth(&nc, res, a.now())
			if v := a.obs.controllerVersion(); v != "" {
				nc.Status.ControllerVersion = v
			}
		}
		nc.Status.Capabilities = a.obs.capabilities()
		if equalStatus(before, &nc) {
			return nil
		}
		return a.statusWriter.Status().Update(ctx, &nc)
	})
}

// publishCapability records one loop's result and republishes the full list.
func (a *adapter) publishCapability(ctx context.Context, name string, ok bool, msg string) error {
	a.obs.setCapability(name, ok, msg)
	return a.publishStatus(ctx, false)
}
