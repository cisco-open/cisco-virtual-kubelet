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

package nxos

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

// NXOSDriver drives NX-OS app-hosting through NX-API CLI.
type NXOSDriver struct {
	config     *v1alpha1.DeviceSpec
	client     *nxapiClient
	deviceInfo *common.DeviceInfo
	mu         sync.Mutex

	resourceMu             sync.RWMutex
	secretLister           corev1listers.SecretLister
	configLister           corev1listers.ConfigMapLister
	namespaceSecretListers map[string]corev1listers.SecretNamespaceLister
	namespaceConfigListers map[string]corev1listers.ConfigMapNamespaceLister

	asyncActions bool
	actionMu     sync.Mutex
	appActions   map[string]nxosAppAction

	// convergingPods dedups the detached convergence goroutine that
	// GetPodStatus kicks: at most one in-flight convergence per pod UID
	// regardless of poll cadence.
	convergeMu     sync.Mutex
	convergingPods map[string]struct{}
}

type nxosAppAction struct {
	Action  string
	At      time.Time
	Running bool
}

func NewAppHostingDriver(ctx context.Context, spec *v1alpha1.DeviceSpec) (*NXOSDriver, error) {
	client, err := newNXAPIClient(spec)
	if err != nil {
		return nil, err
	}
	d := &NXOSDriver{
		config:                 spec,
		client:                 client,
		asyncActions:           true,
		appActions:             make(map[string]nxosAppAction),
		namespaceSecretListers: make(map[string]corev1listers.SecretNamespaceLister),
		namespaceConfigListers: make(map[string]corev1listers.ConfigMapNamespaceLister),
	}
	if err := d.CheckConnection(ctx); err != nil {
		return nil, err
	}
	if spec.AllowUnsignedApps {
		if err := d.ConfigureSignVerification(ctx, false); err != nil {
			log.G(ctx).WithError(err).Warn("allowUnsignedApps=true but failed to disable NX-OS app-hosting sign-verification; unsigned installs may be blocked")
		}
	}
	return d, nil
}

// SetPodResourceListers supplies cluster-wide informer listers for deferred
// lifecycle convergence. Lookups remain explicitly namespace-scoped in
// podResourceListers.
func (d *NXOSDriver) SetPodResourceListers(secrets corev1listers.SecretLister, configMaps corev1listers.ConfigMapLister) {
	if d == nil {
		return
	}
	d.resourceMu.Lock()
	d.secretLister = secrets
	d.configLister = configMaps
	d.resourceMu.Unlock()
}

// rememberPodResourceListers preserves the existing driver contract for
// callers that provide only namespace-scoped listers to DeployPod. The maps
// are keyed by namespace so concurrent pods cannot overwrite each other's
// Secret or ConfigMap source.
func (d *NXOSDriver) rememberPodResourceListers(namespace string, secrets corev1listers.SecretNamespaceLister, configMaps corev1listers.ConfigMapNamespaceLister) {
	if d == nil || namespace == "" {
		return
	}
	d.resourceMu.Lock()
	defer d.resourceMu.Unlock()
	if d.namespaceSecretListers == nil {
		d.namespaceSecretListers = make(map[string]corev1listers.SecretNamespaceLister)
	}
	if d.namespaceConfigListers == nil {
		d.namespaceConfigListers = make(map[string]corev1listers.ConfigMapNamespaceLister)
	}
	if secrets != nil {
		d.namespaceSecretListers[namespace] = secrets
	}
	if configMaps != nil {
		d.namespaceConfigListers[namespace] = configMaps
	}
}

func (d *NXOSDriver) podResourceListers(namespace string) (corev1listers.SecretNamespaceLister, corev1listers.ConfigMapNamespaceLister) {
	if d == nil || namespace == "" {
		return nil, nil
	}
	d.resourceMu.RLock()
	secrets := d.secretLister
	configMaps := d.configLister
	namespaceSecrets := d.namespaceSecretListers[namespace]
	namespaceConfigMaps := d.namespaceConfigListers[namespace]
	d.resourceMu.RUnlock()
	if secrets != nil {
		namespaceSecrets = secrets.Secrets(namespace)
	}
	if configMaps != nil {
		namespaceConfigMaps = configMaps.ConfigMaps(namespace)
	}
	return namespaceSecrets, namespaceConfigMaps
}

func (d *NXOSDriver) hasPodResourceListers(namespace string) bool {
	secrets, configMaps := d.podResourceListers(namespace)
	return secrets != nil && configMaps != nil
}

