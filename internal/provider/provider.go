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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/emit"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/state"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	oteltrace "go.opentelemetry.io/otel/trace"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/record"
	statsv1alpha1 "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
)

const defaultPodNotifierCapacity = 1024
const podStatusNotificationSuppressedReasonUnchanged = "unchanged"

type AppHostingProvider struct {
	ctx             context.Context
	deviceSpec      *v1alpha1.DeviceSpec
	driver          drivers.CiscoKubernetesDeviceDriver
	podsLister      corev1listers.PodLister
	configMapLister corev1listers.ConfigMapLister
	secretLister    corev1listers.SecretLister
	serviceLister   corev1listers.ServiceLister
	nodeProvider    *AppHostingNode

	notifyMu        sync.Mutex
	notifyFn        func(*v1.Pod)
	notifyQueue     chan state.AppEvent
	notifyStarted   bool
	notifyDropped   int64
	traceDevice     string
	traceCache      *correlation.Cache
	podPollInterval time.Duration

	lastNotifiedMu sync.Mutex
	lastNotified   map[types.UID]string
}

func NewAppHostingProvider(
	ctx context.Context,
	deviceSpec *v1alpha1.DeviceSpec,
	vkCfg nodeutil.ProviderConfig,
	driver drivers.CiscoKubernetesDeviceDriver,
	nodeProvider *AppHostingNode,
	eventRecorder record.EventRecorder,
) (*AppHostingProvider, error) {
	// Wire the event recorder into the driver if it supports it.
	if eventRecorder != nil {
		type eventRecorderSetter interface {
			SetEventRecorder(record.EventRecorder)
		}
		if setter, ok := driver.(eventRecorderSetter); ok {
			setter.SetEventRecorder(eventRecorder)
		}
	}
	return &AppHostingProvider{
		ctx:             ctx,
		deviceSpec:      deviceSpec,
		driver:          driver,
		podsLister:      vkCfg.Pods,
		configMapLister: vkCfg.ConfigMaps,
		secretLister:    vkCfg.Secrets,
		serviceLister:   vkCfg.Services,
		nodeProvider:    nodeProvider,
		notifyQueue:     make(chan state.AppEvent, defaultPodNotifierCapacity),
		podPollInterval: defaultPodPollInterval,
		lastNotified:    make(map[types.UID]string),
	}, nil
}

// SetTraceCorrelation attaches the per-process pod/app trace correlation cache.
// CreatePod records the active VK span context by generated app ID; MDT recovery
// spans can then parent to the original admission trace while the cache entry is
// fresh.
func (p *AppHostingProvider) SetTraceCorrelation(deviceName string, cache *correlation.Cache) {
	if p == nil {
		return
	}
	p.notifyMu.Lock()
	p.traceDevice = deviceName
	p.traceCache = cache
	p.notifyMu.Unlock()
}

func (p *AppHostingProvider) GetCapacity(ctx context.Context) (v1.ResourceList, error) {
	resources, err := p.driver.GetDeviceResources(p.ctx)
	return *resources, err
}

// NotifyPods implements node.PodNotifier. Registration is intentionally cheap:
// the callback is stored and a single background consumer drains app-hosting
// state events at the callback's pace. The producer side never blocks telemetry
// goroutines; overflow is surfaced via DroppedNotifierEvents.
func (p *AppHostingProvider) NotifyPods(ctx context.Context, notify func(*v1.Pod)) {
	p.notifyMu.Lock()
	p.notifyFn = notify
	if !p.notifyStarted {
		p.notifyStarted = true
		go p.runPodNotifier(ctx)
	}
	p.notifyMu.Unlock()
}

// ObserveAppEvent receives MDT app-hosting state transitions from the telemetry
// subscriber. It is the producer half of the non-blocking PodNotifier bridge.
func (p *AppHostingProvider) ObserveAppEvent(_ context.Context, event state.AppEvent) bool {
	if p == nil || event.AppID == "" {
		return true
	}
	select {
	case p.notifyQueue <- event:
		return true
	default:
		p.notifyMu.Lock()
		p.notifyDropped++
		p.notifyMu.Unlock()
		return false
	}
}

