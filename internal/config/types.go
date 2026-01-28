package config

import (
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type DeviceDriver string

type Config struct {
	// Kubelet tier: Standard Virtual Kubelet settings
	Kubelet KubeletConfig `mapstructure:"kubelet"`

	// Device tier: Abstracted Cisco-specific settings
	Device DeviceConfig `mapstructure:"device"`
}

type KubeletConfig struct {
	NodeName        string `mapstructure:"node_name"`
	Namespace       string `mapstructure:"namespace"`
	UpdateInterval  string `mapstructure:"update_interval"`
	OperatingSystem string `mapstructure:"os"`
	NodeInternalIP  string `mapstructure:"node_internal_ip"`
	// NodeLabels      map[string]string `mapstructure:"node_labels"`
}

type DeviceConfig struct {
	Name           string            `mapstructure:"name"`
	Driver         DeviceDriver      `mapstructure:"driver"`
	Address        string            `mapstructure:"address"`
	Port           int               `mapstructure:"port"`
	Username       string            `mapstructure:"username"`
	Password       string            `mapstructure:"password"`
	TLSConfig      *TLSConfig        `mapstructure:"tls,omitempty"`
	Capabilities   DeviceCapability  `mapstructure:"capabilities"`
	Labels         map[string]string `mapstructure:"labels,omitempty"`
	Taints         []v1.Taint        `mapstructure:"taints,omitempty"`
	MaxPods        int32             `mapstructure:"maxPods"`
	Region         string            `mapstructure:"region,omitempty"`
	Zone           string            `mapstructure:"zone,omitempty"`
	Authentication AuthConfig        `mapstructure:"authentication"`
	ResourceLimits ResourceConfig    `mapstructure:"resourceLimits"`
	Networking     NetworkConfig     `mapstructure:"networking"`
	Monitoring     MonitoringConfig  `mapstructure:"monitoring"`
}

// TLSConfig represents TLS configuration for device communication
type TLSConfig struct {
	Enabled            bool   `mapstructure:"enabled"`
	InsecureSkipVerify bool   `mapstructure:"insecureSkipVerify"`
	CertFile           string `mapstructure:"certFile,omitempty"`
	KeyFile            string `mapstructure:"keyFile,omitempty"`
	CAFile             string `mapstructure:"caFile,omitempty"`
}

// AuthConfig represents authentication configuration
type AuthConfig struct {
	Method     string            `mapstructure:"method"` // certificate, password, token
	TokenFile  string            `mapstructure:"tokenFile,omitempty"`
	SecretName string            `mapstructure:"secretName,omitempty"`
	SecretKeys map[string]string `mapstructure:"secretKeys,omitempty"`
}

// ResourceConfig represents resource limits and defaults
type ResourceConfig struct {
	DefaultCPU     string            `mapstructure:"defaultCPU"`
	DefaultMemory  string            `mapstructure:"defaultMemory"`
	DefaultStorage string            `mapstructure:"defaultStorage"`
	MaxCPU         string            `mapstructure:"maxCPU"`
	MaxMemory      string            `mapstructure:"maxMemory"`
	MaxStorage     string            `mapstructure:"maxStorage"`
	Others         map[string]string `mapstructure:"others,omitempty"`
}

// NetworkConfig represents networking configuration
type NetworkConfig struct {
	DefaultVRF     string             `mapstructure:"defaultVRF"`
	PodCIDR        string             `mapstructure:"podCIDR"`
	DHCPEnabled    bool               `mapstructure:"dhcpEnabled"`
	ServiceCIDR    string             `mapstructure:"serviceCIDR"`
	DNSServers     []string           `mapstructure:"dnsServers"`
	VLANRange      VLANRange          `mapstructure:"vlanRange"`
	SecurityGroups []string           `mapstructure:"securityGroups,omitempty"`
	LoadBalancer   LoadBalancerConfig `mapstructure:"loadBalancer"`
}

// VLANRange represents VLAN ID range for pod isolation
type VLANRange struct {
	Start int `mapstructure:"start"`
	End   int `mapstructure:"end"`
}

// LoadBalancerConfig represents load balancer configuration
type LoadBalancerConfig struct {
	Enabled   bool     `mapstructure:"enabled"`
	Type      string   `mapstructure:"type"` // aci, native, external
	Addresses []string `mapstructure:"addresses,omitempty"`
	Algorithm string   `mapstructure:"algorithm"`
}

// MonitoringConfig represents monitoring and observability configuration
type MonitoringConfig struct {
	Enabled         bool          `mapstructure:"enabled"`
	MetricsPort     int           `mapstructure:"metricsPort"`
	HealthCheckPort int           `mapstructure:"healthCheckPort"`
	LogLevel        string        `mapstructure:"logLevel"`
	ScrapeInterval  time.Duration `mapstructure:"scrapeInterval"`
	RetentionPeriod time.Duration `mapstructure:"retentionPeriod"`
}

// DeviceCapability represents the capabilities of a Cisco device
type DeviceCapability struct {
	CPU              resource.Quantity `mapstructure:"cpu"`
	Memory           resource.Quantity `mapstructure:"memory"`
	Storage          resource.Quantity `mapstructure:"storage"`
	Pods             resource.Quantity `mapstructure:"pods"`
	ContainerRuntime string            `mapstructure:"containerRuntime"`
	IOxVersion       string            `mapstructure:"ioxVersion,omitempty"`
	SupportedArch    []string          `mapstructure:"supportedArch"`
	NetworkFeatures  NetworkFeatures   `mapstructure:"networkFeatures"`
}

// NetworkFeatures represents networking capabilities
type NetworkFeatures struct {
	VRFSupport       bool     `mapstructure:"vrfSupport"`
	VLANSupport      bool     `mapstructure:"vlanSupport"`
	ACLSupport       bool     `mapstructure:"aclSupport"`
	QoSSupport       bool     `mapstructure:"qosSupport"`
	LoadBalancing    bool     `mapstructure:"loadBalancing"`
	SupportedMTU     []int    `mapstructure:"supportedMTU"`
	RoutingProtocols []string `mapstructure:"routingProtocols"`
}
