package common

import (
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

type ResourceUsage struct {
	CPU       resource.Quantity `json:"cpu" yaml:"cpu"`
	Memory    resource.Quantity `json:"memory" yaml:"memory"`
	Storage   resource.Quantity `json:"storage" yaml:"storage"`
	NetworkRx int64             `json:"networkRx" yaml:"networkRx"`
	NetworkTx int64             `json:"networkTx" yaml:"networkTx"`
	Timestamp metav1.Time       `json:"timestamp" yaml:"timestamp"`
}