// DroppedNotifierEvents returns the cumulative count of app-hosting events
// dropped before they could be queued to the PodNotifier consumer.
func (p *AppHostingProvider) DroppedNotifierEvents() int64 {
	if p == nil {
		return 0
	}
	p.notifyMu.Lock()
	defer p.notifyMu.Unlock()
	return p.notifyDropped
}

// defaultPodPollInterval matches upstream VK's syncProviderWrapper cadence
// (5s). Implementing PodNotifier disables upstream's poll fallback, so we run
// our own poll alongside the push-based MDT notifications. Without this,
// deployments without an IOSXETelemetry CR (e.g. lab CI scenarios that don't
// configure MDT) would never see pod status flip from Pending — driver state
// changes would only reach the upstream pod controller via MDT events that
// never arrive.
const defaultPodPollInterval = 5 * time.Second

// SetPodPollIntervalForTest overrides the PodNotifier poll cadence in tests.
func (p *AppHostingProvider) SetPodPollIntervalForTest(d time.Duration) {
	if p == nil {
		return
	}
	if d <= 0 {
		d = defaultPodPollInterval
	}
	p.notifyMu.Lock()
	p.podPollInterval = d
	p.notifyMu.Unlock()
}

func (p *AppHostingProvider) currentPodPollInterval() time.Duration {
	if p == nil {
		return defaultPodPollInterval
	}
	p.notifyMu.Lock()
	defer p.notifyMu.Unlock()
	if p.podPollInterval <= 0 {
		return defaultPodPollInterval
	}
	return p.podPollInterval
}

func (p *AppHostingProvider) runPodNotifier(ctx context.Context) {
	ticker := time.NewTicker(p.currentPodPollInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-p.notifyQueue:
			p.notifyPodForAppEvent(ctx, ev)
		case <-ticker.C:
			p.pollAndNotifyAllPods(ctx)
		}
	}
}

// pollAndNotifyAllPods fetches live status for every pod the per-device VK
// pod's lister knows about and pushes each through the registered notifyFn.
// Mirrors the upstream syncProviderWrapper.syncPodStatuses path that
// PodNotifier-implementing providers opt out of.
func (p *AppHostingProvider) pollAndNotifyAllPods(ctx context.Context) {
	cb := p.currentNotifyFunc()
	if cb == nil || p.podsLister == nil || p.driver == nil {
		return
	}
	pods, err := p.podsLister.List(labels.Everything())
	if err != nil {
		log.G(ctx).WithError(err).Debug("PodNotifier poll: list pods")
		return
	}
	currentUIDs := make(map[types.UID]struct{}, len(pods))
	for _, pod := range pods {
		if pod != nil {
			currentUIDs[pod.UID] = struct{}{}
		}
	}
	p.gcLastNotified(currentUIDs)

	for _, pod := range pods {
		if pod == nil || pod.DeletionTimestamp != nil {
			continue
		}
		statusPod, err := p.driver.GetPodStatus(ctx, pod)
		if err != nil {
			log.G(ctx).WithError(err).WithFields(log.Fields{
				"pod":       pod.Name,
				"namespace": pod.Namespace,
			}).Debug("PodNotifier poll: status refresh skipped")
			continue
		}
		if p.shouldNotifyPodStatus(statusPod) {
			cb(statusPod.DeepCopy())
		}
	}
}

func (p *AppHostingProvider) notifyPodForAppEvent(ctx context.Context, ev state.AppEvent) {
	cb := p.currentNotifyFunc()
	if cb == nil || ev.AppID == "" || p.podsLister == nil || p.driver == nil {
		return
	}
	pod := p.findPodByAppID(ctx, ev.AppID)
	if pod == nil {
		return
	}
	statusPod, err := p.driver.GetPodStatus(ctx, pod)
	if err != nil {
		log.G(ctx).WithError(err).WithFields(log.Fields{
			"appID":     ev.AppID,
			"pod":       pod.Name,
			"namespace": pod.Namespace,
		}).Debug("PodNotifier: status refresh skipped")
		return
	}
	if p.shouldNotifyPodStatus(statusPod) {
		cb(statusPod.DeepCopy())
	}
}

