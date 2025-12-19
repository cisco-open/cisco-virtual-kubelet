package cisco

import (
	"context"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeviceType represents the type of Cisco device
type DeviceType string

const (
	DeviceTypeC9K  DeviceType = "c9k"  // Catalyst 9000 Series
	DeviceTypeC8K  DeviceType = "c8k"  // Catalyst 8000 Series
	DeviceTypeC8Kv DeviceType = "c8kv" // Catalyst 8000v Virtual
)

// CiscoConfig represents the configuration for the Cisco provider
type CiscoConfig struct {
	Devices        []DeviceConfig    `json:"devices" yaml:"devices"`
	Authentication AuthConfig       `json:"authentication" yaml:"authentication"`
	ResourceLimits ResourceConfig   `json:"resourceLimits" yaml:"resourceLimits"`
	Networking     NetworkConfig    `json:"networking" yaml:"networking"`
	Monitoring     MonitoringConfig `json:"monitoring" yaml:"monitoring"`
}

// DeviceConfig represents configuration for a single Cisco device
type DeviceConfig struct {
	Name             string            `json:"name" yaml:"name"`
	Type             DeviceType        `json:"type" yaml:"type"`
	Address          string            `json:"address" yaml:"address"`
	Port             int               `json:"port" yaml:"port"`
	Username         string            `json:"username" yaml:"username"`
	Password         string            `json:"password" yaml:"password"`
	TLSConfig        *TLSConfig        `json:"tls,omitempty" yaml:"tls,omitempty"`
	Capabilities     DeviceCapability  `json:"capabilities" yaml:"capabilities"`
	Labels           map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Taints           []v1.Taint        `json:"taints,omitempty" yaml:"taints,omitempty"`
	MaxPods          int32             `json:"maxPods" yaml:"maxPods"`
	Region           string            `json:"region,omitempty" yaml:"region,omitempty"`
	Zone             string            `json:"zone,omitempty" yaml:"zone,omitempty"`
}

// TLSConfig represents TLS configuration for device communication
type TLSConfig struct {
	Enabled            bool   `json:"enabled" yaml:"enabled"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify" yaml:"insecureSkipVerify"`
	CertFile           string `json:"certFile,omitempty" yaml:"certFile,omitempty"`
	KeyFile            string `json:"keyFile,omitempty" yaml:"keyFile,omitempty"`
	CAFile             string `json:"caFile,omitempty" yaml:"caFile,omitempty"`
}

// AuthConfig represents authentication configuration
type AuthConfig struct {
	Method       string            `json:"method" yaml:"method"` // certificate, password, token
	TokenFile    string            `json:"tokenFile,omitempty" yaml:"tokenFile,omitempty"`
	SecretName   string            `json:"secretName,omitempty" yaml:"secretName,omitempty"`
	SecretKeys   map[string]string `json:"secretKeys,omitempty" yaml:"secretKeys,omitempty"`
}

// ResourceConfig represents resource limits and defaults
type ResourceConfig struct {
	DefaultCPU     string            `json:"defaultCPU" yaml:"defaultCPU"`
	DefaultMemory  string            `json:"defaultMemory" yaml:"defaultMemory"`
	DefaultStorage string            `json:"defaultStorage" yaml:"defaultStorage"`
	MaxCPU         string            `json:"maxCPU" yaml:"maxCPU"`
	MaxMemory      string            `json:"maxMemory" yaml:"maxMemory"`
	MaxStorage     string            `json:"maxStorage" yaml:"maxStorage"`
	Others         map[string]string `json:"others,omitempty" yaml:"others,omitempty"`
}

// NetworkConfig represents networking configuration
type NetworkConfig struct {
	DefaultVRF      string            `json:"defaultVRF" yaml:"defaultVRF"`
	PodCIDR         string            `json:"podCIDR" yaml:"podCIDR"`
	ServiceCIDR     string            `json:"serviceCIDR" yaml:"serviceCIDR"`
	DNSServers      []string          `json:"dnsServers" yaml:"dnsServers"`
	VLANRange       VLANRange         `json:"vlanRange" yaml:"vlanRange"`
	SecurityGroups  []string          `json:"securityGroups,omitempty" yaml:"securityGroups,omitempty"`
	LoadBalancer    LoadBalancerConfig `json:"loadBalancer" yaml:"loadBalancer"`
}

// VLANRange represents VLAN ID range for pod isolation
type VLANRange struct {
	Start int `json:"start" yaml:"start"`
	End   int `json:"end" yaml:"end"`
}

// LoadBalancerConfig represents load balancer configuration
type LoadBalancerConfig struct {
	Enabled    bool     `json:"enabled" yaml:"enabled"`
	Type       string   `json:"type" yaml:"type"` // aci, native, external
	Addresses  []string `json:"addresses,omitempty" yaml:"addresses,omitempty"`
	Algorithm  string   `json:"algorithm" yaml:"algorithm"`
}

// MonitoringConfig represents monitoring and observability configuration
type MonitoringConfig struct {
	Enabled           bool          `json:"enabled" yaml:"enabled"`
	MetricsPort       int           `json:"metricsPort" yaml:"metricsPort"`
	HealthCheckPort   int           `json:"healthCheckPort" yaml:"healthCheckPort"`
	LogLevel          string        `json:"logLevel" yaml:"logLevel"`
	ScrapeInterval    time.Duration `json:"scrapeInterval" yaml:"scrapeInterval"`
	RetentionPeriod   time.Duration `json:"retentionPeriod" yaml:"retentionPeriod"`
}

// DeviceCapability represents the capabilities of a Cisco device
type DeviceCapability struct {
	CPU              resource.Quantity `json:"cpu" yaml:"cpu"`
	Memory           resource.Quantity `json:"memory" yaml:"memory"`
	Storage          resource.Quantity `json:"storage" yaml:"storage"`
	Pods             resource.Quantity `json:"pods" yaml:"pods"`
	ContainerRuntime string            `json:"containerRuntime" yaml:"containerRuntime"`
	IOxVersion       string            `json:"ioxVersion,omitempty" yaml:"ioxVersion,omitempty"`
	SupportedArch    []string          `json:"supportedArch" yaml:"supportedArch"`
	NetworkFeatures  NetworkFeatures   `json:"networkFeatures" yaml:"networkFeatures"`
}

// NetworkFeatures represents networking capabilities
type NetworkFeatures struct {
	VRFSupport       bool     `json:"vrfSupport" yaml:"vrfSupport"`
	VLANSupport      bool     `json:"vlanSupport" yaml:"vlanSupport"`
	ACLSupport       bool     `json:"aclSupport" yaml:"aclSupport"`
	QoSSupport       bool     `json:"qosSupport" yaml:"qosSupport"`
	LoadBalancing    bool     `json:"loadBalancing" yaml:"loadBalancing"`
	SupportedMTU     []int    `json:"supportedMTU" yaml:"supportedMTU"`
	RoutingProtocols []string `json:"routingProtocols" yaml:"routingProtocols"`
}

// Container represents a running container on a Cisco device
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
	ContainerStateCreated  ContainerState = "created"
	ContainerStateRunning  ContainerState = "running"
	ContainerStateStopped  ContainerState = "stopped"
	ContainerStateExited   ContainerState = "exited"
	ContainerStateError    ContainerState = "error"
	ContainerStateUnknown  ContainerState = "unknown"
)

