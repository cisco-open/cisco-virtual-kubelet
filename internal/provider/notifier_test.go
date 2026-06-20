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
	"sync/atomic"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/emit"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/state"
	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
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
	status             v1.PodStatus
	listPods           []*v1.Pod
	calls              int
	deployCalls        int
	missingUntilDeploy bool
	deleteCalls        int32
	deleteBlock        <-chan struct{}
	deleteDone         chan<- struct{}
}

func (d *notifierDriver) DeployPod(context.Context, *v1.Pod, corev1listers.SecretNamespaceLister, corev1listers.ConfigMapNamespaceLister) error {
	d.deployCalls++
	return nil
}

func (d *notifierDriver) GetPodStatus(_ context.Context, pod *v1.Pod) (*v1.Pod, error) {
	d.calls++
	if d.missingUntilDeploy && d.deployCalls == 0 {
		return nil, errdefs.NotFound(fmt.Sprintf("pod %s/%s not found on device", pod.Namespace, pod.Name))
	}
	out := pod.DeepCopy()
	out.Status = d.status
	return out, nil
}

func (d *notifierDriver) ListPods(context.Context) ([]*v1.Pod, error) {
	return d.listPods, nil
}

func (d *notifierDriver) DeletePod(context.Context, *v1.Pod) error {
	atomic.AddInt32(&d.deleteCalls, 1)
	if d.deleteBlock != nil {
		<-d.deleteBlock
	}
	if d.deleteDone != nil {
		select {
		case d.deleteDone <- struct{}{}:
		default:
		}
	}
	return nil
}

func TestGetPodUsesProviderListForExistence(t *testing.T) {
	ctx := context.Background()
	pod := notifierPod("pending-create", "55555555-5555-5555-5555-555555555555")
	driver := &notifierDriver{status: notifierPodStatus(v1.PodRunning, true)}
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

	if got, err := provider.GetPod(ctx, pod.Namespace, pod.Name); !errdefs.IsNotFound(err) || got != nil {
		t.Fatalf("GetPod before provider create = (%v, %v), want NotFound nil", got, err)
	}
	if driver.calls != 0 {
		t.Fatalf("GetPod used GetPodStatus as existence check %d time(s)", driver.calls)
	}

	devicePod := pod.DeepCopy()
	devicePod.Status = notifierPodStatus(v1.PodRunning, true)
	driver.listPods = []*v1.Pod{devicePod}
	got, err := provider.GetPod(ctx, pod.Namespace, pod.Name)
	if err != nil {
		t.Fatalf("GetPod after provider list match: %v", err)
	}
	if got == nil || got.UID != pod.UID || got.Status.Phase != v1.PodRunning {
		t.Fatalf("GetPod returned %#v, want provider-listed running pod", got)
	}
}

// TestPodNotifierPollDrivesCallbackWithoutMDT covers the regression that
// caused PR #116's lab CI to fail: implementing PodNotifier disables upstream
// VK's syncProviderWrapper poll path, so without MDT events firing
// ObserveAppEvent the callback never sees a status update. The runPodNotifier
// loop ticks on the provider poll interval and calls driver.GetPodStatus
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
	driver := &notifierDriver{status: notifierPodStatus(v1.PodRunning, true)}
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
	provider.SetPodPollIntervalForTest(50 * time.Millisecond)
	seen := make(chan *v1.Pod, 4)
	provider.NotifyPods(ctx, func(pod *v1.Pod) { seen <- pod })

	select {
	case got := <-seen:
		cancel()
		if got.Status.Phase != v1.PodRunning {
			t.Fatalf("phase=%q want %q", got.Status.Phase, v1.PodRunning)
		}
		if got.Name != "lab-ci" {
			t.Fatalf("pod name=%q want lab-ci", got.Name)
		}
		if len(got.Status.ContainerStatuses) != 1 || !got.Status.ContainerStatuses[0].Ready {
			t.Fatalf("container status=%+v, want one ready container", got.Status.ContainerStatuses)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("ticker poll did not push pod through callback")
	}
	if driver.calls == 0 {
		t.Fatal("driver.GetPodStatus was not called")
	}
}

func TestPodNotifierPollReconcilesMissingProviderPod(t *testing.T) {
	ctx := context.Background()
	pod := notifierPod("missed-watch", "99999999-9999-9999-9999-999999999999")
	driver := &notifierDriver{
		status:             notifierPodStatus(v1.PodRunning, true),
		missingUntilDeploy: true,
	}
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

	var seen []*v1.Pod
	setNotifyFuncForTest(provider, func(pod *v1.Pod) {
		seen = append(seen, pod)
	})

	provider.pollAndNotifyAllPods(ctx)

	if driver.deployCalls != 1 {
		t.Fatalf("DeployPod calls=%d, want 1", driver.deployCalls)
	}
	if driver.calls != 2 {
		t.Fatalf("GetPodStatus calls=%d, want notfound then post-deploy status", driver.calls)
	}
	if len(seen) != 1 {
		t.Fatalf("callbacks=%d want 1", len(seen))
	}
	if seen[0].Status.Phase != v1.PodRunning {
		t.Fatalf("phase=%q want %q", seen[0].Status.Phase, v1.PodRunning)
	}
}

