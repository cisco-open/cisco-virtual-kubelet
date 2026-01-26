package iosxe

import (
	"context"
	"fmt"
	"net"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// networkConfig holds the network configuration for an app container
type networkConfig struct {
	virtualPortgroupInterface string
	virtualPortgroupIP        string
	virtualPortgroupNetmask   string
	defaultGateway            string
	gatewayInterface          uint8
}

// resourceConfig holds the resource allocation for an app container
type resourceConfig struct {
	cpuUnits uint16
	memoryMB uint16
	diskMB   uint16
	vcpu     uint16
}

// getNetworkConfig converts pod/container specs to IOS-XE network configuration
func (d *XEDriver) getNetworkConfig(pod *v1.Pod, container *v1.Container) *networkConfig {
	ip, netmask, gateway := d.allocateIPForContainer(pod, container)

	return &networkConfig{
		virtualPortgroupInterface: "0",
		virtualPortgroupIP:        ip,
		virtualPortgroupNetmask:   netmask,
		defaultGateway:            gateway,
		gatewayInterface:          0,
	}
}

// allocateIPForContainer determines the IP address for a container based on pod CIDR configuration
func (d *XEDriver) allocateIPForContainer(pod *v1.Pod, container *v1.Container) (ip, netmask, gateway string) {
	if d.config.Networking.PodCIDR != "" {
		_, ipNet, err := net.ParseCIDR(d.config.Networking.PodCIDR)
		if err == nil {
			netmask = net.IP(ipNet.Mask).String()
			gateway = d.getGatewayFromCIDR(ipNet)
			containerIndex := d.getContainerIndex(pod, container)
			ip = d.getIPForContainer(ipNet, containerIndex)
			return
		}
	}

	return "1.1.1.10", "255.255.255.0", "1.1.1.1"
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

// getGatewayFromCIDR calculates the gateway IP (first usable IP) from a CIDR
func (d *XEDriver) getGatewayFromCIDR(ipNet *net.IPNet) string {
	ip := ipNet.IP.To4()
	if ip == nil {
		return ""
	}
	ip[3] = ip[3] + 1
	return ip.String()
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
		if storage := container.Resources.Requests.Storage(); storage != nil && !storage.IsZero() {
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

// GetContainerStatus maps IOS-XE app operational data to Kubernetes container statuses
func (d *XEDriver) GetContainerStatus(ctx context.Context, pod *v1.Pod,
	discoveredContainers map[string]string,
	appOperData map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App) error {

	now := metav1.Now()

	pod.Status = v1.PodStatus{
		Phase:     v1.PodPending,
		HostIP:    "1.1.1.2",
		PodIP:     "1.1.1.1",
		StartTime: &now,
		Conditions: []v1.PodCondition{
			{
				Type:               v1.PodInitialized,
				Status:             v1.ConditionFalse,
				LastTransitionTime: now,
			},
			{
				Type:               v1.PodReady,
				Status:             v1.ConditionFalse,
				LastTransitionTime: now,
			},
			{
				Type:               v1.PodScheduled,
				Status:             v1.ConditionTrue,
				LastTransitionTime: now,
			},
		},
	}

	allReady := true
	anyRunning := false

	for containerName, appID := range discoveredContainers {
		var containerSpec *v1.Container
		for i := range pod.Spec.Containers {
			if pod.Spec.Containers[i].Name == containerName {
				containerSpec = &pod.Spec.Containers[i]
				break
			}
		}

		if containerSpec == nil {
			log.G(ctx).Warnf("Container spec not found for %s (appID: %s)", containerName, appID)
			continue
		}

		operData := appOperData[appID]

		containerStatus := v1.ContainerStatus{
			Name:        containerName,
			Image:       containerSpec.Image,
			ImageID:     containerSpec.Image,
			ContainerID: fmt.Sprintf("cisco://%s", appID),
			Ready:       false,
		}

		if operData != nil && operData.Details != nil && operData.Details.State != nil {
			state := *operData.Details.State

			switch state {
			case "RUNNING":
				containerStatus.State = v1.ContainerState{
					Running: &v1.ContainerStateRunning{
						StartedAt: now,
					},
				}
				containerStatus.Ready = true
				anyRunning = true
			case "DEPLOYED", "Activated":
				containerStatus.State = v1.ContainerState{
					Waiting: &v1.ContainerStateWaiting{
						Reason:  "ContainerCreating",
						Message: fmt.Sprintf("App state: %s", state),
					},
				}
				allReady = false
			case "STOPPED", "Uninstalled":
				containerStatus.State = v1.ContainerState{
					Terminated: &v1.ContainerStateTerminated{
						ExitCode:   0,
						Reason:     "Completed",
						FinishedAt: now,
					},
				}
				allReady = false
			default:
				containerStatus.State = v1.ContainerState{
					Waiting: &v1.ContainerStateWaiting{
						Reason:  "Unknown",
						Message: fmt.Sprintf("App state: %s", state),
					},
				}
				allReady = false
			}

			log.G(ctx).Infof("Container %s (app: %s) state: %s, ready: %v",
				containerName, appID, state, containerStatus.Ready)
		} else {
			containerStatus.State = v1.ContainerState{
				Waiting: &v1.ContainerStateWaiting{
					Reason:  "ContainerCreating",
					Message: "No operational data available",
				},
			}
			allReady = false
			log.G(ctx).Warnf("No operational data for container %s (app: %s)", containerName, appID)
		}

		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, containerStatus)
	}

	if anyRunning && allReady {
		pod.Status.Phase = v1.PodRunning
		for i := range pod.Status.Conditions {
			if pod.Status.Conditions[i].Type == v1.PodReady ||
				pod.Status.Conditions[i].Type == v1.PodInitialized {
				pod.Status.Conditions[i].Status = v1.ConditionTrue
			}
		}
	} else if anyRunning {
		pod.Status.Phase = v1.PodRunning
	}

	log.G(ctx).Infof("Pod %s/%s status: Phase=%s, Containers=%d/%d ready",
		pod.Namespace, pod.Name, pod.Status.Phase,
		len(pod.Status.ContainerStatuses), len(pod.Spec.Containers))

	return nil
}
