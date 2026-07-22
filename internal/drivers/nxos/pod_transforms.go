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
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	annotationPackageTimeout = "cisco.io/apphost-package-timeout"
	defaultPackageTimeout    = 180 * time.Second
	minPackageTimeout        = 10 * time.Second
	maxPackageTimeout        = 30 * time.Minute
	bytesPerMiB              = 1024 * 1024

	maxNXOSRunOptsLines      = 30
	maxNXOSRunOptsLineLength = 235
)

type nxosAppConfig struct {
	AppID        string
	Container    string
	PodName      string
	PodNamespace string
	PodUID       string
	ImagePath    string
	PullPolicy   v1.PullPolicy
	Timeout      time.Duration
	RunOpts      []string
	Resources    nxosResourceConfig
}

type nxosResourceConfig struct {
	cpuUnits        uint16
	memoryMB        uint16
	diskMB          uint16
	vcpu            uint16
	storageExplicit bool
}

func (d *NXOSDriver) convertPodToAppConfigs(pod *v1.Pod) ([]nxosAppConfig, error) {
	if err := validateNXOSNetworking(d.config); err != nil {
		return nil, err
	}

	appIDs := common.GenerateContainerAppIDs(pod)
	timeout := getPackageTimeout(pod)
	configs := make([]nxosAppConfig, 0, len(pod.Spec.Containers))
	for _, container := range pod.Spec.Containers {
		appID := appIDs[container.Name]
		if err := validateNXOSAppID(appID); err != nil {
			return nil, fmt.Errorf("container %s generated an invalid NX-OS app ID: %w", container.Name, err)
		}
		if err := validateNXOSPackagePath(container.Image); err != nil {
			return nil, fmt.Errorf("container %s image: %w", container.Name, err)
		}
		runOpts, err := d.buildRunOptions(pod, container)
		if err != nil {
			return nil, fmt.Errorf("container %s: %w", container.Name, err)
		}
		resources, err := d.getResourceConfig(&container)
		if err != nil {
			return nil, fmt.Errorf("container %s: %w", container.Name, err)
		}
		if resources.storageExplicit {
			return nil, fmt.Errorf("container %s: NX-OS app-resource profile supports cpu, memory, and vcpu reservations; disk/ephemeral-storage reservation is not supported by NX-OS app-hosting", container.Name)
		}
		configs = append(configs, nxosAppConfig{
			AppID:        appID,
			Container:    container.Name,
			PodName:      pod.Name,
			PodNamespace: pod.Namespace,
			PodUID:       string(pod.UID),
			ImagePath:    container.Image,
			PullPolicy:   container.ImagePullPolicy,
			Timeout:      timeout,
			RunOpts:      runOpts,
			Resources:    resources,
		})
	}
	return configs, nil
}

func validateNXOSNetworking(spec *v1alpha1.DeviceSpec) error {
	if spec == nil || spec.NXOS == nil || spec.NXOS.Networking == nil ||
		spec.NXOS.Networking.Interface == nil {
		return nil
	}
	iface := spec.NXOS.Networking.Interface
	if iface.Type != "" && iface.Type != v1alpha1.NXOSInterfaceManagement {
		return fmt.Errorf("unsupported NX-OS app-hosting interface type %q", iface.Type)
	}
	return nil
}

func getPackageTimeout(pod *v1.Pod) time.Duration {
	if pod == nil || pod.Annotations == nil {
		return defaultPackageTimeout
	}
	raw := strings.TrimSpace(pod.Annotations[annotationPackageTimeout])
	if raw == "" {
		return defaultPackageTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		secs, err2 := strconv.Atoi(raw)
		if err2 != nil {
			return defaultPackageTimeout
		}
		d = time.Duration(secs) * time.Second
	}
	if d < minPackageTimeout {
		return minPackageTimeout
	}
	if d > maxPackageTimeout {
		return maxPackageTimeout
	}
	return d
}

