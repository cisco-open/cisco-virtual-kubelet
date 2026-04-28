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
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/openconfig/ygot/ygot"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	annotationPackageDest    = "cisco.io/apphost-package-dest"
	annotationPackageTimeout = "cisco.io/apphost-package-timeout"
)

const (
	defaultPackageTimeout = 180 * time.Second
	minPackageTimeout     = 10 * time.Second
	maxPackageTimeout     = 30 * time.Minute
)

// getPackageDest reads the on-device flash destination path from a pod annotation.
// Accepts paths with or without a leading slash after the filesystem prefix,
// e.g. both "flash:app.tar" and "flash:/dir/app.tar" are valid.
func getPackageDest(pod *v1.Pod) (string, error) {
	if pod == nil || pod.Annotations == nil {
		return "", nil
	}
	dest, ok := pod.Annotations[annotationPackageDest]
	if !ok {
		return "", nil
	}
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return "", nil
	}
	valid := regexp.MustCompile(`^(bootflash:|harddisk:|flash:|nvram:|usb:)/?(.+)`)
	if !valid.MatchString(dest) {
		return "", fmt.Errorf(
			"invalid %s annotation %q: expected e.g. flash:app.tar or flash:/virtual-kubelet/app.tar",
			annotationPackageDest, dest,
		)
	}
	return dest, nil
}

// getPackageTimeout reads the RUNNING-wait timeout from a pod annotation.
// Accepts Go duration strings (e.g. "3m", "180s") and bare integer seconds (e.g. "180").
// Returns defaultPackageTimeout on any parse failure; clamps to [min, max].
func getPackageTimeout(pod *v1.Pod) time.Duration {
	if pod == nil || pod.Annotations == nil {
		return defaultPackageTimeout
	}
	raw, ok := pod.Annotations[annotationPackageTimeout]
	if !ok {
		return defaultPackageTimeout
	}
	raw = strings.TrimSpace(raw)
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
		d = minPackageTimeout
	}
	if d > maxPackageTimeout {
		d = maxPackageTimeout
	}
	return d
}

