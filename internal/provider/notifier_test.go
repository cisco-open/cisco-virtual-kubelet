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
	"fmt"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/emit"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/state"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

func TestPodNotifierReplaysAppEventToCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "pod-1",
			UID:       types.UID("12345678-1234-1234-1234-123456789abc"),
		},
		Spec: v1.PodSpec{Containers: []v1.Container{{Name: "app"}}},
	}
	appID := common.GetAppHostingName(pod, 0)
	status := v1.PodStatus{Phase: v1.PodRunning}
	driver := &notifierDriver{status: status}
	provider, err := NewAppHostingProvider(ctx,
		&ciskov1.DeviceSpec{},
		nodeutil.ProviderConfig{Pods: podLister(t, pod)},
		driver,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewAppHostingProvider: %v", err)
	}

	seen := make(chan *v1.Pod, 1)
	provider.NotifyPods(ctx, func(pod *v1.Pod) {
		seen <- pod
	})
	if ok := provider.ObserveAppEvent(ctx, state.AppEvent{AppID: appID, State: "RUNNING"}); !ok {
		t.Fatal("ObserveAppEvent returned false")
	}

	select {
	case got := <-seen:
		if got.Status.Phase != v1.PodRunning {
			t.Fatalf("pod phase=%q want %q", got.Status.Phase, v1.PodRunning)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("NotifyPods callback did not fire")
	}
}

func TestPodNotifierOverflowDropsAndSelfMetric(t *testing.T) {
	ctx := context.Background()
	provider, err := NewAppHostingProvider(ctx,
		&ciskov1.DeviceSpec{},
		nodeutil.ProviderConfig{},
		&notifierDriver{},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewAppHostingProvider: %v", err)
	}
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	self := emit.NewSelfMetrics(mp)

	const total = 2000
	for i := 0; i < total; i++ {
		if ok := provider.ObserveAppEvent(ctx, state.AppEvent{AppID: fmt.Sprintf("app-%d", i)}); !ok {
			self.IncNotifierDropped(ctx, "consumer_backpressure")
		}
	}

	wantDrops := int64(total - defaultPodNotifierCapacity)
	if got := provider.DroppedNotifierEvents(); got != wantDrops {
		t.Fatalf("DroppedNotifierEvents()=%d want %d", got, wantDrops)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	got, ok := notifierDroppedMetric(rm)
	if !ok {
		t.Fatal("missing cisco_vk_telemetry_notifier_dropped_total")
	}
	if got != wantDrops {
		t.Fatalf("notifier dropped metric=%d want %d", got, wantDrops)
	}
}

type notifierDriver struct {
	fakeTopologyDriver
	status v1.PodStatus
	calls  int
}

func (d *notifierDriver) GetPodStatus(_ context.Context, pod *v1.Pod) (*v1.Pod, error) {
	d.calls++
	out := pod.DeepCopy()
	out.Status = d.status
	return out, nil
}

// TestPodNotifierPollDrivesCallbackWithoutMDT covers the regression that
// caused PR #116's lab CI to fail: implementing PodNotifier disables upstream
// VK's syncProviderWrapper poll path, so without MDT events firing
// ObserveAppEvent the callback never sees a status update. The runPodNotifier
// loop now ticks every defaultPodPollInterval and calls driver.GetPodStatus
// for every node-local pod, mirroring the upstream poll cadence.
func TestPodNotifierPollDrivesCallbackWithoutMDT(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "lab-ci",
			UID:       types.UID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"),
		},
		Spec: v1.PodSpec{Containers: []v1.Container{{Name: "hello"}}},
	}
	driver := &notifierDriver{status: v1.PodStatus{Phase: v1.PodRunning}}
	provider, err := NewAppHostingProvider(ctx,
		&ciskov1.DeviceSpec{},
		nodeutil.ProviderConfig{Pods: podLister(t, pod)},
		driver,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewAppHostingProvider: %v", err)
	}
	seen := make(chan *v1.Pod, 4)
	provider.NotifyPods(ctx, func(pod *v1.Pod) { seen <- pod })

	// Drive the poll path directly so the test is deterministic — equivalent
	// to one tick of the runPodNotifier ticker, with NO ObserveAppEvent call.
	provider.pollAndNotifyAllPods(ctx)

	select {
	case got := <-seen:
		if got.Status.Phase != v1.PodRunning {
			t.Fatalf("phase=%q want %q", got.Status.Phase, v1.PodRunning)
		}
		if got.Name != "lab-ci" {
			t.Fatalf("pod name=%q want lab-ci", got.Name)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("poll did not push pod through callback")
	}
	if driver.calls != 1 {
		t.Fatalf("driver.GetPodStatus calls=%d want 1", driver.calls)
	}
}

func podLister(t *testing.T, pods ...*v1.Pod) corev1listers.PodLister {
	t.Helper()
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
		cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
	})
	for _, pod := range pods {
		if err := indexer.Add(pod); err != nil {
			t.Fatalf("add pod to indexer: %v", err)
		}
	}
	return corev1listers.NewPodLister(indexer)
}

func notifierDroppedMetric(rm metricdata.ResourceMetrics) (int64, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "cisco_vk_telemetry_notifier_dropped_total" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				if metricAttrString(dp.Attributes, "reason") == "consumer_backpressure" {
					return dp.Value, true
				}
			}
		}
	}
	return 0, false
}

func metricAttrString(attrs attribute.Set, key string) string {
	value, ok := attrs.Value(attribute.Key(key))
	if !ok {
		return ""
	}
	return value.AsString()
}