func (d *NXOSDriver) buildRunOptions(pod *v1.Pod, container v1.Container) ([]string, error) {
	baseOpts := []string{
		fmt.Sprintf("--label %s=%s", common.LabelPodName, pod.Name),
		fmt.Sprintf("--label %s=%s", common.LabelPodNamespace, pod.Namespace),
		fmt.Sprintf("--label %s=%s", common.LabelPodUID, pod.UID),
		fmt.Sprintf("--label %s=%s", common.LabelContainerName, container.Name),
		fmt.Sprintf("--hostname=%s", container.Name),
	}
	envOpts, err := d.buildEnvironmentOptions(pod.Namespace, container)
	if err != nil {
		return nil, err
	}
	return distributeNXOSRunOpts(baseOpts, envOpts)
}

func (d *NXOSDriver) buildEnvironmentOptions(namespace string, container v1.Container) ([]string, error) {
	var envOptions []string
	servicePrefixes := serviceLinkPrefixes(container.Env)
	for _, env := range container.Env {
		if !common.IsPortableEnvironmentVariableName(env.Name) {
			return nil, fmt.Errorf("environment variable name is not portable across app-hosting transports")
		}
		if isServiceLinkEnv(env.Name, servicePrefixes) {
			continue
		}
		value, ok, err := d.resolveEnvironmentValue(namespace, env)
		if err != nil {
			return nil, err
		}
		if !ok || value == "" {
			continue
		}
		if err := validateNXOSEnvironmentValue(value); err != nil {
			return nil, fmt.Errorf("environment variable %s: %w", env.Name, err)
		}
		envOptions = append(envOptions, fmt.Sprintf("--env %s=%s", env.Name, escapeShellValue(value)))
	}
	return envOptions, nil
}

func (d *NXOSDriver) resolveEnvironmentValue(namespace string, env v1.EnvVar) (string, bool, error) {
	if env.Value != "" {
		return env.Value, true, nil
	}
	if env.ValueFrom == nil {
		return "", false, nil
	}
	if env.ValueFrom.SecretKeyRef != nil {
		return d.resolveSecretKeyRef(namespace, env.ValueFrom.SecretKeyRef)
	}
	if env.ValueFrom.ConfigMapKeyRef != nil {
		return d.resolveConfigMapKeyRef(namespace, env.ValueFrom.ConfigMapKeyRef)
	}
	if env.ValueFrom.FieldRef != nil {
		return "", false, fmt.Errorf("fieldRef environment variable %s is not supported for NX-OS app-hosting", env.Name)
	}
	if env.ValueFrom.ResourceFieldRef != nil {
		return "", false, fmt.Errorf("resourceFieldRef environment variable %s is not supported for NX-OS app-hosting", env.Name)
	}
	return "", false, fmt.Errorf("unsupported environment variable source for %s in namespace %s", env.Name, namespace)
}