// ConvertPodToAppConfigs converts a Kubernetes Pod spec into a slice of IOS-XE AppHosting configurations.
// Each container in the pod is converted to a separate AppHosting app configuration.
// Returns a slice of AppHostingConfig structs ready to be created on the device.
func (d *XEDriver) ConvertPodToAppConfigs(pod *v1.Pod) ([]AppHostingConfig, error) {
	containerAppIDs := common.GenerateContainerAppIDs(pod)
	configs := make([]AppHostingConfig, 0, len(pod.Spec.Containers))

	packageDest, err := getPackageDest(pod)
	if err != nil {
		return nil, err
	}
	packageTimeout := getPackageTimeout(pod)

	for _, container := range pod.Spec.Containers {
		appName := containerAppIDs[container.Name]

		// Create the Apps container structure
		apps := &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps{}

		// Create new app entry
		gapp, err := apps.NewApp(appName)
		if err != nil {
			return nil, fmt.Errorf("failed to create app struct for container %s: %w", container.Name, err)
		}

		// Configure network resources based on interface type
		netConfig := d.getNetworkConfig(pod, &container)

		switch netConfig.interfaceType {
		case v1alpha1.XEInterfaceTypeVirtualPortGroup:
			if netConfig.useDHCP {
				// DHCP mode: only set interface name, omit static IP configuration
				gapp.ApplicationNetworkResource = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationNetworkResource{
					VnicGateway_0:                        ygot.String("0"),
					VirtualportgroupGuestInterfaceName_1: ygot.String(netConfig.virtualPortgroupInterface),
				}
			} else {
				// Static IP mode: configure IP address and netmask (only for primary container)
				if netConfig.isPrimaryContainer && (netConfig.virtualPortgroupIP == "" || netConfig.virtualPortgroupNetmask == "") {
					return nil, fmt.Errorf("podCIDR is required to allocate IPs when dhcp=false")
				}
				netRes := &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationNetworkResource{
					VnicGateway_0:                        ygot.String("0"),
					VirtualportgroupGuestInterfaceName_1: ygot.String(netConfig.virtualPortgroupInterface),
				}
				if netConfig.virtualPortgroupIP != "" && netConfig.virtualPortgroupNetmask != "" {
					netRes.VirtualportgroupGuestIpAddress_1 = ygot.String(netConfig.virtualPortgroupIP)
					netRes.VirtualportgroupGuestIpNetmask_1 = ygot.String(netConfig.virtualPortgroupNetmask)
				}
				gapp.ApplicationNetworkResource = netRes
			}

		case v1alpha1.XEInterfaceTypeAppGigabitEthernet:
			guestIf := netConfig.vlanIf.GuestInterface
			if guestIf == 0 && netConfig.appGigGuestInterface != 0 {
				guestIf = netConfig.appGigGuestInterface
			}

			switch netConfig.appGigMode {
			case v1alpha1.XEAppGigabitEthernetModeAccess:
				if gapp.ApplicationNetworkResource == nil {
					gapp.ApplicationNetworkResource = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationNetworkResource{}
				}
				gapp.ApplicationNetworkResource.AppintfVlanMode = Cisco_IOS_XEAppHostingCfg_ImAppAppintfMode_appintf_access
				gapp.ApplicationNetworkResource.AppintfAccessInterfaceNumber = ygot.Uint8(guestIf)

				if netConfig.vlanIf.Vlan > 0 {
					gapp.AppintfVlanRules = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_AppintfVlanRules{}
					vlanRule, err := gapp.AppintfVlanRules.NewAppintfVlanRule(netConfig.vlanIf.Vlan)
					if err != nil {
						return nil, fmt.Errorf("failed to create VLAN rule for container %s: %w", container.Name, err)
					}
					vlanRule.GuestInterface = ygot.Uint8(guestIf)

					if !netConfig.useDHCP {
						if netConfig.isPrimaryContainer && (netConfig.virtualPortgroupIP == "" || netConfig.virtualPortgroupNetmask == "") {
							return nil, fmt.Errorf("podCIDR is required to allocate IPs when dhcp=false")
						}
						if netConfig.virtualPortgroupIP != "" && netConfig.virtualPortgroupNetmask != "" {
							vlanRule.GuestIp = ygot.String(netConfig.virtualPortgroupIP)
							vlanRule.GuestIpnetmask = ygot.String(netConfig.virtualPortgroupNetmask)
						}
					}
				}

			case v1alpha1.XEAppGigabitEthernetModeTrunk:
				if gapp.ApplicationNetworkResource == nil {
					gapp.ApplicationNetworkResource = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationNetworkResource{}
				}
				gapp.ApplicationNetworkResource.AppintfVlanMode = Cisco_IOS_XEAppHostingCfg_ImAppAppintfMode_appintf_trunk

				if netConfig.vlanIf.Vlan > 0 {
					gapp.AppintfVlanRules = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_AppintfVlanRules{}
					vlanRule, err := gapp.AppintfVlanRules.NewAppintfVlanRule(netConfig.vlanIf.Vlan)
					if err != nil {
						return nil, fmt.Errorf("failed to create VLAN rule for container %s: %w", container.Name, err)
					}
					vlanRule.GuestInterface = ygot.Uint8(guestIf)
					vlanRule.MacForwardEnable = ygot.Bool(netConfig.vlanIf.MacForwardingEnabled)
					vlanRule.McastEnable = ygot.Bool(netConfig.vlanIf.MulticastEnabled)
					vlanRule.MirrorEnable = ygot.Bool(netConfig.vlanIf.MirrorEnabled)

					if !netConfig.useDHCP {
						if netConfig.isPrimaryContainer && (netConfig.virtualPortgroupIP == "" || netConfig.virtualPortgroupNetmask == "") {
							return nil, fmt.Errorf("podCIDR is required to allocate IPs when dhcp=false")
						}
						if netConfig.virtualPortgroupIP != "" && netConfig.virtualPortgroupNetmask != "" {
							vlanRule.GuestIp = ygot.String(netConfig.virtualPortgroupIP)
							vlanRule.GuestIpnetmask = ygot.String(netConfig.virtualPortgroupNetmask)
						}
					}
				}
			default:
				return nil, fmt.Errorf("unsupported AppGigabitEthernet mode: %s", netConfig.appGigMode)
			}

		case v1alpha1.XEInterfaceTypeManagement:
			gapp.AppintfMgmt = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_AppintfMgmt{
				AccessIfNum: ygot.Uint8(netConfig.mgmtGuestInterface),
			}
			gapp.ApplicationNetworkResource = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationNetworkResource{}
			gapp.ApplicationNetworkResource.ManagementInterfaceName = ygot.String(fmt.Sprintf("%d", netConfig.mgmtGuestInterface))

			if !netConfig.useDHCP {
				if netConfig.isPrimaryContainer && (netConfig.mgmtGuestIPv4 == "" || netConfig.mgmtGuestIPv4Mask == "") {
					return nil, fmt.Errorf("podCIDR is required to allocate IPs when dhcp=false")
				}
				if netConfig.mgmtGuestIPv4 != "" && netConfig.mgmtGuestIPv4Mask != "" {
					gapp.ApplicationNetworkResource.ManagementGuestIpAddress = ygot.String(netConfig.mgmtGuestIPv4)
					gapp.ApplicationNetworkResource.ManagementGuestIpNetmask = ygot.String(netConfig.mgmtGuestIPv4Mask)
				}
			}

		default:
			return nil, fmt.Errorf("unsupported interface type: %s", netConfig.interfaceType)
		}

		// Configure run options with pod/container labels and environment variables
		baseOpts := []string{
			fmt.Sprintf("--label %s=%s", common.LabelPodName, pod.Name),
			fmt.Sprintf("--label %s=%s", common.LabelPodNamespace, pod.Namespace),
			fmt.Sprintf("--label %s=%s", common.LabelPodUID, pod.UID),
			fmt.Sprintf("--label %s=%s", common.LabelContainerName, container.Name),
		}

		// Build environment options
		envOpts, err := d.buildEnvironmentOptions(&container, pod)
		if err != nil {
			return nil, fmt.Errorf("failed to build environment options for container %s: %w", container.Name, err)
		}

		// Distribute across RunOpts lines
		runOptsMap, err := distributeRunOpts(baseOpts, envOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to distribute RunOpts for container %s: %w", container.Name, err)
		}

		// Configure run options
		gapp.RunOptss = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss{
			RunOpts: runOptsMap,
		}
		// Enable docker resources to allow RunOpts to take effect
		gapp.DockerResource = ygot.Bool(true)

		// Configure resource profile
		resConfig := d.getResourceConfig(&container)
		gapp.ApplicationResourceProfile = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationResourceProfile{
			ProfileName:      ygot.String("custom"),
			CpuUnits:         ygot.Uint16(resConfig.cpuUnits),
			MemoryCapacityMb: ygot.Uint16(resConfig.memoryMB),
			DiskSizeMb:       ygot.Uint16(resConfig.diskMB),
			Vcpu:             ygot.Uint16(resConfig.vcpu),
		}

		// Set app to start automatically
		gapp.Start = ygot.Bool(true)

		// Add to configs slice
		configs = append(configs, AppHostingConfig{
			Metadata: AppHostingMetadata{
				AppName:       appName,
				ContainerName: container.Name,
				PodName:       pod.Name,
				PodNamespace:  pod.Namespace,
				PodUID:        string(pod.UID),
				Pod:           pod,
			},
			Spec: AppHostingSpec{
				ImagePath:        container.Image,
				DesiredState:     AppDesiredStateRunning,
				DeviceConfig:     apps,
				ImagePullPolicy:  container.ImagePullPolicy,
				PackageDest:      packageDest,
				PackageTimeout:   packageTimeout,
				ImagePullSecrets: pod.Spec.ImagePullSecrets,
			},
			Status: AppHostingStatus{
				Phase: AppPhaseConverging,
			},
		})
	}

	return configs, nil
}