func (d *NXOSDriver) CheckConnection(ctx context.Context) error {
	out, err := d.client.show(ctx, "show version")
	if err != nil {
		return fmt.Errorf("nxos connectivity check failed: %w", err)
	}
	d.deviceInfo = parseDeviceInfo(out)
	if d.deviceInfo.Hostname == "" {
		if host, hostErr := d.client.show(ctx, "show hostname"); hostErr == nil {
			d.deviceInfo.Hostname = strings.TrimSpace(host)
		}
	}
	if inv, invErr := d.client.show(ctx, "show inventory"); invErr == nil {
		enrichInventory(d.deviceInfo, inv)
	}
	log.G(ctx).WithFields(log.Fields{
		"hostname": d.deviceInfo.Hostname,
		"version":  d.deviceInfo.SoftwareVersion,
		"product":  d.deviceInfo.ProductID,
		"serial":   d.deviceInfo.SerialNumber,
	}).Info("NX-OS device connected")
	return nil
}

func (d *NXOSDriver) GetDeviceInfo(ctx context.Context) (*common.DeviceInfo, error) {
	if d.deviceInfo == nil {
		if err := d.CheckConnection(ctx); err != nil {
			return nil, err
		}
	}
	return d.deviceInfo, nil
}

func (d *NXOSDriver) GetDeviceResources(ctx context.Context) (*v1.ResourceList, error) {
	pods := d.config.MaxPods
	if pods <= 0 {
		pods = 16
	}
	if observed, err := d.readAppHostingResources(ctx); err == nil && observed.hasCapacity() {
		resources := v1.ResourceList{
			v1.ResourcePods: *resource.NewQuantity(int64(pods), resource.DecimalSI),
		}
		if observed.CPUTotalMilli > 0 {
			resources[v1.ResourceCPU] = *resource.NewMilliQuantity(observed.CPUTotalMilli, resource.DecimalSI)
		}
		if observed.MemoryTotalMB > 0 {
			resources[v1.ResourceMemory] = *resource.NewQuantity(observed.MemoryTotalMB*1024*1024, resource.BinarySI)
		}
		if observed.StorageTotalMB > 0 {
			resources[v1.ResourceStorage] = *resource.NewQuantity(observed.StorageTotalMB*1024*1024, resource.BinarySI)
		}
		return &resources, nil
	}
	resources := v1.ResourceList{
		v1.ResourceCPU:     resource.MustParse("4"),
		v1.ResourceMemory:  resource.MustParse("16Gi"),
		v1.ResourceStorage: resource.MustParse("1500Mi"),
		v1.ResourcePods:    *resource.NewQuantity(int64(pods), resource.DecimalSI),
	}
	return &resources, nil
}

func (d *NXOSDriver) GetGlobalOperationalData(ctx context.Context) (*common.AppHostingOperData, error) {
	out, err := d.client.show(ctx, "show feature | include app-hosting")
	if err != nil {
		return nil, err
	}
	enabled := strings.Contains(strings.ToLower(out), "enabled")
	resources, resErr := d.readAppHostingResources(ctx)
	if resErr == nil && resources.hasCapacity() {
		return &common.AppHostingOperData{
			IoxEnabled: enabled,
			SystemCPU: common.AppResource{
				Quota:     milliToWholeCores(resources.CPUTotalMilli),
				Available: milliToWholeCores(resources.CPUAvailableMilli),
				Unit:      "cores",
			},
			Memory: common.AppResource{
				Quota:     resources.MemoryTotalMB,
				Available: resources.MemoryAvailableMB,
				Unit:      "MB",
			},
			Storage: common.AppResource{
				Quota:     resources.StorageTotalMB,
				Available: resources.StorageAvailableMB,
				Unit:      "MB",
			},
		}, nil
	}
	return &common.AppHostingOperData{
		IoxEnabled: enabled,
		SystemCPU:  common.AppResource{Quota: 4, Available: 4, Unit: "cores"},
		Memory:     common.AppResource{Quota: 16384, Available: 16384, Unit: "MB"},
		Storage:    common.AppResource{Quota: 1500, Available: 1500, Unit: "MB"},
	}, nil
}

func (d *NXOSDriver) ConfigureSignVerification(ctx context.Context, enabled bool) error {
	mode := "disable"
	if enabled {
		mode = "enable"
	}
	if _, err := d.client.conf(ctx, "configure terminal", fmt.Sprintf("app-hosting signed-verification %s", mode)); err != nil {
		return fmt.Errorf("nxos configure app-hosting signed-verification %s: %w", mode, err)
	}
	log.G(ctx).Infof("NX-OS app-hosting signed-verification set to %s", mode)
	return nil
}