func (d *NXOSDriver) resolveSecretKeyRef(namespace string, ref *v1.SecretKeySelector) (string, bool, error) {
	secretLister, _ := d.podResourceListers(namespace)
	if secretLister == nil {
		return "", false, fmt.Errorf("secret lister not available")
	}
	secret, err := secretLister.Get(ref.Name)
	if err != nil {
		if ref.Optional != nil && *ref.Optional {
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to get secret %s: %w", ref.Name, err)
	}
	value, ok := secret.Data[ref.Key]
	if !ok {
		if ref.Optional != nil && *ref.Optional {
			return "", false, nil
		}
		return "", false, fmt.Errorf("key %s not found in secret %s", ref.Key, ref.Name)
	}
	return string(value), true, nil
}

func (d *NXOSDriver) resolveConfigMapKeyRef(namespace string, ref *v1.ConfigMapKeySelector) (string, bool, error) {
	_, configLister := d.podResourceListers(namespace)
	if configLister == nil {
		return "", false, fmt.Errorf("configmap lister not available")
	}
	configMap, err := configLister.Get(ref.Name)
	if err != nil {
		if ref.Optional != nil && *ref.Optional {
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to get configmap %s: %w", ref.Name, err)
	}
	value, ok := configMap.Data[ref.Key]
	if !ok {
		if ref.Optional != nil && *ref.Optional {
			return "", false, nil
		}
		return "", false, fmt.Errorf("key %s not found in configmap %s", ref.Key, ref.Name)
	}
	return value, true, nil
}

func distributeNXOSRunOpts(baseOpts, envOpts []string) ([]string, error) {
	lines := make([]string, 0, len(baseOpts)+len(envOpts))
	for _, opt := range baseOpts {
		if err := validateNXOSRunOption(opt); err != nil {
			return nil, err
		}
		if len(opt) > maxNXOSRunOptsLineLength {
			return nil, fmt.Errorf("single run-opts option too long (%d chars, max %d)", len(opt), maxNXOSRunOptsLineLength)
		}
		if len(lines) >= maxNXOSRunOptsLines {
			return nil, fmt.Errorf("too many run-opts lines needed (max %d)", maxNXOSRunOptsLines)
		}
		lines = append(lines, opt)
	}
	var current strings.Builder
	addLine := func() error {
		if current.Len() == 0 {
			return nil
		}
		if len(lines) >= maxNXOSRunOptsLines {
			return fmt.Errorf("too many run-opts lines needed (max %d)", maxNXOSRunOptsLines)
		}
		lines = append(lines, strings.TrimSpace(current.String()))
		current.Reset()
		return nil
	}
	addOption := func(opt string) error {
		if err := validateNXOSRunOption(opt); err != nil {
			return err
		}
		needed := len(opt)
		if current.Len() > 0 {
			needed++
		}
		if current.Len() > 0 && current.Len()+needed > maxNXOSRunOptsLineLength {
			if err := addLine(); err != nil {
				return err
			}
			needed = len(opt)
		}
		if needed > maxNXOSRunOptsLineLength {
			return fmt.Errorf("single run-opts option too long (%d chars, max %d)", needed, maxNXOSRunOptsLineLength)
		}
		if current.Len() > 0 {
			current.WriteString(" ")
		}
		current.WriteString(opt)
		return nil
	}
	for _, opt := range envOpts {
		if err := addOption(opt); err != nil {
			return nil, err
		}
	}
	if err := addLine(); err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines, nil
}

func (d *NXOSDriver) getResourceConfig(container *v1.Container) (nxosResourceConfig, error) {
	var resourceLimits v1alpha1.ResourceConfig
	if d.config != nil {
		resourceLimits = d.config.ResourceLimits
	}
	config := nxosResourceConfig{
		cpuUnits: 1000,
		memoryMB: 512,
		diskMB:   1024,
		vcpu:     1,
	}
	if container.Resources.Requests != nil {
		if cpu := container.Resources.Requests.Cpu(); cpu != nil && !cpu.IsZero() {
			units, err := nxosUint16Resource("cpu request milli-units", cpu.MilliValue())
			if err != nil {
				return config, err
			}
			config.cpuUnits = units
		}
		if mem := container.Resources.Requests.Memory(); mem != nil && !mem.IsZero() {
			mb, err := nxosMiBResource("memory request MiB", mem.Value())
			if err != nil {
				return config, err
			}
			config.memoryMB = mb
		}
		if storage, ok := container.Resources.Requests[v1.ResourceEphemeralStorage]; ok && !storage.IsZero() {
			mb, err := nxosMiBResource("ephemeral-storage request MiB", storage.Value())
			if err != nil {
				return config, err
			}
			config.diskMB = mb
			config.storageExplicit = true
		}
	}
	if container.Resources.Limits != nil {
		if cpu := container.Resources.Limits.Cpu(); cpu != nil && !cpu.IsZero() {
			vcpu, err := nxosVCPUResource("cpu limit rounded vcpu", cpu.MilliValue())
			if err != nil {
				return config, err
			}
			config.vcpu = vcpu
			if config.vcpu > 1 {
				return config, fmt.Errorf("NX-OS app-hosting on this transport does not support vcpu reservation; use CPU requests to reserve cpu units")
			}
		}
	}
	if err := applyResourceOverride(resourceLimits.DefaultCPU, func(q resource.Quantity) error {
		units, err := nxosUint16Resource("defaultCPU milli-units", q.MilliValue())
		if err != nil {
			return err
		}
		config.cpuUnits = units
		return nil
	}); err != nil {
		return config, fmt.Errorf("defaultCPU: %w", err)
	}
	if err := applyResourceOverride(resourceLimits.DefaultMemory, func(q resource.Quantity) error {
		mb, err := nxosMiBResource("defaultMemory MiB", q.Value())
		if err != nil {
			return err
		}
		config.memoryMB = mb
		return nil
	}); err != nil {
		return config, fmt.Errorf("defaultMemory: %w", err)
	}
	if resourceLimits.DefaultStorage != "" {
		return config, fmt.Errorf("defaultStorage is not supported by NX-OS app-resource profile")
	}
	return config, nil
}

func nxosUint16Resource(name string, value int64) (uint16, error) {
	if value < 0 {
		return 0, fmt.Errorf("%s must be non-negative, got %d", name, value)
	}
	if value > math.MaxUint16 {
		return 0, fmt.Errorf("%s %d exceeds NX-OS app-hosting maximum %d", name, value, math.MaxUint16)
	}
	return uint16(value), nil
}

func nxosMiBResource(name string, bytes int64) (uint16, error) {
	if bytes < 0 {
		return 0, fmt.Errorf("%s must be non-negative, got %d bytes", name, bytes)
	}
	mb := bytes / bytesPerMiB
	if bytes > 0 && bytes%bytesPerMiB != 0 {
		mb++
	}
	return nxosUint16Resource(name, mb)
}

func nxosVCPUResource(name string, milliCores int64) (uint16, error) {
	if milliCores < 0 {
		return 0, fmt.Errorf("%s must be non-negative, got %d milli-cores", name, milliCores)
	}
	vcpu := (milliCores + 999) / 1000
	if vcpu < 1 {
		vcpu = 1
	}
	return nxosUint16Resource(name, vcpu)
}

func applyResourceOverride(raw string, apply func(resource.Quantity) error) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	q, err := resource.ParseQuantity(raw)
	if err != nil {
		return err
	}
	return apply(q)
}

func escapeShellValue(value string) string {
	value = strings.ReplaceAll(value, `'`, `'\''`)
	return fmt.Sprintf(`'%s'`, value)
}

func validateNXOSEnvironmentValue(value string) error {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c < 0x20 || c > 0x7e || strings.IndexByte(";|\"'\\`", c) >= 0 {
			return fmt.Errorf("value contains characters that are unsafe in NX-API CLI run options")
		}
	}
	return nil
}

