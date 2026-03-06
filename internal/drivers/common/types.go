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

package common

import (
	"context"
	"encoding/xml"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Container struct {
	ID          string            `json:"id" yaml:"id"`
	Name        string            `json:"name" yaml:"name"`
	Image       string            `json:"image" yaml:"image"`
	State       ContainerState    `json:"state" yaml:"state"`
	DeviceID    string            `json:"deviceId" yaml:"deviceId"`
	NetworkID   string            `json:"networkId" yaml:"networkId"`
	Resources   ResourceUsage     `json:"resources" yaml:"resources"`
	Labels      map[string]string `json:"labels" yaml:"labels"`
	Annotations map[string]string `json:"annotations" yaml:"annotations"`
	CreatedAt   metav1.Time       `json:"createdAt" yaml:"createdAt"`
	StartedAt   *metav1.Time      `json:"startedAt,omitempty" yaml:"startedAt,omitempty"`
	FinishedAt  *metav1.Time      `json:"finishedAt,omitempty" yaml:"finishedAt,omitempty"`
}

// ContainerState represents the state of a container
type ContainerState string

const (
	ContainerStateCreated ContainerState = "created"
	ContainerStateRunning ContainerState = "running"
	ContainerStateStopped ContainerState = "stopped"
	ContainerStateExited  ContainerState = "exited"
	ContainerStateError   ContainerState = "error"
	ContainerStateUnknown ContainerState = "unknown"
)

type ClientAuth struct {
	Method   string
	Username string
	Password string
}

type ResourceUsage struct {
	CPU       resource.Quantity `json:"cpu" yaml:"cpu"`
	Memory    resource.Quantity `json:"memory" yaml:"memory"`
	Storage   resource.Quantity `json:"storage" yaml:"storage"`
	NetworkRx int64             `json:"networkRx" yaml:"networkRx"`
	NetworkTx int64             `json:"networkTx" yaml:"networkTx"`
	Timestamp metav1.Time       `json:"timestamp" yaml:"timestamp"`
}

// NetworkClient defines the generic interface for any backend (RESTconf, Netconf, etc.)
type NetworkClient interface {
	Get(ctx context.Context, path string, result any, unmarshal func([]byte, any) error) error
	Post(ctx context.Context, path string, payload any, marshal func(any) ([]byte, error)) error
	Patch(ctx context.Context, path string, payload any, marshal func(any) ([]byte, error)) error
	Delete(ctx context.Context, path string) error
}

type HostMeta struct {
	XMLName xml.Name `xml:"XRD"`
	Links   []Link   `xml:"Link"`
}

type Link struct {
	// Use ,attr to ensure it looks at the attributes of the current element
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

func (*HostMeta) IsYANGGoStruct() {}

// DeviceInfo contains device information fetched from the network device
type DeviceInfo struct {
	SerialNumber    string
	SoftwareVersion string
	ProductID       string
}

// AppHostingOperData contains global operational data including resources and notifications
type AppHostingOperData struct {
	IoxEnabled    bool
	SystemCPU     AppResource
	Memory        AppResource
	Storage       AppResource
	Notifications []AppNotification
}

type AppResource struct {
	Quota     int64
	Available int64
	Unit      string
}

type AppNotification struct {
	AppID     string
	Severity  string
	Message   string
	Timestamp string
}