func parseDeviceInfo(out string) *common.DeviceInfo {
	info := &common.DeviceInfo{}
	for _, line := range strings.Split(out, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "device name") || strings.HasPrefix(lower, "hostname") {
			if v := afterColon(line); v != "" {
				info.Hostname = v
			}
		}
		if strings.Contains(lower, "nxos: version") || strings.Contains(lower, "nx-os") && strings.Contains(lower, "version") {
			if m := regexp.MustCompile(`(?i)(?:nxos:|nx-os)?\s*version\s+([^\s,]+)`).FindStringSubmatch(line); len(m) > 1 {
				info.SoftwareVersion = m[1]
			}
		}
		if strings.Contains(lower, "system version") {
			if v := afterColon(line); v != "" {
				info.SoftwareVersion = strings.TrimPrefix(v, "version ")
			}
		}
	}
	return info
}

func enrichInventory(info *common.DeviceInfo, out string) {
	pidRe := regexp.MustCompile(`(?i)\bPID:\s*([^,\s]+)`)
	serialRe := regexp.MustCompile(`(?i)\bSN:\s*([^,\s]+)`)
	if m := pidRe.FindStringSubmatch(out); len(m) > 1 && info.ProductID == "" {
		info.ProductID = strings.TrimSpace(m[1])
	}
	if m := serialRe.FindStringSubmatch(out); len(m) > 1 && info.SerialNumber == "" {
		info.SerialNumber = strings.TrimSpace(m[1])
	}
}

func afterColon(line string) string {
	idx := strings.Index(line, ":")
	if idx == -1 {
		return ""
	}
	return strings.TrimSpace(line[idx+1:])
}

func nxosGuestInterface(spec *v1alpha1.DeviceSpec) uint8 {
	if spec == nil || spec.NXOS == nil || spec.NXOS.Networking == nil ||
		spec.NXOS.Networking.Interface == nil ||
		spec.NXOS.Networking.Interface.Management == nil {
		return 0
	}
	return spec.NXOS.Networking.Interface.Management.GuestInterface
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v
}

func milliToWholeCores(v int64) int64 {
	if v <= 0 {
		return 0
	}
	cores := v / 1000
	if cores < 1 {
		return 1
	}
	return cores
}

const nxosAppActionRetryInterval = 30 * time.Second

// nxosAppActionTimeout bounds an async app-hosting action (install /
// activate / start). The work runs in a goroutine that OUTLIVES the
// request that scheduled it, so it must NOT inherit the request ctx
// (which is cancelled the moment DeployPod/GetPodStatus returns).
const nxosAppActionTimeout = 30 * time.Minute

func (d *NXOSDriver) runAppAction(ctx context.Context, appID, action string, fn func(context.Context) error) error {
	if err := validateNXOSAppID(appID); err != nil {
		return err
	}
	if !d.markAppAction(appID, action) {
		return nil
	}
	if !d.asyncActions {
		if err := fn(ctx); err != nil {
			d.clearAppAction(appID)
			return err
		}
		d.completeAppAction(appID, action)
		return nil
	}
	log.G(ctx).WithFields(log.Fields{
		"appid":  appID,
		"action": action,
	}).Info("NX-OS app-hosting action scheduled")
	// Detach from the request ctx: derive a background ctx (preserving
	// logger fields) with a bounded timeout so the action survives the
	// return of the call that scheduled it.
	bgCtx, cancel := context.WithTimeout(log.WithLogger(context.Background(), log.G(ctx)), nxosAppActionTimeout)
	go func() {
		defer cancel()
		if err := fn(bgCtx); err != nil {
			d.clearAppAction(appID)
			log.G(bgCtx).WithError(err).WithFields(log.Fields{
				"appid":  appID,
				"action": action,
			}).Warn("NX-OS app-hosting action failed")
			return
		}
		d.completeAppAction(appID, action)
		log.G(bgCtx).WithFields(log.Fields{
			"appid":  appID,
			"action": action,
		}).Info("NX-OS app-hosting action completed")
	}()
	return nil
}

func (d *NXOSDriver) markAppAction(appID, action string) bool {
	if d == nil || appID == "" || action == "" {
		return false
	}
	d.actionMu.Lock()
	defer d.actionMu.Unlock()
	if d.appActions == nil {
		d.appActions = make(map[string]nxosAppAction)
	}
	now := time.Now()
	if previous, ok := d.appActions[appID]; ok &&
		previous.Action == action &&
		(previous.Running || now.Sub(previous.At) < nxosAppActionRetryInterval) {
		return false
	}
	d.appActions[appID] = nxosAppAction{Action: action, At: now, Running: true}
	return true
}

func (d *NXOSDriver) completeAppAction(appID, action string) {
	if d == nil || appID == "" {
		return
	}
	d.actionMu.Lock()
	defer d.actionMu.Unlock()
	if current, ok := d.appActions[appID]; ok && current.Action == action {
		current.Running = false
		current.At = time.Now()
		d.appActions[appID] = current
	}
}

func (d *NXOSDriver) clearAppAction(appID string) {
	if d == nil || appID == "" {
		return
	}
	d.actionMu.Lock()
	defer d.actionMu.Unlock()
	delete(d.appActions, appID)
}