// getNetworkConfig converts pod/container specs to IOS-XE network configuration
func (d *XEDriver) getNetworkConfig(pod *v1.Pod, container *v1.Container) *networkConfig {
	// Check if XE-specific interface configuration is present
	if d.config.XE != nil && d.config.XE.Networking.Interface != nil {
		cfg := d.getInterfaceConfig(pod, container, d.config.XE.Networking.Interface)
		cfg.isPrimaryContainer = d.getContainerIndex(pod, container) == 0
		return cfg
	}

	// Default to VirtualPortGroup interface with DHCP implied
	return &networkConfig{
		interfaceType:             v1alpha1.XEInterfaceTypeVirtualPortGroup,
		useDHCP:                   true,
		virtualPortgroupInterface: "0",
		guestInterface:            0,
		isPrimaryContainer:        d.getContainerIndex(pod, container) == 0,
	}
}

// getInterfaceConfig creates network configuration based on the new interface configuration model
func (d *XEDriver) getInterfaceConfig(pod *v1.Pod, container *v1.Container, ifConfig *v1alpha1.XEInterfaceConfig) *networkConfig {
	netConfig := &networkConfig{
		interfaceType: ifConfig.Type,
		useDHCP:       true,
	}

	switch ifConfig.Type {
	case v1alpha1.XEInterfaceTypeVirtualPortGroup:
		if ifConfig.VirtualPortGroup != nil {
			vpgInterface := ifConfig.VirtualPortGroup.Interface
			if vpgInterface == "" {
				vpgInterface = "0"
			}
			netConfig.virtualPortgroupInterface = vpgInterface
			netConfig.guestInterface = ifConfig.VirtualPortGroup.GuestInterface
			netConfig.useDHCP = ifConfig.VirtualPortGroup.Dhcp
			if !netConfig.useDHCP {
				ip, netmask, err := d.allocateIPForContainer(pod, container)
				netConfig.virtualPortgroupIP = ip
				netConfig.virtualPortgroupNetmask = netmask
				netConfig.ipAllocationErr = err
			}
		}

	case v1alpha1.XEInterfaceTypeAppGigabitEthernet:
		if ifConfig.AppGigabitEthernet != nil {
			netConfig.appGigMode = ifConfig.AppGigabitEthernet.Mode
			netConfig.appGigGuestInterface = ifConfig.AppGigabitEthernet.GuestInterface
			netConfig.vlanIf = ifConfig.AppGigabitEthernet.VlanIf
			if netConfig.appGigMode == v1alpha1.XEAppGigabitEthernetModeAccess && netConfig.vlanIf.Vlan == 0 {
				netConfig.useDHCP = ifConfig.AppGigabitEthernet.Dhcp
			} else {
				netConfig.useDHCP = ifConfig.AppGigabitEthernet.VlanIf.Dhcp
			}
			if !netConfig.useDHCP {
				ip, netmask, err := d.allocateIPForContainer(pod, container)
				netConfig.virtualPortgroupIP = ip
				netConfig.virtualPortgroupNetmask = netmask
				netConfig.ipAllocationErr = err
			}
		}

	case v1alpha1.XEInterfaceTypeManagement:
		if ifConfig.Management != nil {
			netConfig.mgmtGuestInterface = ifConfig.Management.GuestInterface
			netConfig.useDHCP = ifConfig.Management.Dhcp
			if !netConfig.useDHCP {
				ip, netmask, err := d.allocateIPForContainer(pod, container)
				netConfig.mgmtGuestIPv4 = ip
				netConfig.mgmtGuestIPv4Mask = netmask
				netConfig.ipAllocationErr = err
			}
		}
	}

	return netConfig
}

