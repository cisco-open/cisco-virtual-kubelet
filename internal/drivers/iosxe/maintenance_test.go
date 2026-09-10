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

package iosxe

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

func TestMaintenanceRecoveryHoldsOneLeaseUntilRunning(t *testing.T) {
	state := "DEPLOYED"
	held := false
	acquired, finished, mutations := 0, 0, 0
	driver := &XEDriver{config: &v1alpha1.DeviceSpec{}, client: &fakeNetworkClient{
		getHook: func(_ string, result any) error {
			root := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
			root.App = map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{"app1": makeOperData(state)}
			return nil
		},
		postHook: func(_ string, payload any) error {
			if !held {
				t.Fatal("recovery mutated outside lease")
			}
			request := payload.(map[string]interface{})
			if _, ok := request["activate"]; ok {
				state = "ACTIVATED"
			} else if _, ok := request["start"]; ok {
				state = "RUNNING"
			} else {
				t.Fatalf("unexpected mutation: %v", request)
			}
			mutations++
			return nil
		},
	}}
	driver.SetMaintenanceMutationGuard(func(ctx context.Context) (context.Context, func(error), error) {
		acquired++
		held = true
		return ctx, func(err error) {
			if err != nil || state != "RUNNING" {
				t.Fatalf("released before convergence: state=%s err=%v", state, err)
			}
			finished++
			held = false
		}, nil
	})
	err := driver.withMaintenanceMutation(testCtx(), func(ctx context.Context) error {
		return driver.convergeStatusApp(ctx, newRunningAppConfig("app1"), time.Millisecond)
	})
	if err != nil || acquired != 1 || finished != 1 || mutations != 2 || held {
		t.Fatalf("lifecycle guard=%d/%d mutations=%d held=%v err=%v", acquired, finished, mutations, held, err)
	}
}

func TestMissingContainerRecoveryCannotBypassMaintenance(t *testing.T) {
	var mutations atomic.Int32
	attempted := make(chan struct{}, 1)
	pod := lifecycleTestPod()
	pod.Spec.Containers = pod.Spec.Containers[:1]
	secrets := corev1listers.NewSecretLister(cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{}))
	configs := corev1listers.NewConfigMapLister(cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{}))
	driver := &XEDriver{config: &v1alpha1.DeviceSpec{XE: &v1alpha1.XEConfig{}},
		client:       &fakeNetworkClient{postHook: func(string, any) error { mutations.Add(1); return nil }},
		secretLister: secrets.Secrets(pod.Namespace), configMapLister: configs.ConfigMaps(pod.Namespace),
		installInFlight: map[string]bool{}, recoveringPods: map[string]bool{}}
	driver.SetMaintenanceMutationGuard(func(ctx context.Context) (context.Context, func(error), error) {
		attempted <- struct{}{}
		return ctx, nil, errors.New("software upgrade active")
	})
	driver.recoverMissingContainers(testCtx(), pod, map[string]string{})
	select {
	case <-attempted:
	case <-time.After(time.Second):
		t.Fatal("detached recovery did not acquire maintenance lease")
	}
	if mutations.Load() != 0 {
		t.Fatal("detached recovery bypassed active upgrade")
	}
}

func TestMaintenanceRecoveryPanicRetainsUncertainOutcome(t *testing.T) {
	var outcome error
	driver := &XEDriver{}
	driver.SetMaintenanceMutationGuard(func(ctx context.Context) (context.Context, func(error), error) {
		return ctx, func(err error) { outcome = err }, nil
	})
	defer func() {
		if got := recover(); got != "recovery panic" {
			t.Fatalf("panic was suppressed or replaced: %v", got)
		}
		if !errors.Is(outcome, devicecoordination.ErrMutationIncomplete) {
			t.Fatalf("panic passed successful outcome: %v", outcome)
		}
	}()
	_ = driver.withMaintenanceMutation(context.Background(), func(context.Context) error { panic("recovery panic") })
}
