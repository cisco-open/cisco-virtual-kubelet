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
	"math/rand"
	"sync"
	"time"

	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const (
	// CapabilityInventory is reported once the first complete inventory sync
	// succeeds.
	CapabilityInventory = "inventory"

	defaultInventoryInterval = 5 * time.Minute
	inventoryJitter          = 0.1
)

// inventoryStore holds the last complete snapshot. A failed refresh never
// replaces it.
type inventoryStore struct {
	mu    sync.RWMutex
	items []InventoryItem
	at    time.Time
}

func (s *inventoryStore) set(items []InventoryItem, at time.Time) {
	s.mu.Lock()
	s.items, s.at = append([]InventoryItem(nil), items...), at
	s.mu.Unlock()
}

// Snapshot returns a copy of the last good inventory and when it was taken.
func (s *inventoryStore) Snapshot() ([]InventoryItem, time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]InventoryItem(nil), s.items...), s.at
}

type inventoryRunnable struct{ adapter *adapter }

// NeedLeaderElection is false: each worker owns exactly one endpoint.
func (*inventoryRunnable) NeedLeaderElection() bool { return false }

func (r *inventoryRunnable) Start(ctx context.Context) error {
	a := r.adapter
	a.syncInventory(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(jittered(a.inventoryInterval)):
			a.syncInventory(ctx)
		}
	}
}

func jittered(d time.Duration) time.Duration {
	delta := (rand.Float64()*2 - 1) * inventoryJitter * float64(d) //nolint:gosec // scheduling jitter only
	return d + time.Duration(delta)
}

func (a *adapter) syncInventory(ctx context.Context) {
	log := ctrl.Log.WithName("nexus-dashboard").WithValues("networkController", a.key)
	items, err := a.list(ctx)
	if ctx.Err() != nil {
		return
	}
	msg, ok := "", true
	if err != nil {
		prev, _ := a.inventory.Snapshot()
		ok, msg = false, bound(fmt.Sprintf("inventory refresh failed: %s; keeping last good snapshot of %d switches",
			redactedErr(err), len(prev)))
		log.Info("inventory refresh failed", "error", redactedErr(err))
	} else {
		a.inventory.set(items, a.now())
		msg = summarize(items)
	}
	if perr := a.publishCapability(ctx, CapabilityInventory, ok, msg); perr != nil {
		log.Error(perr, "publish inventory status")
	}
	if err == nil && a.devices.enabled() {
		a.syncDevices(ctx, items)
	}
}

// syncDevices runs only after a complete, successful inventory refresh, so a
// partial or failed listing can never look like a change in the fleet.
func (a *adapter) syncDevices(ctx context.Context, items []InventoryItem) {
	log := ctrl.Log.WithName("nexus-dashboard").WithValues("networkController", a.key)
	res, err := a.devices.Sync(ctx, items)
	if ctx.Err() != nil {
		return
	}
	ok, msg := res.ok() && err == nil, res.String()
	if err != nil {
		ok, msg = false, bound(fmt.Sprintf("device adoption failed: %s", err))
		log.Info("device adoption failed", "error", err.Error())
	}
	if perr := a.publishCapability(ctx, CapabilityDeviceAdoption, ok, bound(msg)); perr != nil {
		log.Error(perr, "publish device-adoption status")
	}
}

func (a *adapter) publishCapability(ctx context.Context, name string, ok bool, msg string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var nc ciskov1.NetworkController
		if err := a.statusWriter.Get(ctx, a.key, &nc); err != nil {
			return err
		}
		before := nc.DeepCopy()
		setCapability(&nc.Status, name, ok, msg)
		if equalStatus(before, &nc) {
			return nil
		}
		return a.statusWriter.Status().Update(ctx, &nc)
	})
}

// summarize reports counts only: no serials, hostnames or addresses.
func summarize(items []InventoryItem) string {
	fabrics := map[string]struct{}{}
	nxos, skipped := 0, 0
	for _, it := range items {
		fabrics[it.Fabric] = struct{}{}
		if it.SkipReason != "" {
			skipped++
		} else {
			nxos++
		}
	}
	return fmt.Sprintf("%d switches in %d fabrics; %d adoptable NX-OS, %d skipped", len(items), len(fabrics), nxos, skipped)
}

func redactedErr(err error) string { return bound(classify(err).message) }