func (p *AppHostingProvider) shouldNotifyPodStatus(pod *v1.Pod) bool {
	if p == nil || pod == nil {
		return false
	}
	fingerprint := podStateFingerprint(pod)
	p.lastNotifiedMu.Lock()
	if p.lastNotified == nil {
		p.lastNotified = make(map[types.UID]string)
	}
	prev, ok := p.lastNotified[pod.UID]
	if ok && prev == fingerprint {
		p.lastNotifiedMu.Unlock()
		emit.IncPodStatusNotificationSuppressed(podStatusNotificationSuppressedReasonUnchanged)
		return false
	}
	p.lastNotified[pod.UID] = fingerprint
	p.lastNotifiedMu.Unlock()
	return true
}

func (p *AppHostingProvider) gcLastNotified(currentUIDs map[types.UID]struct{}) {
	if p == nil {
		return
	}
	p.lastNotifiedMu.Lock()
	defer p.lastNotifiedMu.Unlock()
	for uid := range p.lastNotified {
		if _, ok := currentUIDs[uid]; !ok {
			delete(p.lastNotified, uid)
		}
	}
}

func (p *AppHostingProvider) forgetLastNotified(uid types.UID) {
	if p == nil {
		return
	}
	p.lastNotifiedMu.Lock()
	delete(p.lastNotified, uid)
	p.lastNotifiedMu.Unlock()
}

func podStateFingerprint(pod *v1.Pod) string {
	if pod == nil {
		return ""
	}

	type stableContainerStatus struct {
		Name         string `json:"name"`
		Ready        bool   `json:"ready"`
		Started      *bool  `json:"started"`
		RestartCount int32  `json:"restartCount"`
		State        string `json:"state"`
		StateReason  string `json:"stateReason"`
	}
	type stablePodCondition struct {
		Type   v1.PodConditionType `json:"type"`
		Status v1.ConditionStatus  `json:"status"`
		Reason string              `json:"reason"`
	}

	containers := make([]stableContainerStatus, 0, len(pod.Status.ContainerStatuses))
	for _, status := range pod.Status.ContainerStatuses {
		state := "None"
		reason := ""
		switch {
		case status.State.Running != nil:
			state = "Running"
		case status.State.Waiting != nil:
			state = "Waiting"
			reason = status.State.Waiting.Reason
		case status.State.Terminated != nil:
			state = "Terminated"
			reason = status.State.Terminated.Reason
		}
		var started *bool
		if status.Started != nil {
			value := *status.Started
			started = &value
		}
		containers = append(containers, stableContainerStatus{
			Name:         status.Name,
			Ready:        status.Ready,
			Started:      started,
			RestartCount: status.RestartCount,
			State:        state,
			StateReason:  reason,
		})
	}
	sort.Slice(containers, func(i, j int) bool {
		return containers[i].Name < containers[j].Name
	})

	conditions := make([]stablePodCondition, 0, len(pod.Status.Conditions))
	for _, condition := range pod.Status.Conditions {
		conditions = append(conditions, stablePodCondition{
			Type:   condition.Type,
			Status: condition.Status,
			Reason: condition.Reason,
		})
	}
	sort.Slice(conditions, func(i, j int) bool {
		return conditions[i].Type < conditions[j].Type
	})

	podIPs := make([]string, 0, len(pod.Status.PodIPs))
	for _, ip := range pod.Status.PodIPs {
		podIPs = append(podIPs, ip.IP)
	}

	payload := struct {
		Phase             v1.PodPhase             `json:"phase"`
		Reason            string                  `json:"reason"`
		Message           string                  `json:"message"`
		ContainerStatuses []stableContainerStatus `json:"containerStatuses"`
		Conditions        []stablePodCondition    `json:"conditions"`
		PodIP             string                  `json:"podIP"`
		PodIPs            []string                `json:"podIPs"`
	}{
		Phase:             pod.Status.Phase,
		Reason:            pod.Status.Reason,
		Message:           pod.Status.Message,
		ContainerStatuses: containers,
		Conditions:        conditions,
		PodIP:             pod.Status.PodIP,
		PodIPs:            podIPs,
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (p *AppHostingProvider) currentNotifyFunc() func(*v1.Pod) {
	p.notifyMu.Lock()
	defer p.notifyMu.Unlock()
	return p.notifyFn
}

func (p *AppHostingProvider) findPodByAppID(ctx context.Context, appID string) *v1.Pod {
	_, cleanUID, ok := common.ParseCVKAppName(appID)
	if !ok || cleanUID == "" {
		return nil
	}
	pods, err := p.podsLister.List(labels.Everything())
	if err != nil {
		log.G(ctx).WithError(err).Debug("PodNotifier: list pods")
		return nil
	}
	for _, pod := range pods {
		if pod.DeletionTimestamp != nil {
			continue
		}
		if strings.ReplaceAll(string(pod.UID), "-", "") == cleanUID {
			return pod.DeepCopy()
		}
	}
	return nil
}

func (p *AppHostingProvider) CreatePod(ctx context.Context, pod *v1.Pod) error {
	p.rememberPodTrace(ctx, pod)
	// Deploy the container. This MUST be idempotent
	// In future we can range over the pod.spec.containers
	if err := p.driver.DeployPod(p.ctx, pod, p.secretLister.Secrets(pod.Namespace), p.configMapLister.ConfigMaps(pod.Namespace)); err != nil {
		return errdefs.AsInvalidInput(err)
	}

	// Trigger node status update to reflect potentially changed resources
	if p.nodeProvider != nil {
		p.nodeProvider.ForceStatusUpdate(p.ctx)
	}

	return nil
}

func (p *AppHostingProvider) rememberPodTrace(ctx context.Context, pod *v1.Pod) {
	if p == nil || pod == nil {
		return
	}
	sc := oteltrace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return
	}
	p.notifyMu.Lock()
	device := p.traceDevice
	cache := p.traceCache
	p.notifyMu.Unlock()
	if cache == nil || device == "" {
		return
	}
	for _, appID := range common.GenerateContainerAppIDs(pod) {
		cache.Upsert(device, appID, sc)
	}
}

func (p *AppHostingProvider) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	// IOS-XE/XR may have limited "Update" support (e.g., changing resources requires a restart)
	return p.driver.UpdatePod(p.ctx, pod)
}