// allocateIPForContainer determines the IP address for a container based on pod prefix configuration
func (d *XEDriver) allocateIPForContainer(pod *v1.Pod, container *v1.Container) (ip, netmask string, err error) {
	podCIDR := d.config.PodCIDR
	if podCIDR == "" {
		return "", "", fmt.Errorf("podCIDR is empty")
	}

	_, ipNet, parseErr := net.ParseCIDR(podCIDR)
	if parseErr != nil {
		return "", "", fmt.Errorf("invalid podCIDR: %w", parseErr)
	}

	netmask = net.IP(ipNet.Mask).String()
	containerIndex := d.getContainerIndex(pod, container)
	if containerIndex != 0 {
		return "", "", nil
	}
	ip = d.getIPForContainer(ipNet, containerIndex)
	return ip, netmask, nil
}

// getContainerIndex returns the index of a container within a pod's container list
func (d *XEDriver) getContainerIndex(pod *v1.Pod, container *v1.Container) int {
	for i, c := range pod.Spec.Containers {
		if c.Name == container.Name {
			return i
		}
	}
	return 0
}

// getIPForContainer calculates the IP address for a container based on its index
func (d *XEDriver) getIPForContainer(ipNet *net.IPNet, containerIndex int) string {
	ip := ipNet.IP.To4()
	if ip == nil {
		return ""
	}
	ip[3] = ip[3] + uint8(10+containerIndex)
	return ip.String()
}

