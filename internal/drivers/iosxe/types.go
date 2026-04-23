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
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
)

// networkConfig holds the network configuration for an app container
type networkConfig struct {
	interfaceType             v1alpha1.XEInterfaceType
	virtualPortgroupInterface string
	virtualPortgroupIP        string
	virtualPortgroupNetmask   string
	guestInterface            uint8
	isPrimaryContainer        bool
	// AppGigabitEthernet specific
	appGigMode           v1alpha1.XEAppGigabitEthernetMode
	appGigGuestInterface uint8
	vlanIf               v1alpha1.XEVlanInterfaceConfig
	useDHCP              bool
	ipAllocationErr      error
	// Management specific
	mgmtGuestInterface uint8
	mgmtGuestIPv4      string
	mgmtGuestIPv4Mask  string
}

// resourceConfig holds the resource allocation for an app container
type resourceConfig struct {
	cpuUnits uint16
	memoryMB uint16
	diskMB   uint16
	vcpu     uint16
}

// AppDesiredState represents the desired lifecycle state of an app on the device.
type AppDesiredState string

const (
	// AppDesiredStateRunning means the app should be fully deployed and running.
	AppDesiredStateRunning AppDesiredState = "Running"
	// AppDesiredStateDeleted means the app should be fully removed from the device.
	AppDesiredStateDeleted AppDesiredState = "Deleted"
)

// AppPhase summarises where the reconciler is in converging an app toward its desired state.
type AppPhase string

const (
	AppPhaseConverging AppPhase = "Converging"
	AppPhaseReady      AppPhase = "Ready"
	AppPhaseDeleting   AppPhase = "Deleting"
	AppPhaseDeleted    AppPhase = "Deleted"
	AppPhaseError      AppPhase = "Error"
)

// AppHostingMetadata identifies the app and the Kubernetes objects it belongs to.
type AppHostingMetadata struct {
	AppName       string
	ContainerName string
	PodName       string
	PodNamespace  string
	PodUID        string
	Pod           *v1.Pod // set by ConvertPodToAppConfigs; used for event recording
}

// AppHostingSpec declares the desired state and the device configuration payload.
type AppHostingSpec struct {
	ImagePath        string
	DesiredState     AppDesiredState
	DeviceConfig     *Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps // YANG config payload
	ImagePullPolicy  v1.PullPolicy
	PackageDest      string                    // on-device flash path (from annotation); empty = use default
	PackageTimeout   time.Duration             // 0 = use default (180s)
	ImagePullSecrets []v1.LocalObjectReference // from pod.Spec.ImagePullSecrets
}

// AppHostingStatus captures the last-observed device state and reconciler phase.
type AppHostingStatus struct {
	ObservedState  string    // Device oper state: "", "DEPLOYED", "ACTIVATED", "RUNNING", etc.
	ConfigPresent  bool      // Whether config exists on the device
	Phase          AppPhase  // Reconciler phase
	Message        string    // Human-readable message for last transition
	LastTransition time.Time // Timestamp of the last status change
}

// AppHostingConfig represents a complete IOS-XE AppHosting configuration for a single container,
// modelled after the Kubernetes resource pattern (metadata + spec + status).
type AppHostingConfig struct {
	Metadata AppHostingMetadata
	Spec     AppHostingSpec
	Status   AppHostingStatus
}

// AppName is a convenience accessor.
func (c *AppHostingConfig) AppName() string { return c.Metadata.AppName }

// ContainerName is a convenience accessor.
func (c *AppHostingConfig) ContainerName() string { return c.Metadata.ContainerName }

// ImagePath is a convenience accessor.
func (c *AppHostingConfig) ImagePath() string { return c.Spec.ImagePath }

// PodUID is a convenience accessor.
func (c *AppHostingConfig) PodUID() string { return c.Metadata.PodUID }

// ImagePullPolicy is a convenience accessor.
func (c *AppHostingConfig) ImagePullPolicy() v1.PullPolicy { return c.Spec.ImagePullPolicy }

// PackageDest is a convenience accessor.
func (c *AppHostingConfig) PackageDest() string { return c.Spec.PackageDest }

// PackageTimeout is a convenience accessor.
func (c *AppHostingConfig) PackageTimeout() time.Duration { return c.Spec.PackageTimeout }

// ImagePullSecrets is a convenience accessor.
func (c *AppHostingConfig) ImagePullSecrets() []v1.LocalObjectReference {
	return c.Spec.ImagePullSecrets
}