func TestPodNotifierPollRecoversDeletingPodOnce(t *testing.T) {
	ctx := context.Background()
	pod := notifierPod("terminating", "aaaaaaaa-1111-2222-3333-aaaaaaaaaaaa")
	now := metav1.Now()
	pod.DeletionTimestamp = &now
	deleteBlock := make(chan struct{})
	deleteDone := make(chan struct{}, 1)
	driver := &notifierDriver{deleteBlock: deleteBlock, deleteDone: deleteDone}
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
	setNotifyFuncForTest(provider, func(*v1.Pod) {})

	provider.pollAndNotifyAllPods(ctx)
	for i := 0; i < 50 && atomic.LoadInt32(&driver.deleteCalls) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&driver.deleteCalls); got != 1 {
		t.Fatalf("DeletePod calls after first poll=%d want 1", got)
	}

	provider.pollAndNotifyAllPods(ctx)
	if got := atomic.LoadInt32(&driver.deleteCalls); got != 1 {
		t.Fatalf("DeletePod calls while recovery in-flight=%d want 1", got)
	}
	close(deleteBlock)
	select {
	case <-deleteDone:
	case <-time.After(time.Second):
		t.Fatal("recovery DeletePod did not finish after unblock")
	}

	provider.pollAndNotifyAllPods(ctx)
	if got := atomic.LoadInt32(&driver.deleteCalls); got != 1 {
		t.Fatalf("DeletePod calls after completed recovery=%d want 1", got)
	}
}

func TestProviderDeleteSuppressesNotifierDeleteRecoveryWhileInFlight(t *testing.T) {
	ctx := context.Background()
	pod := notifierPod("normal-delete", "bbbbbbbb-1111-2222-3333-bbbbbbbbbbbb")
	now := metav1.Now()
	pod.DeletionTimestamp = &now
	deleteBlock := make(chan struct{})
	driver := &notifierDriver{deleteBlock: deleteBlock}
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
	setNotifyFuncForTest(provider, func(*v1.Pod) {})

	done := make(chan error, 1)
	go func() {
		done <- provider.DeletePod(ctx, pod)
	}()
	for i := 0; i < 50 && atomic.LoadInt32(&driver.deleteCalls) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&driver.deleteCalls); got != 1 {
		t.Fatalf("DeletePod calls after normal delete start=%d want 1", got)
	}

	provider.pollAndNotifyAllPods(ctx)
	if got := atomic.LoadInt32(&driver.deleteCalls); got != 1 {
		t.Fatalf("DeletePod calls after recovery poll=%d want normal delete only", got)
	}

	close(deleteBlock)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("DeletePod returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("DeletePod did not return after unblock")
	}

	provider.pollAndNotifyAllPods(ctx)
	if got := atomic.LoadInt32(&driver.deleteCalls); got != 1 {
		t.Fatalf("DeletePod calls after completed normal delete=%d want 1", got)
	}
}

func TestPodNotifierPollSuppressesUnchangedPodStatus(t *testing.T) {
	ctx := context.Background()
	pod := notifierPod("unchanged", "11111111-1111-1111-1111-111111111111")
	driver := &notifierDriver{status: notifierPodStatus(v1.PodRunning, true)}
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

	var seen []*v1.Pod
	setNotifyFuncForTest(provider, func(pod *v1.Pod) {
		seen = append(seen, pod)
	})
	before := podStatusNotificationsSuppressedMetric(t)

	provider.pollAndNotifyAllPods(ctx)
	driver.status.ContainerStatuses[0].State.Running.StartedAt = metav1.Now()
	driver.status.Conditions[0].LastTransitionTime = metav1.Now()
	provider.pollAndNotifyAllPods(ctx)

	if len(seen) != 1 {
		t.Fatalf("callbacks=%d want 1", len(seen))
	}
	if got := podStatusNotificationsSuppressedMetric(t) - before; got != 1 {
		t.Fatalf("suppressed counter delta=%v want 1", got)
	}
}

func TestPodNotifierPollEmitsOnGenuineStateChange(t *testing.T) {
	ctx := context.Background()
	pod := notifierPod("state-change", "22222222-2222-2222-2222-222222222222")
	driver := &notifierDriver{status: notifierPodStatus(v1.PodRunning, true)}
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

	var seen []*v1.Pod
	setNotifyFuncForTest(provider, func(pod *v1.Pod) {
		seen = append(seen, pod)
	})

	provider.pollAndNotifyAllPods(ctx)
	afterFirstPoll := podStatusNotificationsSuppressedMetric(t)
	driver.status = notifierPodStatus(v1.PodFailed, true)
	provider.pollAndNotifyAllPods(ctx)
	afterSecondPoll := podStatusNotificationsSuppressedMetric(t)

	if len(seen) != 2 {
		t.Fatalf("callbacks=%d want 2", len(seen))
	}
	if got := seen[1].Status.Phase; got != v1.PodFailed {
		t.Fatalf("second callback phase=%q want %q", got, v1.PodFailed)
	}
	if afterSecondPoll != afterFirstPoll {
		t.Fatalf("suppressed counter advanced from %v to %v on state change", afterFirstPoll, afterSecondPoll)
	}
}