// getResourceConfig converts Kubernetes resource requests/limits to IOS-XE resource configuration
func (d *XEDriver) getResourceConfig(container *v1.Container) *resourceConfig {
	config := &resourceConfig{
		cpuUnits: 1000,
		memoryMB: 512,
		diskMB:   1024,
		vcpu:     1,
	}

	if container.Resources.Requests != nil {
		if cpu := container.Resources.Requests.Cpu(); cpu != nil && !cpu.IsZero() {
			config.cpuUnits = uint16(cpu.MilliValue())
		}
		if mem := container.Resources.Requests.Memory(); mem != nil && !mem.IsZero() {
			config.memoryMB = uint16(mem.Value() / (1024 * 1024))
		}
		if storage, ok := container.Resources.Requests[v1.ResourceEphemeralStorage]; ok && !storage.IsZero() {
			config.diskMB = uint16(storage.Value() / (1024 * 1024))
		}
	}

	if container.Resources.Limits != nil {
		if cpu := container.Resources.Limits.Cpu(); cpu != nil && !cpu.IsZero() {
			milliCores := cpu.MilliValue()
			config.vcpu = uint16((milliCores + 999) / 1000)
			if config.vcpu < 1 {
				config.vcpu = 1
			}
		}
	}

	if d.config.ResourceLimits.DefaultCPU != "" {
		if q, err := resource.ParseQuantity(d.config.ResourceLimits.DefaultCPU); err == nil {
			config.cpuUnits = uint16(q.MilliValue())
		}
	}
	if d.config.ResourceLimits.DefaultMemory != "" {
		if q, err := resource.ParseQuantity(d.config.ResourceLimits.DefaultMemory); err == nil {
			config.memoryMB = uint16(q.Value() / (1024 * 1024))
		}
	}
	if d.config.ResourceLimits.DefaultStorage != "" {
		if q, err := resource.ParseQuantity(d.config.ResourceLimits.DefaultStorage); err == nil {
			config.diskMB = uint16(q.Value() / (1024 * 1024))
		}
	}

	return config
}

// buildEnvironmentOptions extracts environment variables from a container spec and formats them
// as Docker run options. Supports direct values and references to secrets/configmaps.
func (d *XEDriver) buildEnvironmentOptions(container *v1.Container, pod *v1.Pod) ([]string, error) {
	var envOptions []string

	// Process environment variables from container.Env
	for _, env := range container.Env {
		var value string
		var err error

		if env.Value != "" {
			// Direct value (Phase 1)
			value = env.Value
		} else if env.ValueFrom != nil {
			// Reference to secret/configmap/etc (Phase 2)
			value, err = d.resolveEnvVarValueFrom(env.ValueFrom, pod.Namespace)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve environment variable %s: %v", env.Name, err)
			}
		} else {
			// Neither direct value nor valueFrom - skip
			continue
		}

		// Skip empty values (including optional secrets/configmaps that don't exist)
		if value == "" {
			continue
		}

		// Escape special characters for shell safety
		escapedValue := escapeShellValue(value)
		envOptions = append(envOptions, fmt.Sprintf("-e %s=%s", env.Name, escapedValue))
	}

	return envOptions, nil
}

