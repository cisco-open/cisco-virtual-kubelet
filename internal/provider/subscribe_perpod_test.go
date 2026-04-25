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

// Wave 6A regression tests for external-review-followup Finding #3:
// the per-pod controller-runtime path must consume Subscribe
// notifications. NotifySubscribeFired records a timestamp; Reconcile
// compares it against cr.Status.LastDeviceCheck to decide between
// triggerEvent (normal CR/scope-object event) and triggerSubscribe
// (bypass hash short-circuit).

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSubscribeFiredSince_NeverFired(t *testing.T) {
	t.Parallel()
	r := &ConfigReconciler{}
	now := metav1.Now()
	if r.subscribeFiredSince(&now) {
		t.Errorf("zero notifyTime must not claim subscribe fired")
	}
}

func TestSubscribeFiredSince_NilLastCheck(t *testing.T) {
	t.Parallel()
	r := &ConfigReconciler{}
	r.NotifySubscribeFired()
	if r.subscribeFiredSince(nil) {
		t.Errorf("nil LastDeviceCheck (first reconcile) must not claim subscribe; trigger should follow normal rules")
	}
}

func TestSubscribeFiredSince_StaleLastCheck(t *testing.T) {
	t.Parallel()
	r := &ConfigReconciler{}
	old := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	r.NotifySubscribeFired()
	if !r.subscribeFiredSince(&old) {
		t.Errorf("notify fired AFTER an old LastDeviceCheck must claim subscribe")
	}
}

func TestSubscribeFiredSince_FreshLastCheck(t *testing.T) {
	t.Parallel()
	r := &ConfigReconciler{}
	r.NotifySubscribeFired()
	// Sleep a tick so the LastDeviceCheck is strictly after notify.
	time.Sleep(2 * time.Millisecond)
	now := metav1.Now()
	if r.subscribeFiredSince(&now) {
		t.Errorf("LastDeviceCheck strictly newer than notify must NOT claim subscribe (already reconciled since)")
	}
}

func TestNotifySubscribeFired_MonotonicNonDecreasing(t *testing.T) {
	t.Parallel()
	r := &ConfigReconciler{}
	r.NotifySubscribeFired()
	first := r.subscribeNotifyTime.Load()
	if first == 0 {
		t.Fatal("expected non-zero notify time after NotifySubscribeFired")
	}
	time.Sleep(2 * time.Millisecond)
	r.NotifySubscribeFired()
	second := r.subscribeNotifyTime.Load()
	if second <= first {
		t.Errorf("expected NotifySubscribeFired to advance the timestamp: first=%d, second=%d", first, second)
	}
}