func TestPodNotifierGCDropsDeletedPodFingerprints(t *testing.T) {
	ctx := context.Background()
	podA := notifierPod("pod-a", "33333333-3333-3333-3333-333333333333")
	podB := notifierPod("pod-b", "44444444-4444-4444-4444-444444444444")
	lister, indexer := podListerWithIndexer(t, podA, podB)
	driver := &notifierDriver{status: notifierPodStatus(v1.PodRunning, true)}
	provider, err := NewAppHostingProvider(ctx,
		&ciskov1.DeviceSpec{},
		nodeutil.ProviderConfig{Pods: lister},
		driver,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewAppHostingProvider: %v", err)
	}

	var seen []*v1.Pod
	setNotifyFuncForTest(provider, func(pod *v1.Pod) {
		seen = append(seen, pod)
	})

	provider.pollAndNotifyAllPods(ctx)
	if len(seen) != 2 {
		t.Fatalf("callbacks after first poll=%d want 2", len(seen))
	}
	provider.pollAndNotifyAllPods(ctx)
	if len(seen) != 2 {
		t.Fatalf("callbacks after unchanged second poll=%d want 2", len(seen))
	}

	if err := indexer.Delete(podA); err != nil {
		t.Fatalf("delete pod from indexer: %v", err)
	}
	driver.status = notifierPodStatus(v1.PodFailed, true)
	provider.pollAndNotifyAllPods(ctx)

	if len(seen) != 3 {
		t.Fatalf("callbacks after deleting one pod and changing state=%d want 3", len(seen))
	}
	if got := seen[2].Name; got != "pod-b" {
		t.Fatalf("third poll notified pod %q want pod-b", got)
	}
	provider.lastNotifiedMu.Lock()
	cacheLen := len(provider.lastNotified)
	provider.lastNotifiedMu.Unlock()
	if cacheLen != 1 {
		t.Fatalf("lastNotified entries=%d want 1", cacheLen)
	}
}

func podLister(t *testing.T, pods ...*v1.Pod) corev1listers.PodLister {
	t.Helper()
	lister, _ := podListerWithIndexer(t, pods...)
	return lister
}

func podListerWithIndexer(t *testing.T, pods ...*v1.Pod) (corev1listers.PodLister, cache.Indexer) {
	t.Helper()
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
		cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
	})
	for _, pod := range pods {
		if err := indexer.Add(pod); err != nil {
			t.Fatalf("add pod to indexer: %v", err)
		}
	}
	return corev1listers.NewPodLister(indexer), indexer
}

func notifierPod(name, uid string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      name,
			UID:       types.UID(uid),
		},
		Spec: v1.PodSpec{Containers: []v1.Container{{Name: "app"}}},
	}
}

func notifierPodStatus(phase v1.PodPhase, ready bool) v1.PodStatus {
	started := true
	return v1.PodStatus{
		Phase: phase,
		ContainerStatuses: []v1.ContainerStatus{{
			Name:         "app",
			Ready:        ready,
			Started:      &started,
			RestartCount: 1,
			State: v1.ContainerState{
				Running: &v1.ContainerStateRunning{StartedAt: metav1.Now()},
			},
		}},
		Conditions: []v1.PodCondition{{
			Type:               v1.PodReady,
			Status:             v1.ConditionTrue,
			Reason:             "Ready",
			LastTransitionTime: metav1.Now(),
		}},
		PodIP:  "10.0.0.1",
		PodIPs: []v1.PodIP{{IP: "10.0.0.1"}},
	}
}

func setNotifyFuncForTest(provider *AppHostingProvider, cb func(*v1.Pod)) {
	provider.notifyMu.Lock()
	provider.notifyFn = cb
	provider.notifyMu.Unlock()
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

func podStatusNotificationsSuppressedMetric(t *testing.T) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather prometheus metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "cisco_vk_pod_status_notifications_suppressed_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			if prometheusLabelValue(metric.GetLabel(), "reason") != "unchanged" {
				continue
			}
			if metric.GetCounter() == nil {
				return 0
			}
			return metric.GetCounter().GetValue()
		}
	}
	return 0
}

func prometheusLabelValue(labels []*io_prometheus_client.LabelPair, name string) string {
	for _, label := range labels {
		if label.GetName() == name {
			return label.GetValue()
		}
	}
	return ""
}

func metricAttrString(attrs attribute.Set, key string) string {
	value, ok := attrs.Value(attribute.Key(key))
	if !ok {
		return ""
	}
	return value.AsString()
}