// escapeShellValue safely escapes special characters in environment variable values
// to prevent shell injection and ensure proper parsing by Docker.
func escapeShellValue(value string) string {
	// Escape quotes and special characters that could break shell parsing
	value = strings.ReplaceAll(value, `\`, `\\`)   // Escape backslashes first
	value = strings.ReplaceAll(value, `"`, `\"`)   // Escape double quotes
	value = strings.ReplaceAll(value, `$`, `\$`)   // Escape dollar signs (variable expansion)
	value = strings.ReplaceAll(value, "`", "\\`")  // Escape backticks (command substitution)

	// Wrap in double quotes to handle spaces and other special chars
	return fmt.Sprintf(`"%s"`, value)
}

// resolveEnvVarValueFrom resolves environment variable values from various sources
// (secrets, configmaps, downward API, resource references).
func (d *XEDriver) resolveEnvVarValueFrom(valueFrom *v1.EnvVarSource, namespace string) (string, error) {
	if valueFrom.SecretKeyRef != nil {
		return d.resolveSecretKeyRef(valueFrom.SecretKeyRef, namespace)
	}

	if valueFrom.ConfigMapKeyRef != nil {
		return d.resolveConfigMapKeyRef(valueFrom.ConfigMapKeyRef, namespace)
	}

	if valueFrom.FieldRef != nil {
		return "", fmt.Errorf("fieldRef environment variables not yet supported")
	}

	if valueFrom.ResourceFieldRef != nil {
		return "", fmt.Errorf("resourceFieldRef environment variables not yet supported")
	}

	return "", fmt.Errorf("unsupported environment variable source")
}

// resolveSecretKeyRef resolves a value from a Kubernetes secret.
func (d *XEDriver) resolveSecretKeyRef(ref *v1.SecretKeySelector, namespace string) (string, error) {
	if d.secretLister == nil {
		return "", fmt.Errorf("secret lister not available")
	}

	secret, err := d.secretLister.Get(ref.Name)
	if err != nil {
		if ref.Optional != nil && *ref.Optional {
			return "", nil // Optional secret, return empty value
		}
		return "", fmt.Errorf("failed to get secret %s: %v", ref.Name, err)
	}

	value, exists := secret.Data[ref.Key]
	if !exists {
		if ref.Optional != nil && *ref.Optional {
			return "", nil // Optional key, return empty value
		}
		return "", fmt.Errorf("key %s not found in secret %s", ref.Key, ref.Name)
	}

	return string(value), nil
}

// resolveConfigMapKeyRef resolves a value from a Kubernetes configmap.
func (d *XEDriver) resolveConfigMapKeyRef(ref *v1.ConfigMapKeySelector, namespace string) (string, error) {
	if d.configMapLister == nil {
		return "", fmt.Errorf("configmap lister not available")
	}

	configMap, err := d.configMapLister.Get(ref.Name)
	if err != nil {
		if ref.Optional != nil && *ref.Optional {
			return "", nil // Optional configmap, return empty value
		}
		return "", fmt.Errorf("failed to get configmap %s: %v", ref.Name, err)
	}

	value, exists := configMap.Data[ref.Key]
	if !exists {
		if ref.Optional != nil && *ref.Optional {
			return "", nil // Optional key, return empty value
		}
		return "", fmt.Errorf("key %s not found in configmap %s", ref.Key, ref.Name)
	}

	return value, nil
}

// distributeRunOpts takes base options (labels) and environment options and distributes them
// across multiple RunOpts lines if they exceed the character limit per line.
func distributeRunOpts(baseOpts []string, envOpts []string) (map[uint16]*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss_RunOpts, error) {
	runOptsMap := make(map[uint16]*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss_RunOpts)

	var currentLine strings.Builder
	lineIndex := uint16(1)

	// Helper function to add a line to the map
	addLine := func() error {
		if currentLine.Len() == 0 {
			return nil
		}
		if lineIndex > MaxRunOptsLines {
			return fmt.Errorf("too many RunOpts lines needed (max %d), consider reducing environment variables", MaxRunOptsLines)
		}
		runOptsMap[lineIndex] = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss_RunOpts{
			LineIndex: ygot.Uint16(lineIndex),
			LineRunOpts: ygot.String(strings.TrimSpace(currentLine.String())),
		}
		lineIndex++
		currentLine.Reset()
		return nil
	}

	// Add option to current line, starting new line if needed
	addOption := func(opt string) error {
		needed := len(opt)
		if currentLine.Len() > 0 {
			needed += 1 // space separator
		}

		// If this option would exceed the line limit, start a new line
		if currentLine.Len() > 0 && currentLine.Len()+needed > MaxRunOptsLineLength {
			if err := addLine(); err != nil {
				return err
			}
		}

		// If even a single option is too long, that's an error
		if needed > MaxRunOptsLineLength {
			return fmt.Errorf("single RunOpts option too long (%d chars, max %d): %s", needed, MaxRunOptsLineLength, opt)
		}

		// Add the option to current line
		if currentLine.Len() > 0 {
			currentLine.WriteString(" ")
		}
		currentLine.WriteString(opt)
		return nil
	}

	// Add base options (labels) first
	for _, opt := range baseOpts {
		if err := addOption(opt); err != nil {
			return nil, err
		}
	}

	// Add environment options
	for _, opt := range envOpts {
		if err := addOption(opt); err != nil {
			return nil, err
		}
	}

	// Add the final line if not empty
	if err := addLine(); err != nil {
		return nil, err
	}

	// Ensure we have at least one line (even if empty, for backward compatibility)
	if len(runOptsMap) == 0 {
		runOptsMap[1] = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss_RunOpts{
			LineIndex: ygot.Uint16(1),
			LineRunOpts: ygot.String(""),
		}
	}

	return runOptsMap, nil
}
