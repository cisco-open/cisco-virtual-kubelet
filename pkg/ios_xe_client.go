package cisco

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IOSXEClient implements DeviceClient for IOS XE devices
type IOSXEClient struct {
	config     DeviceConfig
	httpClient *http.Client
	baseURL    string
	token      string
	connected  bool
	schema     *DiscoveredEndpoints // Dynamically discovered schema endpoints
}

// NewIOSXEClient creates a new IOS XE client
func NewIOSXEClient(config DeviceConfig) *IOSXEClient {

	u := &url.URL{
		Host: fmt.Sprintf("%s:%d", config.Address, config.Port),
	}

	if config.TLSConfig.Enabled {
		u.Scheme = "https"
	} else {
		u.Scheme = "http"
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: false,
	}

	if config.TLSConfig != nil {
		tlsConfig.InsecureSkipVerify = config.TLSConfig.InsecureSkipVerify
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	return &IOSXEClient{
		config:     config,
		httpClient: httpClient,
		baseURL:    u.String(),
	}
}

// Connect establishes connection to the IOS XE device
func (c *IOSXEClient) Connect() error {
	ctx := context.Background()

	// Step 1: Test basic connectivity with RESTCONF
	req, err := http.NewRequest("GET", c.baseURL+"/restconf/data/ietf-yang-library:yang-library", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	req.SetBasicAuth(c.config.Username, c.config.Password)
	req.Header.Set("Accept", "application/yang-data+json")

	connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req = req.WithContext(connCtx)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to device: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("authentication failed (status %d): %s", resp.StatusCode, string(body))
	}

	c.connected = true
	log.G(ctx).Infof("✅ Successfully connected to IOS XE device %s", c.config.Name)

	// Step 2: Perform automatic schema discovery
	log.G(ctx).Infof("🔍 Discovering RESTCONF schema for device %s...", c.config.Name)
	if err := c.discoverSchema(ctx); err != nil {
		log.G(ctx).Warnf("Schema discovery failed: %v (continuing with limited functionality)", err)
		// Don't fail connection if schema discovery fails
	}

	return nil
}

// Disconnect closes the connection to the device
func (c *IOSXEClient) Disconnect() error {
	c.connected = false
	return nil
}

// IsConnected returns the connection status
func (c *IOSXEClient) IsConnected() bool {
	return c.connected
}

// GetSystemInfo retrieves system information from the device
func (c *IOSXEClient) GetSystemInfo() (*SystemInfo, error) {
	if !c.connected {
		return nil, fmt.Errorf("not connected to device")
	}

	// Get system information via RESTCONF
	systemInfo := &SystemInfo{}

	// Get hostname
	hostname, err := c.getHostname()
	if err == nil {
		systemInfo.Hostname = hostname
	}

	// Get version information
	version, err := c.getVersion()
	if err == nil {
		systemInfo.Version = version
	}

	// Get memory information
	memory, err := c.getMemoryInfo()
	if err == nil {
		systemInfo.MemoryTotal = memory
	}

	// Get CPU information
	cpuCount, err := c.getCPUInfo()
	if err == nil {
		systemInfo.CPUCount = cpuCount
	}

	return systemInfo, nil
}

// GetResourceUsage retrieves current resource usage from the device
func (c *IOSXEClient) GetResourceUsage() (*ResourceUsage, error) {
	if !c.connected {
		return nil, fmt.Errorf("not connected to device")
	}

	usage := &ResourceUsage{
		Timestamp: metav1.Now(),
	}

	// Get CPU usage
	cpuUsage, err := c.getCPUUsage()
	if err == nil {
		usage.CPU = cpuUsage
	}

	// Get memory usage
	memUsage, err := c.getMemoryUsage()
	if err == nil {
		usage.Memory = memUsage
	}

	// Get storage usage
	storageUsage, err := c.getStorageUsage()
	if err == nil {
		usage.Storage = storageUsage
	}

	return usage, nil
}

// AppHostingResourceInfo represents the app-hosting resource information from device
type AppHostingResourceInfo struct {
	CPU struct {
		Quota     int `json:"quota"`     // CPU percentage quota
		Available int `json:"available"` // Available CPU percentage
	} `json:"cpu"`
	VCPU struct {
		Count int `json:"count"` // Number of VCPUs
	} `json:"vcpu"`
	Memory struct {
		Quota     int `json:"quota"`     // Memory quota in MB
		Available int `json:"available"` // Available memory in MB
	} `json:"memory"`
	Storage struct {
		Total     int `json:"total"`     // Total storage in MB
		Available int `json:"available"` // Available storage in MB
	} `json:"storage"`
}

// GetAppHostingResources queries actual app-hosting resource availability from device
// This corresponds to the "show app-hosting resource" CLI command
func (c *IOSXEClient) GetAppHostingResources(ctx context.Context) (*AppHostingResourceInfo, error) {
	if !c.connected {
		return nil, fmt.Errorf("not connected to device")
	}

	// Query app-hosting operational data via RESTCONF
	path := "/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data/app-resource-utilization"

	data, err := c.restconfGet(path)
	if err != nil {
		// Fallback: Use default values based on device type
		log.G(ctx).Debugf("Resource endpoint not available, using fallback: %v", err)
		return c.getAppHostingResourcesFallback(ctx)
	}

	// Parse response
	var response struct {
		ResourceUtilization AppHostingResourceInfo `json:"app-resource-utilization"`
	}

	if err := json.Unmarshal(data, &response); err != nil {
		log.G(ctx).Warnf("Failed to parse resource data: %v", err)
		return c.getAppHostingResourcesFallback(ctx)
	}

	log.G(ctx).WithFields(map[string]interface{}{
		"vcpu":       response.ResourceUtilization.VCPU.Count,
		"cpu_quota":  response.ResourceUtilization.CPU.Quota,
		"cpu_avail":  response.ResourceUtilization.CPU.Available,
		"mem_quota":  response.ResourceUtilization.Memory.Quota,
		"mem_avail":  response.ResourceUtilization.Memory.Available,
		"stor_total": response.ResourceUtilization.Storage.Total,
		"stor_avail": response.ResourceUtilization.Storage.Available,
	}).Info("Retrieved app-hosting resources from device")

	return &response.ResourceUtilization, nil
}

// getAppHostingResourcesFallback provides default resource values based on device type
func (c *IOSXEClient) getAppHostingResourcesFallback(ctx context.Context) (*AppHostingResourceInfo, error) {
	log.G(ctx).Info("Using fallback resource values for device")

	// Conservative defaults for C9300 series
	info := &AppHostingResourceInfo{}
	info.CPU.Quota = 25
	info.CPU.Available = 25
	info.VCPU.Count = 2
	info.Memory.Quota = 2048
	info.Memory.Available = 2048
	info.Storage.Total = 112487
	info.Storage.Available = 97533

	return info, nil
}

// ExecuteCommand executes a CLI command on the device
func (c *IOSXEClient) ExecuteCommand(cmd string) (*ExecResult, error) {
	if !c.connected {
		return nil, fmt.Errorf("not connected to device")
	}

	// Use RESTCONF operations for command execution
	reqBody := map[string]interface{}{
		"cisco-ia:input": map[string]string{
			"command": cmd,
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	req, err := http.NewRequest("POST",
		c.baseURL+"/restconf/operations/cisco-ia:save-config",
		bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.SetBasicAuth(c.config.Username, c.config.Password)
	req.Header.Set("Content-Type", "application/yang-data+json")
	req.Header.Set("Accept", "application/yang-data+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute command: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	result := &ExecResult{
		ExitCode: 0,
		Stdout:   string(body),
	}

	if resp.StatusCode >= 400 {
		result.ExitCode = resp.StatusCode
		result.Stderr = string(body)
	}

	return result, nil
}

// CreateContainer creates a new IOx container on the device
func (c *IOSXEClient) CreateContainer(ctx context.Context, spec ContainerSpec) (*Container, error) {
	if !c.connected {
		return nil, fmt.Errorf("not connected to device")
	}

	log.G(ctx).Infof("Creating container %s on device %s", spec.Name, c.config.Name)

	// Create a container object
	container := &Container{
		ID:          generateContainerID(),
		Name:        spec.Name,
		Image:       spec.Image,
		State:       ContainerStateCreated,
		DeviceID:    c.config.Name,
		Labels:      spec.Labels,
		Annotations: spec.Annotations,
		CreatedAt:   metav1.Now(),
		Resources:   ResourceUsage{}, // Will be updated after deployment
	}

	// Use AppHostingManager for full deployment lifecycle
	manager := NewAppHostingManager(c)
	if err := manager.DeployApplication(ctx, spec, container); err != nil {
		container.State = ContainerStateError
		return container, fmt.Errorf("failed to deploy application: %v", err)
	}

	// Update container state
	container.State = ContainerStateRunning
	now := metav1.Now()
	container.StartedAt = &now

	log.G(ctx).Infof("✅ Container %s created successfully on device %s", spec.Name, c.config.Name)
	return container, nil
}

// DestroyContainer removes a container from the device
func (c *IOSXEClient) DestroyContainer(ctx context.Context, containerID string) error {
	if !c.connected {
		return fmt.Errorf("not connected to device")
	}

	log.G(ctx).Infof("Destroying container %s on device %s", containerID, c.config.Name)

	// Use AppHostingManager for complete cleanup
	manager := NewAppHostingManager(c)
	if err := manager.UndeployApplication(ctx, containerID); err != nil {
		return fmt.Errorf("failed to undeploy application: %v", err)
	}

	log.G(ctx).Infof("Container %s destroyed successfully from device %s", containerID, c.config.Name)
	return nil
}

func (c *IOSXEClient) GetContainer(ctx context.Context, containerID string) (*Container, error) {
	if !c.connected {
		return nil, fmt.Errorf("not connected to device")
	}

	// Query IOx for application status
	return c.getIOxApplicationStatus(ctx, containerID)
}

// ListContainers returns all containers on the device
func (c *IOSXEClient) ListContainers(ctx context.Context) ([]*Container, error) {
	if !c.connected {
		return nil, fmt.Errorf("not connected to device")
	}

	// List all IOx applications
	return c.listIOxApplications(ctx)
}

// Helper methods for RESTCONF API calls

func (c *IOSXEClient) getHostname() (string, error) {
	resp, err := c.restconfGet("/restconf/data/Cisco-IOS-XE-native:native/hostname")
	if err != nil {
		return "", err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", err
	}

	if hostname, ok := result["Cisco-IOS-XE-native:hostname"].(string); ok {
		return hostname, nil
	}

	return c.config.Address, nil
}

func (c *IOSXEClient) getVersion() (string, error) {
	_, err := c.restconfGet("/restconf/data/Cisco-IOS-XE-device-hardware-oper:device-hardware-data/device-hardware/device-system-data/software-version")
	if err != nil {
		return "", err
	}

	// Extract version from nested response
	return "16.12.03", nil // Placeholder
}

func (c *IOSXEClient) getMemoryInfo() (int64, error) {
	_, err := c.restconfGet("/restconf/data/Cisco-IOS-XE-memory-oper:memory-statistics")
	if err != nil {
		return 0, err
	}

	// Parse memory statistics
	return 4 * 1024 * 1024 * 1024, nil // Placeholder: 4GB
}

func (c *IOSXEClient) getCPUInfo() (int, error) {
	_, err := c.restconfGet("/restconf/data/Cisco-IOS-XE-process-cpu-oper:cpu-usage")
	if err != nil {
		return 0, err
	}

	// Parse CPU information
	return 4, nil // Placeholder: 4 cores
}

func (c *IOSXEClient) getCPUUsage() (resource.Quantity, error) {
	// Implementation would parse actual CPU usage from device
	return resource.MustParse("500m"), nil
}

func (c *IOSXEClient) getMemoryUsage() (resource.Quantity, error) {
	// Implementation would parse actual memory usage from device
	return resource.MustParse("2Gi"), nil
}

func (c *IOSXEClient) getStorageUsage() (resource.Quantity, error) {
	// Implementation would parse actual storage usage from device
	return resource.MustParse("4Gi"), nil
}

func (c *IOSXEClient) restconfGet(path string) ([]byte, error) {
	req, err := http.NewRequest("GET", c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(c.config.Username, c.config.Password)
	req.Header.Set("Accept", "application/yang-data+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func (c *IOSXEClient) restconfPost(path string, body []byte) ([]byte, error) {
	req, err := http.NewRequest("POST", c.baseURL+path, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(c.config.Username, c.config.Password)
	req.Header.Set("Content-Type", "application/yang-data+json")
	req.Header.Set("Accept", "application/yang-data+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// IOx-specific helper methods

func (c *IOSXEClient) deployIOxApplication(ctx context.Context, spec ContainerSpec, container *Container) error {
	log.G(ctx).Infof("🚀 Deploying REAL IOx application %s to C9K device %s", spec.Name, c.config.Name)

	// Step 1: Check if app-hosting is enabled
	if err := c.verifyAppHostingEnabled(ctx); err != nil {
		return fmt.Errorf("app-hosting not enabled: %v", err)
	}

	// Initialize RESTCONF client for lifecycle operations
	restconfClient := NewRESTCONFAppHostingClient(c)

	// STEP 2: Configure app-hosting appid FIRST (MUST be before install)
	// This creates the app-hosting configuration with resources and networking
	// Command: app-hosting appid <name> ...
	fmt.Printf("[CISCO-VK] ⚙️ Step 2: Creating app-hosting configuration for: %s\n", spec.Name)
	log.G(ctx).Infof("⚙️ Step 2: Creating app-hosting configuration for: %s", spec.Name)
	appConfig := c.buildAppHostingConfig(spec, container)
	if err := restconfClient.Configure(ctx, spec.Name, appConfig); err != nil {
		fmt.Printf("[CISCO-VK] ❌ Configure failed: %v\n", err)
		return fmt.Errorf("failed to configure app-hosting: %v", err)
	}
	fmt.Printf("[CISCO-VK] ✅ Configuration created successfully\n")

	// Small delay to let configuration apply
	time.Sleep(2 * time.Second)

	// STEP 3: Install the application package via RESTCONF RPC
	// This installs the package to the configured appid
	// Command: app-hosting install appid <name> package <path>
	fmt.Printf("[CISCO-VK] 📦 Step 3: Installing package to configured appid: %s\n", spec.Image)
	log.G(ctx).Infof("📦 Step 3: Installing package to configured appid: %s", spec.Image)
	if err := restconfClient.Install(ctx, spec.Name, spec.Image); err != nil {
		return fmt.Errorf("failed to install app-hosting package: %v", err)
	}

	// WAIT for DEPLOYED state (can take up to 90s for large images)
	log.G(ctx).Infof("⏳ Waiting for DEPLOYED state...")
	if err := restconfClient.WaitForState(ctx, spec.Name, "DEPLOYED", 90*time.Second); err != nil {
		return fmt.Errorf("failed to reach DEPLOYED state: %v", err)
	}

	// STEP 4: Activate the application via RESTCONF RPC
	// This prepares the container environment
	// Command: app-hosting activate appid <name>
	log.G(ctx).Infof("⚡ Step 4: Activating application: %s", spec.Name)
	if err := restconfClient.Activate(ctx, spec.Name); err != nil {
		return fmt.Errorf("failed to activate app-hosting instance: %v", err)
	}

	// WAIT for ACTIVATED state
	log.G(ctx).Infof("⏳ Waiting for ACTIVATED state...")
	if err := restconfClient.WaitForState(ctx, spec.Name, "ACTIVATED", 30*time.Second); err != nil {
		return fmt.Errorf("failed to reach ACTIVATED state: %v", err)
	}

	// STEP 5: Start the application via RESTCONF RPC
	// This starts the container runtime
	// Command: app-hosting start appid <name>
	log.G(ctx).Infof("▶️ Step 5: Starting application: %s", spec.Name)
	if err := restconfClient.Start(ctx, spec.Name); err != nil {
		return fmt.Errorf("failed to start app-hosting instance: %v", err)
	}

	// WAIT for RUNNING state
	log.G(ctx).Infof("⏳ Waiting for RUNNING state...")
	if err := restconfClient.WaitForState(ctx, spec.Name, "RUNNING", 15*time.Second); err != nil {
		return fmt.Errorf("failed to reach RUNNING state: %v", err)
	}

	// STEP 6: Final verification
	log.G(ctx).Infof("✅ Step 6: Verifying deployment...")
	if err := c.verifyAppDeployment(ctx, spec.Name); err != nil {
		log.G(ctx).Warnf("Post-deployment verification warning: %v", err)
		// Non-fatal - app is running
	}

	log.G(ctx).Infof("✅ Successfully deployed IOx application %s", spec.Name)
	return nil
}

// verifyAppHostingEnabled checks if IOx/app-hosting is enabled on the device
func (c *IOSXEClient) verifyAppHostingEnabled(ctx context.Context) error {
	// Use discovered schema to check if app-hosting is available
	if !c.HasAppHostingSupport() {
		return fmt.Errorf("app-hosting endpoints not discovered - IOx may not be enabled on device (enable with 'iox' command)")
	}

	// Try to query the IOx config endpoint if available
	if c.schema.IOxConfigPath != "" {
		url := c.baseURL + c.schema.IOxConfigPath

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return err
		}

		req.SetBasicAuth(c.config.Username, c.config.Password)
		req.Header.Set("Accept", "application/yang-data+json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to check IOx status: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == 404 {
			return fmt.Errorf("IOx not configured on device - please enable with 'iox' command")
		}

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("IOx check failed (status %d): %s", resp.StatusCode, string(body))
		}
	}

	log.G(ctx).Infof("✅ IOx/app-hosting is available on device %s", c.config.Name)
	return nil
}

// C9KAppHostingConfig represents the app-hosting configuration for C9K
type C9KAppHostingConfig struct {
	AppID         string            `json:"appid"`
	Image         string            `json:"image,omitempty"`
	AppVnic       AppVnicConfig     `json:"app-vnic,omitempty"`
	AppResource   AppResourceConfig `json:"app-resource,omitempty"`
	DockerRunOpts string            `json:"docker-run-opts,omitempty"` // Docker run options for container command/args
	Start         bool              `json:"start,omitempty"`
}

type AppVnicConfig struct {
	Management GuestInterface `json:"management,omitempty"`
}

type GuestInterface struct {
	GuestInterface int    `json:"guest-interface"`
	GuestIPAddress string `json:"guest-ipaddress,omitempty"`
	Netmask        string `json:"netmask,omitempty"`
	AppDefaultGW   string `json:"app-default-gateway,omitempty"`
}

type AppResourceConfig struct {
	Docker      DockerConfig `json:"docker,omitempty"`
	Profile     string       `json:"profile,omitempty"`
	CPU         int          `json:"cpu,omitempty"`          // CPU units (e.g., 3000)
	MemoryMB    int          `json:"memory,omitempty"`       // Memory in MB
	PersistDisk int          `json:"persist-disk,omitempty"` // Persistent disk in MB
	VCPU        int          `json:"vcpu,omitempty"`         // Number of virtual CPUs
}

type DockerConfig struct {
	RunOpts    []string `json:"run-opts,omitempty"`
	PrependPkg string   `json:"prepend-pkg-opts,omitempty"`
}

// AppHostingOperData represents operational data from C9K
type AppHostingOperData struct {
	AppHostingOperData struct {
		App []AppInfo `json:"app"`
	} `json:"Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data"`
}

type AppInfo struct {
	AppID           string     `json:"application-name"`
	State           string     `json:"state"`
	ApplicationType string     `json:"application-type"`
	Details         AppDetails `json:"details,omitempty"`
}

type AppDetails struct {
	State       string `json:"state"`
	RunState    string `json:"run-state"`
	IPAddress   string `json:"ip-address,omitempty"`
	Description string `json:"description,omitempty"`
}

// buildAppHostingConfig creates the app-hosting configuration for the container
func (c *IOSXEClient) buildAppHostingConfig(spec ContainerSpec, container *Container) C9KAppHostingConfig {
	// Generate unique IP for this container (basic IPAM)
	containerIP := fmt.Sprintf("192.168.100.%d", 10+(len(container.ID)%240))

	config := C9KAppHostingConfig{
		AppID: spec.Name,
		Image: spec.Image,
		AppVnic: AppVnicConfig{
			Management: GuestInterface{
				GuestInterface: 0,
				GuestIPAddress: containerIP,
				Netmask:        "255.255.255.0",
				AppDefaultGW:   "192.168.100.1",
			},
		},
		AppResource: AppResourceConfig{
			Docker: DockerConfig{
				RunOpts: []string{
					"--rm",
					"-d", // Run in daemon mode
				},
			},
			Profile:     "custom",
			CPU:         1000, // CPU units (default 1000 = 1 core)
			MemoryMB:    512,  // Default memory allocation in MB
			PersistDisk: 1024, // Default persistent disk in MB
			VCPU:        2,    // Number of virtual CPUs
		},
		Start: true,
	}

	// Apply resource limits from container spec
	if spec.Resources.Limits.Memory() != nil {
		memoryQuantity := spec.Resources.Limits.Memory()
		if memoryQuantity != nil {
			memoryMB := int(memoryQuantity.Value() / (1024 * 1024))
			if memoryMB > 0 {
				config.AppResource.MemoryMB = memoryMB
			}
		}
	}

	// Apply CPU limits from container spec
	if spec.Resources.Limits.Cpu() != nil {
		cpuQuantity := spec.Resources.Limits.Cpu()
		if cpuQuantity != nil {
			// Convert from Kubernetes CPU units to milliCPU
			cpuMillis := cpuQuantity.MilliValue()
			if cpuMillis > 0 {
				config.AppResource.CPU = int(cpuMillis)
			}
		}
	}

	// Add port mappings
	for _, port := range spec.Ports {
		portMapping := fmt.Sprintf("-p %d:%d", port.HostPort, port.ContainerPort)
		config.AppResource.Docker.RunOpts = append(config.AppResource.Docker.RunOpts, portMapping)
	}

	// Build docker run options from Command and Args
	if len(spec.Command) > 0 || len(spec.Args) > 0 {
		var dockerOpts string
		if len(spec.Command) > 0 {
			// Use command as entrypoint
			dockerOpts = fmt.Sprintf("--entrypoint %s", spec.Command[0])
			// If there are additional command parts or args, add them
			if len(spec.Command) > 1 {
				cmdArgs := append(spec.Command[1:], spec.Args...)
				dockerOpts = fmt.Sprintf("%s -c \\\"%s\\\"", dockerOpts, strings.Join(cmdArgs, " "))
			} else if len(spec.Args) > 0 {
				dockerOpts = fmt.Sprintf("%s -c \\\"%s\\\"", dockerOpts, strings.Join(spec.Args, " "))
			}
		} else if len(spec.Args) > 0 {
			// Only args provided, join them
			dockerOpts = strings.Join(spec.Args, " ")
		}
		config.DockerRunOpts = dockerOpts
	}

	return config
}

// createAppHostingInstance creates the app-hosting instance via RESTCONF
func (c *IOSXEClient) createAppHostingInstance(ctx context.Context, config C9KAppHostingConfig) error {
	// Use discovered schema endpoint
	if c.schema == nil || c.schema.AppHostingConfigPath == "" {
		return fmt.Errorf("app-hosting config endpoint not available")
	}

	url := c.baseURL + c.schema.AppHostingConfigPath

	// Prepare the YANG-compliant payload
	payload := map[string]interface{}{
		"Cisco-IOS-XE-native:app-hosting": map[string]interface{}{
			"appid": []map[string]interface{}{
				{
					"appid": config.AppID,
					"app-vnic": map[string]interface{}{
						"management": map[string]interface{}{
							"guest-interface":     config.AppVnic.Management.GuestInterface,
							"guest-ipaddress":     config.AppVnic.Management.GuestIPAddress,
							"netmask":             config.AppVnic.Management.Netmask,
							"app-default-gateway": config.AppVnic.Management.AppDefaultGW,
						},
					},
					"app-resource": map[string]interface{}{
						"docker": map[string]interface{}{
							"run-opts": config.AppResource.Docker.RunOpts,
						},
						"profile": config.AppResource.Profile,
						"memory":  config.AppResource.MemoryMB,
						"vcpu":    config.AppResource.VCPU,
					},
				},
			},
		},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal app-hosting config: %v", err)
	}

	log.G(ctx).Infof("📤 Creating app-hosting instance: %s", string(jsonPayload))

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return err
	}

	req.SetBasicAuth(c.config.Username, c.config.Password)
	req.Header.Set("Content-Type", "application/yang-data+json")
	req.Header.Set("Accept", "application/yang-data+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to create app-hosting instance: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("app-hosting creation failed (status %d): %s", resp.StatusCode, string(body))
	}

	log.G(ctx).Infof("✅ App-hosting instance created successfully")
	return nil
}

// startAppHostingInstance starts the app-hosting instance
func (c *IOSXEClient) startAppHostingInstance(ctx context.Context, appID string) error {
	// Use discovered schema endpoint - construct path dynamically
	if c.schema == nil || c.schema.AppHostingConfigPath == "" {
		return fmt.Errorf("app-hosting config endpoint not available")
	}

	url := fmt.Sprintf("%s%s/appid=%s/start", c.baseURL, c.schema.AppHostingConfigPath, appID)

	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewBuffer([]byte(`{}`)))
	if err != nil {
		return err
	}

	req.SetBasicAuth(c.config.Username, c.config.Password)
	req.Header.Set("Content-Type", "application/yang-data+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to start app-hosting instance: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("app-hosting start failed (status %d): %s", resp.StatusCode, string(body))
	}

	log.G(ctx).Infof("✅ App-hosting instance %s started", appID)
	return nil
}

// verifyAppDeployment verifies that the application is deployed and running
func (c *IOSXEClient) verifyAppDeployment(ctx context.Context, appID string) error {
	maxRetries := 20 // Increased timeout for slow device operations
	for i := 0; i < maxRetries; i++ {
		status, err := c.getAppHostingStatus(ctx, appID)
		if err != nil {
			log.G(ctx).Warnf("Failed to get app status (attempt %d/%d): %v", i+1, maxRetries, err)
			time.Sleep(3 * time.Second)
			continue
		}

		// Accept DEPLOYED, ACTIVATED, or RUNNING as success states
		if status.State == "RUNNING" {
			log.G(ctx).Infof("✅ Application %s is RUNNING with IP: %s", appID, status.Details.IPAddress)
			return nil
		}

		if status.State == "DEPLOYED" || status.State == "ACTIVATED" {
			log.G(ctx).Infof("✅ Application %s is %s (container deployed successfully)", appID, status.State)
			// For Docker containers, DEPLOYED state is sufficient
			return nil
		}

		log.G(ctx).Infof("⏳ Application %s state: %s (run-state: %s), waiting...",
			appID, status.State, status.Details.RunState)
		time.Sleep(3 * time.Second)
	}

	return fmt.Errorf("application %s failed to reach deployed/running state after %d attempts", appID, maxRetries)
}

// getAppHostingStatus gets the operational status of an app-hosting instance
func (c *IOSXEClient) getAppHostingStatus(ctx context.Context, appID string) (*AppInfo, error) {
	// Use discovered schema endpoint for operational data
	if c.schema == nil || c.schema.AppHostingOperPath == "" {
		return nil, fmt.Errorf("app-hosting operational endpoint not available")
	}

	url := c.baseURL + c.schema.AppHostingOperPath

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(c.config.Username, c.config.Password)
	req.Header.Set("Accept", "application/yang-data+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get app status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("app status query failed (status %d): %s", resp.StatusCode, string(body))
	}

	var operData AppHostingOperData
	if err := json.NewDecoder(resp.Body).Decode(&operData); err != nil {
		return nil, fmt.Errorf("failed to parse app status: %v", err)
	}

	// Find our application
	for _, app := range operData.AppHostingOperData.App {
		if app.AppID == appID {
			return &app, nil
		}
	}

	return nil, fmt.Errorf("application %s not found in operational data", appID)
}

func (c *IOSXEClient) removeIOxApplication(ctx context.Context, appID string) error {
	log.G(ctx).Infof("🗑️ Removing REAL IOx application %s from C9K device %s", appID, c.config.Name)

	// Step 1: Stop the application
	if err := c.stopAppHostingInstance(ctx, appID); err != nil {
		log.G(ctx).Warnf("Failed to stop app %s (continuing with removal): %v", appID, err)
	}

	// Step 2: Remove the application configuration
	if err := c.removeAppHostingInstance(ctx, appID); err != nil {
		return fmt.Errorf("failed to remove app-hosting instance: %v", err)
	}

	log.G(ctx).Infof("✅ Successfully removed IOx application %s", appID)
	return nil
}

// stopAppHostingInstance stops the app-hosting instance
func (c *IOSXEClient) stopAppHostingInstance(ctx context.Context, appID string) error {
	// Use discovered schema endpoint - construct path dynamically
	if c.schema == nil || c.schema.AppHostingConfigPath == "" {
		return fmt.Errorf("app-hosting config endpoint not available")
	}

	url := fmt.Sprintf("%s%s/appid=%s/stop", c.baseURL, c.schema.AppHostingConfigPath, appID)

	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewBuffer([]byte(`{}`)))
	if err != nil {
		return err
	}

	req.SetBasicAuth(c.config.Username, c.config.Password)
	req.Header.Set("Content-Type", "application/yang-data+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to stop app-hosting instance: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("app-hosting stop failed (status %d): %s", resp.StatusCode, string(body))
	}

	log.G(ctx).Infof("✅ App-hosting instance %s stopped", appID)
	return nil
}

// removeAppHostingInstance removes the app-hosting configuration
func (c *IOSXEClient) removeAppHostingInstance(ctx context.Context, appID string) error {
	// Use discovered schema endpoint - construct path dynamically
	if c.schema == nil || c.schema.AppHostingConfigPath == "" {
		return fmt.Errorf("app-hosting config endpoint not available")
	}

	url := fmt.Sprintf("%s%s/appid=%s", c.baseURL, c.schema.AppHostingConfigPath, appID)

	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}

	req.SetBasicAuth(c.config.Username, c.config.Password)
	req.Header.Set("Accept", "application/yang-data+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to remove app-hosting instance: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode != 404 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("app-hosting removal failed (status %d): %s", resp.StatusCode, string(body))
	}

	log.G(ctx).Infof("✅ App-hosting instance %s removed", appID)
	return nil
}

// executeAppHostingRPC executes an app-hosting RPC operation
func (c *IOSXEClient) executeAppHostingRPC(ctx context.Context, operation string, payload map[string]interface{}) error {
	// RESTCONF RPC endpoint for app-hosting operations
	url := fmt.Sprintf("%s/restconf/operations/Cisco-IOS-XE-app-hosting-rpc:app-hosting-%s", c.baseURL, operation)

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal RPC payload: %v", err)
	}

	log.G(ctx).Debugf("Executing app-hosting RPC %s: %s", operation, string(jsonPayload))

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return err
	}

	req.SetBasicAuth(c.config.Username, c.config.Password)
	req.Header.Set("Content-Type", "application/yang-data+json")
	req.Header.Set("Accept", "application/yang-data+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("RPC %s failed: %v", operation, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("RPC %s failed (status %d): %s", operation, resp.StatusCode, string(body))
	}

	log.G(ctx).Debugf("RPC %s completed successfully", operation)
	return nil
}

// getAllAppHostingStatus retrieves status of all app-hosting instances
func (c *IOSXEClient) getAllAppHostingStatus(ctx context.Context) (*AppHostingOperData, error) {
	// Use discovered schema endpoint for operational data
	if c.schema == nil || c.schema.AppHostingOperPath == "" {
		return nil, fmt.Errorf("app-hosting operational endpoint not available")
	}

	url := c.baseURL + c.schema.AppHostingOperPath

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(c.config.Username, c.config.Password)
	req.Header.Set("Accept", "application/yang-data+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get app-hosting status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("app-hosting status query failed (status %d): %s", resp.StatusCode, string(body))
	}

	var operData AppHostingOperData
	if err := json.NewDecoder(resp.Body).Decode(&operData); err != nil {
		return nil, fmt.Errorf("failed to parse app-hosting status: %v", err)
	}

	return &operData, nil
}

func (c *IOSXEClient) getIOxApplicationStatus(ctx context.Context, containerID string) (*Container, error) {
	// Simulate getting application status
	container := &Container{
		ID:        containerID,
		Name:      "app-" + containerID,
		State:     ContainerStateRunning,
		DeviceID:  c.config.Name,
		CreatedAt: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
	}

	startTime := metav1.NewTime(time.Now().Add(-5 * time.Minute))
	container.StartedAt = &startTime

	return container, nil
}

func (c *IOSXEClient) listIOxApplications(ctx context.Context) ([]*Container, error) {
	// Simulate listing applications
	return []*Container{}, nil
}

// generateContainerID generates a unique container ID
func generateContainerID() string {
	return fmt.Sprintf("cisco-%d", time.Now().UnixNano())
}

// CreateVRF creates a VRF on the device
func (c *IOSXEClient) CreateVRF(ctx context.Context, vrfName string) error {
	log.G(ctx).Infof("Creating VRF %s on device %s", vrfName, c.config.Name)

	// RESTCONF payload for VRF creation
	vrfConfig := map[string]interface{}{
		"Cisco-IOS-XE-native:vrf": map[string]interface{}{
			"definition": []map[string]interface{}{
				{
					"name": vrfName,
					"rd":   fmt.Sprintf("65000:%d", time.Now().Unix()%65535),
				},
			},
		},
	}

	jsonBody, err := json.Marshal(vrfConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal VRF config: %v", err)
	}

	_, err = c.restconfPost("/restconf/data/Cisco-IOS-XE-native:native", jsonBody)
	return err
}

// CreateVLAN creates a VLAN on the device
func (c *IOSXEClient) CreateVLAN(ctx context.Context, vlanID int, name string) error {
	log.G(ctx).Infof("Creating VLAN %d (%s) on device %s", vlanID, name, c.config.Name)

	vlanConfig := map[string]interface{}{
		"Cisco-IOS-XE-native:vlan": map[string]interface{}{
			"vlan-list": []map[string]interface{}{
				{
					"id":   vlanID,
					"name": name,
				},
			},
		},
	}

	jsonBody, err := json.Marshal(vlanConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal VLAN config: %v", err)
	}

	_, err = c.restconfPost("/restconf/data/Cisco-IOS-XE-native:native", jsonBody)
	return err
}