func (p *AppHostingProvider) DeletePod(ctx context.Context, pod *v1.Pod) error {
	err := p.driver.DeletePod(p.ctx, pod)
	if pod != nil {
		p.forgetLastNotified(pod.UID)
	}

	// Trigger node status update to reflect potentially freed resources
	if p.nodeProvider != nil {
		p.nodeProvider.ForceStatusUpdate(p.ctx)
	}

	return err
}

func (p *AppHostingProvider) GetPod(ctx context.Context, namespace, name string) (*v1.Pod, error) {

	log.G(p.ctx).WithFields(log.Fields{
		"name":      name,
		"namespace": namespace,
	}).Debug("Running GetPod:")

	// Fast path: fetch pod spec from informer cache (desired state)
	pod, err := p.podsLister.Pods(namespace).Get(name)
	if err == nil {
		devicePods, listErr := p.driver.ListPods(p.ctx)
		if listErr != nil {
			log.G(p.ctx).WithError(listErr).WithFields(log.Fields{
				"name":      name,
				"namespace": namespace,
			}).Debug("GetPod: failed to list device pods while checking provider existence")
			return nil, errdefs.NotFound(fmt.Sprintf("pod %s/%s not found on device", namespace, name))
		}
		for _, devicePod := range devicePods {
			if devicePod == nil || devicePod.Namespace != namespace || devicePod.Name != name {
				continue
			}
			if devicePod.UID != "" && pod.UID != "" && devicePod.UID != pod.UID {
				continue
			}
			return devicePod.DeepCopy(), nil
		}
		return nil, errdefs.NotFound(fmt.Sprintf("pod %s/%s not found on device", namespace, name))
	}

	// Pod not in K8s — check if it still exists on the device.
	// This is the delete path: the upstream VK pod controller calls GetPod
	// after a pod is removed from the API server to determine whether the
	// provider still needs to clean up the app on the device (see
	// syncPodFromKubernetesHandler in podcontroller.go). Only CVK-managed
	// apps (those with RunOpts labels) are discovered by ListPods, so
	// ad-hoc admin-deployed apps are never affected.
	log.G(p.ctx).WithFields(log.Fields{
		"name":      name,
		"namespace": namespace,
	}).Debug("GetPod: pod not in K8s lister, checking device")

	devicePods, listErr := p.driver.ListPods(p.ctx)
	if listErr != nil {
		log.G(p.ctx).WithError(listErr).Warn("GetPod: failed to list pods from device")
		return nil, errdefs.NotFound(fmt.Sprintf("pod %s/%s not found", namespace, name))
	}
	for _, dp := range devicePods {
		if dp.Namespace == namespace && dp.Name == name {
			return dp, nil
		}
	}

	return nil, errdefs.NotFound(fmt.Sprintf("pod %s/%s not found on device", namespace, name))
}