func validateNXOSRunOption(option string) error {
	if option == "" {
		return fmt.Errorf("empty NX-OS app-hosting run option")
	}
	for i := 0; i < len(option); i++ {
		c := option[i]
		if c < 0x20 || c > 0x7e || strings.IndexByte(";|\"\\`", c) >= 0 {
			return fmt.Errorf("NX-OS app-hosting run option contains unsafe CLI syntax")
		}
	}
	return nil
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// validateNXOSPackagePath ensures a pod-controlled image value is a single,
// safe NX-API CLI token before it can be interpolated into an app-hosting
// install command. CVK permits pre-staged bootflash paths and its existing
// Beta HTTP(S) source behavior. More complex URL characters can be
// percent-encoded by the caller.
func validateNXOSPackagePath(raw string) error {
	if raw == "" {
		return fmt.Errorf("NX-OS app-hosting package path is required")
	}
	if !isSafeNXOSPackageToken(raw) {
		return fmt.Errorf("NX-OS app-hosting package path contains characters that are unsafe in NX-API CLI")
	}

	if strings.HasPrefix(raw, "bootflash:") {
		relative := strings.TrimPrefix(raw, "bootflash:")
		relative = strings.TrimPrefix(relative, "/")
		if relative == "" {
			return fmt.Errorf("NX-OS bootflash package path must name a file")
		}
		for _, segment := range strings.Split(relative, "/") {
			if segment == "" || segment == "." || segment == ".." {
				return fmt.Errorf("NX-OS bootflash package path contains an invalid path segment")
			}
		}
		return nil
	}

	u, err := url.ParseRequestURI(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("NX-OS app-hosting package path must be a bootflash path or an HTTP(S) URL")
	}
	if u.User != nil || u.Fragment != "" || u.EscapedPath() == "" || u.EscapedPath() == "/" {
		return fmt.Errorf("NX-OS HTTP(S) package URL must include a host and package path and must not contain userinfo or a fragment")
	}
	return nil
}

func isSafeNXOSPackageToken(raw string) bool {
	const punctuation = "-._~:/?&=%+@,[]"
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.IndexByte(punctuation, c) >= 0 {
			continue
		}
		return false
	}
	return true
}