// ContainerSpec represents the specification for creating a container
type ContainerSpec struct {
	Name         string                      `json:"name" yaml:"name"`
	Image        string                      `json:"image" yaml:"image"`
	Command      []string                    `json:"command,omitempty" yaml:"command,omitempty"`
	Args         []string                    `json:"args,omitempty" yaml:"args,omitempty"`
	Env          []v1.EnvVar                 `json:"env,omitempty" yaml:"env,omitempty"`
	Resources    v1.ResourceRequirements     `json:"resources" yaml:"resources"`
	VolumeMounts []v1.VolumeMount            `json:"volumeMounts,omitempty" yaml:"volumeMounts,omitempty"`
	Ports        []v1.ContainerPort          `json:"ports,omitempty" yaml:"ports,omitempty"`
	SecurityContext *v1.SecurityContext      `json:"securityContext,omitempty" yaml:"securityContext,omitempty"`
	LivenessProbe   *v1.Probe                `json:"livenessProbe,omitempty" yaml:"livenessProbe,omitempty"`
	ReadinessProbe  *v1.Probe                `json:"readinessProbe,omitempty" yaml:"readinessProbe,omitempty"`
	StartupProbe    *v1.Probe                `json:"startupProbe,omitempty" yaml:"startupProbe,omitempty"`
	Labels       map[string]string           `json:"labels,omitempty" yaml:"labels,omitempty"`
	Annotations  map[string]string           `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

// ResourceUsage represents resource usage statistics
type ResourceUsage struct {
	CPU        resource.Quantity `json:"cpu" yaml:"cpu"`
	Memory     resource.Quantity `json:"memory" yaml:"memory"`
	Storage    resource.Quantity `json:"storage" yaml:"storage"`
	NetworkRx  int64             `json:"networkRx" yaml:"networkRx"`
	NetworkTx  int64             `json:"networkTx" yaml:"networkTx"`
	Timestamp  metav1.Time       `json:"timestamp" yaml:"timestamp"`
}

// DeviceState represents the current state of a device
type DeviceState struct {
	Name         string         `json:"name" yaml:"name"`
	Status       DeviceStatus   `json:"status" yaml:"status"`
	Resources    ResourceUsage  `json:"resources" yaml:"resources"`
	Capacity     DeviceCapability `json:"capacity" yaml:"capacity"`
	Containers   []Container    `json:"containers" yaml:"containers"`
	LastSeen     metav1.Time    `json:"lastSeen" yaml:"lastSeen"`
	Version      string         `json:"version" yaml:"version"`
	Conditions   []DeviceCondition `json:"conditions" yaml:"conditions"`
}

// DeviceStatus represents the status of a device
type DeviceStatus string

const (
	DeviceStatusReady     DeviceStatus = "ready"
	DeviceStatusNotReady  DeviceStatus = "notReady"
	DeviceStatusUnknown   DeviceStatus = "unknown"
	DeviceStatusMaintenance DeviceStatus = "maintenance"
)

// DeviceCondition represents a condition of a device
type DeviceCondition struct {
	Type               string      `json:"type" yaml:"type"`
	Status             v1.ConditionStatus `json:"status" yaml:"status"`
	LastTransitionTime metav1.Time `json:"lastTransitionTime" yaml:"lastTransitionTime"`
	Reason             string      `json:"reason" yaml:"reason"`
	Message            string      `json:"message" yaml:"message"`
}

// NetworkNamespace represents a network namespace on a device
type NetworkNamespace struct {
	ID       string            `json:"id" yaml:"id"`
	Name     string            `json:"name" yaml:"name"`
	VRF      string            `json:"vrf" yaml:"vrf"`
	VLAN     int               `json:"vlan" yaml:"vlan"`
	CIDR     string            `json:"cidr" yaml:"cidr"`
	Gateway  string            `json:"gateway" yaml:"gateway"`
	DNSServers []string        `json:"dnsServers" yaml:"dnsServers"`
	Labels   map[string]string `json:"labels" yaml:"labels"`
	DeviceID string            `json:"deviceId" yaml:"deviceId"`
}

// ExecResult represents the result of executing a command
type ExecResult struct {
	ExitCode int    `json:"exitCode" yaml:"exitCode"`
	Stdout   string `json:"stdout" yaml:"stdout"`
	Stderr   string `json:"stderr" yaml:"stderr"`
}

// LogOptions represents options for retrieving container logs
type LogOptions struct {
	Follow     bool      `json:"follow" yaml:"follow"`
	Previous   bool      `json:"previous" yaml:"previous"`
	Timestamps bool      `json:"timestamps" yaml:"timestamps"`
	Since      *metav1.Time `json:"since,omitempty" yaml:"since,omitempty"`
	TailLines  *int64    `json:"tailLines,omitempty" yaml:"tailLines,omitempty"`
	LimitBytes *int64    `json:"limitBytes,omitempty" yaml:"limitBytes,omitempty"`
}

// ServiceEndpoint represents a service endpoint
type ServiceEndpoint struct {
	Name      string `json:"name" yaml:"name"`
	Namespace string `json:"namespace" yaml:"namespace"`
	IP        string `json:"ip" yaml:"ip"`
	Port      int32  `json:"port" yaml:"port"`
	Protocol  string `json:"protocol" yaml:"protocol"`
	DeviceID  string `json:"deviceId" yaml:"deviceId"`
}

// LoadBalancer represents a load balancer configuration
type LoadBalancer struct {
	Name      string            `json:"name" yaml:"name"`
	Type      string            `json:"type" yaml:"type"`
	Frontend  LoadBalancerFrontend `json:"frontend" yaml:"frontend"`
	Backend   []LoadBalancerBackend `json:"backend" yaml:"backend"`
	Algorithm string            `json:"algorithm" yaml:"algorithm"`
	DeviceID  string            `json:"deviceId" yaml:"deviceId"`
}

// LoadBalancerFrontend represents the frontend configuration
type LoadBalancerFrontend struct {
	IP       string `json:"ip" yaml:"ip"`
	Port     int32  `json:"port" yaml:"port"`
	Protocol string `json:"protocol" yaml:"protocol"`
}

// LoadBalancerBackend represents the backend configuration
type LoadBalancerBackend struct {
	IP     string `json:"ip" yaml:"ip"`
	Port   int32  `json:"port" yaml:"port"`
	Weight int    `json:"weight" yaml:"weight"`
	Health string `json:"health" yaml:"health"`
}

// SecurityPolicy represents a network security policy
type SecurityPolicy struct {
	Name      string              `json:"name" yaml:"name"`
	Namespace string              `json:"namespace" yaml:"namespace"`
	Rules     []SecurityPolicyRule `json:"rules" yaml:"rules"`
	DeviceID  string              `json:"deviceId" yaml:"deviceId"`
}

// SecurityPolicyRule represents a single security rule
type SecurityPolicyRule struct {
	Action     string   `json:"action" yaml:"action"` // allow, deny
	Direction  string   `json:"direction" yaml:"direction"` // ingress, egress
	Protocol   string   `json:"protocol" yaml:"protocol"`
	SourceCIDR string   `json:"sourceCidr,omitempty" yaml:"sourceCidr,omitempty"`
	DestCIDR   string   `json:"destCidr,omitempty" yaml:"destCidr,omitempty"`
	Ports      []int32  `json:"ports,omitempty" yaml:"ports,omitempty"`
}

// DeviceClient represents a client interface for communicating with devices
type DeviceClient interface {
	Connect() error
	Disconnect() error
	IsConnected() bool
	GetSystemInfo() (*SystemInfo, error)
	GetResourceUsage() (*ResourceUsage, error)
	ExecuteCommand(cmd string) (*ExecResult, error)
	
	// Container management methods
	CreateContainer(ctx context.Context, spec ContainerSpec) (*Container, error)
	DestroyContainer(ctx context.Context, containerID string) error
	GetContainer(ctx context.Context, containerID string) (*Container, error)
	ListContainers(ctx context.Context) ([]*Container, error)
}

// SystemInfo represents system information from a device
type SystemInfo struct {
	Hostname     string `json:"hostname" yaml:"hostname"`
	Version      string `json:"version" yaml:"version"`
	SerialNumber string `json:"serialNumber" yaml:"serialNumber"`
	Model        string `json:"model" yaml:"model"`
	Uptime       string `json:"uptime" yaml:"uptime"`
	CPUCount     int    `json:"cpuCount" yaml:"cpuCount"`
	MemoryTotal  int64  `json:"memoryTotal" yaml:"memoryTotal"`
	StorageTotal int64  `json:"storageTotal" yaml:"storageTotal"`
}

// DiscoveredEndpoints contains the actual working endpoints for a specific IOS XE version
// This allows the provider to adapt to different schema versions dynamically
type DiscoveredEndpoints struct {
	IOxConfigPath          string   `json:"ioxConfigPath,omitempty"`
	IOxOperPath           string   `json:"ioxOperPath,omitempty"`
	AppHostingConfigPath  string   `json:"appHostingConfigPath,omitempty"`
	AppHostingOperPath    string   `json:"appHostingOperPath,omitempty"`
	VirtualServicePath    string   `json:"virtualServicePath,omitempty"`
	ContainerConfigPath   string   `json:"containerConfigPath,omitempty"`
	SupportedOperations   []string `json:"supportedOperations,omitempty"`
}