func (p *AppHostingProvider) GetPodStatus(ctx context.Context, namespace, name string) (*v1.PodStatus, error) {

	log.G(p.ctx).WithFields(log.Fields{
		"name":      name,
		"namespace": namespace,
	}).Debug("Calling driver GetPodStatus:")

	// Fetch pod spec from informer cache (desired state)
	pod, err := p.podsLister.Pods(namespace).Get(name)
	if err != nil {
		return nil, errdefs.NotFound(fmt.Sprintf("pod %s/%s not found: %v", namespace, name, err))
	}

	// Get actual status from Cisco device
	statusPod, err := p.driver.GetPodStatus(p.ctx, pod)
	if err != nil {
		return nil, errdefs.AsNotFound(err)
	}

	return &statusPod.Status, nil
}

func (p *AppHostingProvider) GetPods(ctx context.Context) ([]*v1.Pod, error) {
	pods, err := p.driver.ListPods(p.ctx)
	if err != nil {
		return nil, errdefs.AsNotFound(err)
	}

	return pods, nil
}

func (p *AppHostingProvider) AttachToContainer(ctx context.Context, namespace, podName, containerName string, attach api.AttachIO) error {
	return fmt.Errorf("AttachToContainer is not supported by the Cisco Virtual Kubelet")
}

// NOT YET IMPLEMENTED

// GetContainerLogs implements nodeutil.Provider.
func (p *AppHostingProvider) GetContainerLogs(ctx context.Context, namespace string, podName string, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	return nil, fmt.Errorf("GetContainerLogs is not supported by the Cisco Virtual Kubelet")
}

// GetMetricsResource implements nodeutil.Provider.
func (p *AppHostingProvider) GetMetricsResource(ctx context.Context) ([]*io_prometheus_client.MetricFamily, error) {
	return p.buildMetricsResource(ctx)
}

// GetStatsSummary implements nodeutil.Provider.
func (p *AppHostingProvider) GetStatsSummary(ctx context.Context) (*statsv1alpha1.Summary, error) {
	return p.buildStatsSummary(ctx)
}

// PortForward implements nodeutil.Provider.
func (p *AppHostingProvider) PortForward(ctx context.Context, namespace string, pod string, port int32, stream io.ReadWriteCloser) error {
	return fmt.Errorf("PortForward is not supported by the Cisco Virtual Kubelet")
}

// RunInContainer implements nodeutil.Provider.
func (p *AppHostingProvider) RunInContainer(ctx context.Context, namespace string, podName string, containerName string, cmd []string, attach api.AttachIO) error {
	return fmt.Errorf("RunInContainer is not supported by the Cisco Virtual Kubelet")
}

// AppHostingNode implements node.NodeProvider for proper heartbeat management.
// This follows the NaiveNodeProvider pattern from virtual-kubelet.
// The library's NodeController handles periodic heartbeat updates automatically.
type AppHostingNode struct {
	ctx             context.Context // long-lived app context for async operations
	nodeName        string
	deviceSpec      *v1alpha1.DeviceSpec
	driver          drivers.CiscoKubernetesDeviceDriver
	statusCallback  func(*v1.Node)
	lastStatusSync  time.Time
	syncInFlight    bool
	statusSyncMutex sync.Mutex
	// Track previous condition statuses for correct LastTransitionTime handling
	prevReadyStatus            v1.ConditionStatus
	prevDiskPressureStatus     v1.ConditionStatus
	readyTransitionTime        metav1.Time
	diskPressureTransitionTime metav1.Time
}

// NewAppHostingNode creates a new AppHostingNode.
// The provided ctx should be the long-lived application context, not a request-scoped one.
func NewAppHostingNode(
	ctx context.Context,
	nodeName string,
	deviceSpec *v1alpha1.DeviceSpec,
	driver drivers.CiscoKubernetesDeviceDriver,
) *AppHostingNode {
	return &AppHostingNode{
		ctx:        ctx,
		nodeName:   nodeName,
		deviceSpec: deviceSpec,
		driver:     driver,
	}
}

// Ping implements node.NodeProvider.
// Called periodically by the library's nodePingController.
// Returning nil indicates the node is healthy.
// Note: Ping reports process-level health, not device reachability.
// Device health is surfaced via NodeReady conditions in syncNodeStatus.
func (a *AppHostingNode) Ping(ctx context.Context) error {
	a.statusSyncMutex.Lock()
	defer a.statusSyncMutex.Unlock()

	// Throttle: only sync if >30s since last attempt and no sync already in-flight
	if !a.syncInFlight && time.Since(a.lastStatusSync) > 30*time.Second {
		if a.statusCallback != nil {
			a.syncInFlight = true
			go a.syncNodeStatus(a.ctx, a.statusCallback)
			a.lastStatusSync = time.Now()
		}
	}
	return nil
}

// NotifyNodeStatus implements node.NodeProvider.
// Called once at startup to allow async node status updates.
// We use this to update node info with device details and monitor operational status.
func (a *AppHostingNode) NotifyNodeStatus(ctx context.Context, cb func(*v1.Node)) {
	if a.deviceSpec == nil {
		return
	}

	a.statusSyncMutex.Lock()
	a.statusCallback = cb
	a.statusSyncMutex.Unlock()

	// Perform initial sync immediately using the long-lived app context,
	// not the NotifyNodeStatus ctx which may be short-lived.
	go a.syncNodeStatus(a.ctx, cb)
}

// ForceStatusUpdate triggers an immediate status update if a callback is registered.
// Skipped if a sync is already in-flight to avoid redundant device queries.
func (a *AppHostingNode) ForceStatusUpdate(ctx context.Context) {
	a.statusSyncMutex.Lock()
	cb := a.statusCallback
	inFlight := a.syncInFlight
	a.statusSyncMutex.Unlock()

	if cb != nil && !inFlight {
		log.G(a.ctx).Info("Forcing node status update due to pod lifecycle event")
		a.statusSyncMutex.Lock()
		a.syncInFlight = true
		a.statusSyncMutex.Unlock()
		go a.syncNodeStatus(a.ctx, cb)
	}
}

// syncNodeStatus fetches the latest device info and operational data, then calls the callback.
func (a *AppHostingNode) syncNodeStatus(ctx context.Context, cb func(*v1.Node)) {
	// Always clear syncInFlight when we're done
	defer func() {
		a.statusSyncMutex.Lock()
		a.syncInFlight = false
		a.statusSyncMutex.Unlock()
	}()

	// Record time of attempt
	a.statusSyncMutex.Lock()
	a.lastStatusSync = time.Now()
	a.statusSyncMutex.Unlock()

	deviceInfo, err := a.driver.GetDeviceInfo(ctx)
	if err != nil || deviceInfo == nil {
		log.G(ctx).Warn("Failed to get device info during node status sync")
		return // Skip update if we can't identify the device
	}

	// Fetch dynamic operational data (IOx status, resources)
	operData, err := a.driver.GetGlobalOperationalData(ctx)
	if err != nil {
		log.G(ctx).WithError(err).Warn("Failed to get operational data during node status sync")
		// We continue with basic device info, but conditions may be incomplete
	}

	// Determine node internal IP from device address
	nodeInternalIP := a.deviceSpec.Address

	log.G(ctx).Debugf("Updating node status with device info, InternalIP=%s", nodeInternalIP)

	now := metav1.Now()

	// --- Build Node Conditions with correct LastTransitionTime tracking ---
	conditions := []v1.NodeCondition{}

	// Condition: Ready
	newReadyStatus := v1.ConditionTrue
	readyReason := "KubeletReady"
	readyMessage := "Cisco IOx is enabled and reachable"

	if operData != nil && !operData.IoxEnabled {
		newReadyStatus = v1.ConditionFalse
		readyReason = "IOxDisabled"
		readyMessage = "IOx hosting is disabled on device"
	}

	a.statusSyncMutex.Lock()
	if a.prevReadyStatus == "" || a.prevReadyStatus != newReadyStatus {
		// First report or actual transition — update transition time
		a.readyTransitionTime = now
		a.prevReadyStatus = newReadyStatus
	}
	readyTransitionTime := a.readyTransitionTime
	a.statusSyncMutex.Unlock()

	conditions = append(conditions, v1.NodeCondition{
		Type:               v1.NodeReady,
		Status:             newReadyStatus,
		LastHeartbeatTime:  now,
		LastTransitionTime: readyTransitionTime,
		Reason:             readyReason,
		Message:            readyMessage,
	})

	// Condition: DiskPressure (IOx Storage)
	newDiskPressureStatus := v1.ConditionFalse
	diskReason := "StorageAvailable"
	diskMessage := "Sufficient storage available"

	if operData != nil && operData.Storage.Quota > 0 {
		if float64(operData.Storage.Available)/float64(operData.Storage.Quota) < 0.05 {
			newDiskPressureStatus = v1.ConditionTrue
			diskReason = "StorageLow"
			diskMessage = fmt.Sprintf("Available storage low: %d/%d %s", operData.Storage.Available, operData.Storage.Quota, operData.Storage.Unit)
		}
	}

	a.statusSyncMutex.Lock()
	if a.prevDiskPressureStatus == "" || a.prevDiskPressureStatus != newDiskPressureStatus {
		a.diskPressureTransitionTime = now
		a.prevDiskPressureStatus = newDiskPressureStatus
	}
	diskPressureTransitionTime := a.diskPressureTransitionTime
	a.statusSyncMutex.Unlock()

	conditions = append(conditions, v1.NodeCondition{
		Type:               v1.NodeDiskPressure,
		Status:             newDiskPressureStatus,
		LastHeartbeatTime:  now,
		LastTransitionTime: diskPressureTransitionTime,
		Reason:             diskReason,
		Message:            diskMessage,
	})

	// --- Build dynamic Capacity and Allocatable from operational data ---
	capacity := v1.ResourceList{}
	if operData != nil {
		if operData.SystemCPU.Quota > 0 {
			capacity[v1.ResourceCPU] = *resource.NewQuantity(operData.SystemCPU.Quota, resource.DecimalSI)
		}
		if operData.Memory.Quota > 0 {
			// Memory quota is in MB from the device; convert to bytes for Kubernetes
			capacity[v1.ResourceMemory] = *resource.NewQuantity(operData.Memory.Quota*1024*1024, resource.BinarySI)
		}
		if operData.Storage.Quota > 0 {
			capacity[v1.ResourceStorage] = *resource.NewQuantity(operData.Storage.Quota*1024*1024, resource.BinarySI)
		}
	}

	// Discover deployed pods to calculate available pod slots
	var maxPods int64 = 16
	var deployedPodCount int64
	pods, podErr := a.driver.ListPods(ctx)
	if podErr != nil {
		log.G(ctx).WithError(podErr).Warn("Failed to list pods during node status sync, using 0 for deployed count")
	} else {
		deployedPodCount = int64(len(pods))
	}
	capacity[v1.ResourcePods] = *resource.NewQuantity(maxPods, resource.DecimalSI)

	// Allocatable reflects currently available resources
	allocatable := v1.ResourceList{}
	if operData != nil {
		if operData.SystemCPU.Available > 0 {
			allocatable[v1.ResourceCPU] = *resource.NewQuantity(operData.SystemCPU.Available, resource.DecimalSI)
		}
		if operData.Memory.Available > 0 {
			allocatable[v1.ResourceMemory] = *resource.NewQuantity(operData.Memory.Available*1024*1024, resource.BinarySI)
		}
		if operData.Storage.Available > 0 {
			allocatable[v1.ResourceStorage] = *resource.NewQuantity(operData.Storage.Available*1024*1024, resource.BinarySI)
		}
	}
	availablePods := maxPods - deployedPodCount
	if availablePods < 0 {
		availablePods = 0
	}
	allocatable[v1.ResourcePods] = *resource.NewQuantity(availablePods, resource.DecimalSI)

	// --- Fetch topology data for node annotations ---
	annotations := map[string]string{}

	if deviceInfo.RouterID != "" {
		annotations["cisco.io/router-id"] = deviceInfo.RouterID
	}
	if deviceInfo.Hostname != "" {
		annotations["cisco.io/hostname"] = deviceInfo.Hostname
	}

	// Topology neighbor counts (only if driver supports TopologyProvider)
	if topo, ok := a.driver.(drivers.TopologyProvider); ok {
		var activeProtocols []string

		cdpNeighbors, cdpErr := topo.GetCDPNeighbors(ctx)
		if cdpErr != nil {
			log.G(ctx).WithError(cdpErr).Debug("Failed to fetch CDP neighbors during node status sync")
		} else if len(cdpNeighbors) > 0 {
			activeProtocols = append(activeProtocols, "cdp")
			annotations["cisco.io/cdp-neighbor-count"] = fmt.Sprintf("%d", len(cdpNeighbors))
		}

		ospfNeighbors, ospfErr := topo.GetOSPFNeighbors(ctx)
		if ospfErr != nil {
			log.G(ctx).WithError(ospfErr).Debug("Failed to fetch OSPF neighbors during node status sync")
		} else if len(ospfNeighbors) > 0 {
			activeProtocols = append(activeProtocols, "ospf")
			annotations["cisco.io/ospf-neighbor-count"] = fmt.Sprintf("%d", len(ospfNeighbors))
		}

		// Re-read router ID after OSPF query may have populated it
		if deviceInfo.RouterID != "" {
			annotations["cisco.io/router-id"] = deviceInfo.RouterID
		}

		if len(activeProtocols) > 0 {
			annotations["cisco.io/protocols"] = strings.Join(activeProtocols, ",")
		}
	}

	nodePlatform := nodePlatformMetadata(a.deviceSpec.Driver)
	labels := map[string]string{
		"kubernetes.io/hostname":        a.nodeName,
		"platform":                      nodePlatform.Label,
		"provider":                      "cisco-apphosting",
		"type":                          "virtual-kubelet",
		"topology.kubernetes.io/zone":   nodePlatform.Topology,
		"topology.kubernetes.io/region": nodePlatform.Topology,
	}
	if a.deviceSpec.Zone != "" {
		labels["topology.kubernetes.io/zone"] = a.deviceSpec.Zone
	}
	if a.deviceSpec.Region != "" {
		labels["topology.kubernetes.io/region"] = a.deviceSpec.Region
	}
	for k, v := range a.deviceSpec.Labels {
		labels[k] = v
	}

	taints := append([]v1.Taint(nil), a.deviceSpec.Taints...)

	// Create a node update with device info and addresses
	nodeUpdate := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: v1.NodeSpec{
			Taints: taints,
		},
		Status: v1.NodeStatus{
			NodeInfo: v1.NodeSystemInfo{
				MachineID:       deviceInfo.SerialNumber,
				SystemUUID:      deviceInfo.SerialNumber,
				KernelVersion:   deviceInfo.SoftwareVersion,
				KubeletVersion:  getVirtualKubeletVersion(),
				OSImage:         nodePlatform.OSImage,
				Architecture:    deviceInfo.ProductID,
				OperatingSystem: "Cisco",
			},
			Addresses: []v1.NodeAddress{
				{
					Type:    v1.NodeInternalIP,
					Address: nodeInternalIP,
				},
			},
			Conditions:  conditions,
			Capacity:    capacity,
			Allocatable: allocatable,
		},
	}

	cb(nodeUpdate)
}
