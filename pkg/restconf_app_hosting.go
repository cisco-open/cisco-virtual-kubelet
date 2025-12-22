package cisco

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
)

// RESTCONFAppHostingClient handles app-hosting lifecycle via RESTCONF API
type RESTCONFAppHostingClient struct {
	client *IOSXEClient
}

// NewRESTCONFAppHostingClient creates a new RESTCONF-based app-hosting client
func NewRESTCONFAppHostingClient(client *IOSXEClient) *RESTCONFAppHostingClient {
	return &RESTCONFAppHostingClient{
		client: client,
	}
}

// AppStatus represents the operational status of an app
type AppStatus struct {
	Name    string     `json:"name"`
	Details AppDetails `json:"details"`
}

type PackageInformation struct {
	Path        string `json:"path,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

type ResourceAllocation struct {
	CPUUnits int `json:"cpu-units,omitempty"`
	MemoryMB int `json:"memory-mb,omitempty"`
	DiskMB   int `json:"disk-mb,omitempty"`
}

// Install installs an application package via RESTCONF RPC
func (r *RESTCONFAppHostingClient) Install(ctx context.Context, appID, packagePath string) error {
	fmt.Printf("[CISCO-VK] 📦 Installing application via RESTCONF RPC: %s from %s\n", appID, packagePath)
	log.G(ctx).Infof("📦 Installing application via RESTCONF: %s from %s", appID, packagePath)

	// Build RPC request payload per Cisco-IOS-XE-rpc.yang schema
	payload := map[string]interface{}{
		"Cisco-IOS-XE-rpc:input": map[string]interface{}{
			"install": map[string]string{
				"appid":   appID,
				"package": packagePath,
			},
		},
	}

	// Execute RPC - use /restconf/operations/ for RPCs (not /restconf/data/)
	endpoint := "/restconf/operations/Cisco-IOS-XE-rpc:app-hosting"
	fmt.Printf("[CISCO-VK] 📤 Sending Install RPC to: %s\n", endpoint)
	if err := r.executeRPC(ctx, endpoint, payload); err != nil {
		fmt.Printf("[CISCO-VK] ❌ Install RPC failed: %v\n", err)
		return fmt.Errorf("install RPC failed: %v", err)
	}

	fmt.Printf("[CISCO-VK] ✅ Install RPC completed for %s\n", appID)
	log.G(ctx).Infof("✅ Install RPC completed for %s", appID)
	return nil
}

// Activate activates an installed application via RESTCONF RPC
func (r *RESTCONFAppHostingClient) Activate(ctx context.Context, appID string) error {
	fmt.Printf("[CISCO-VK] ⚡ Activating application via RESTCONF RPC: %s\n", appID)
	log.G(ctx).Infof("⚡ Activating application via RESTCONF: %s", appID)

	// Build RPC request payload per Cisco-IOS-XE-rpc.yang schema
	payload := map[string]interface{}{
		"Cisco-IOS-XE-rpc:input": map[string]interface{}{
			"activate": map[string]string{
				"appid": appID,
			},
		},
	}

	// Execute RPC - use /restconf/operations/ for RPCs
	endpoint := "/restconf/operations/Cisco-IOS-XE-rpc:app-hosting"
	fmt.Printf("[CISCO-VK] 📤 Sending Activate RPC to: %s\n", endpoint)
	if err := r.executeRPC(ctx, endpoint, payload); err != nil {
		fmt.Printf("[CISCO-VK] ❌ Activate RPC failed: %v\n", err)
		return fmt.Errorf("activate RPC failed: %v", err)
	}

	fmt.Printf("[CISCO-VK] ✅ Activate RPC completed for %s\n", appID)
	log.G(ctx).Infof("✅ Activate RPC completed for %s", appID)
	return nil
}

// Start starts an activated application via RESTCONF RPC
func (r *RESTCONFAppHostingClient) Start(ctx context.Context, appID string) error {
	fmt.Printf("[CISCO-VK] ▶️ Starting application via RESTCONF RPC: %s\n", appID)
	log.G(ctx).Infof("▶️ Starting application via RESTCONF: %s", appID)

	// Build RPC request payload per Cisco-IOS-XE-rpc.yang schema
	payload := map[string]interface{}{
		"Cisco-IOS-XE-rpc:input": map[string]interface{}{
			"start": map[string]string{
				"appid": appID,
			},
		},
	}

	// Execute RPC - use /restconf/operations/ for RPCs
	endpoint := "/restconf/operations/Cisco-IOS-XE-rpc:app-hosting"
	fmt.Printf("[CISCO-VK] 📤 Sending Start RPC to: %s\n", endpoint)
	if err := r.executeRPC(ctx, endpoint, payload); err != nil {
		fmt.Printf("[CISCO-VK] ❌ Start RPC failed: %v\n", err)
		return fmt.Errorf("start RPC failed: %v", err)
	}

	fmt.Printf("[CISCO-VK] ✅ Start RPC completed for %s\n", appID)
	log.G(ctx).Infof("✅ Start RPC completed for %s", appID)
	return nil
}

// Stop stops a running application via RESTCONF RPC
func (r *RESTCONFAppHostingClient) Stop(ctx context.Context, appID string) error {
	fmt.Printf("[CISCO-VK] ⏸️ Stopping application via RESTCONF RPC: %s\n", appID)
	log.G(ctx).Infof("⏸️ Stopping application via RESTCONF: %s", appID)

	// Build RPC request payload per Cisco-IOS-XE-rpc.yang schema
	payload := map[string]interface{}{
		"Cisco-IOS-XE-rpc:input": map[string]interface{}{
			"stop": map[string]string{
				"appid": appID,
			},
		},
	}

	// Execute RPC - use /restconf/operations/ for RPCs
	endpoint := "/restconf/operations/Cisco-IOS-XE-rpc:app-hosting"
	fmt.Printf("[CISCO-VK] 📤 Sending Stop RPC to: %s\n", endpoint)
	if err := r.executeRPC(ctx, endpoint, payload); err != nil {
		fmt.Printf("[CISCO-VK] ❌ Stop RPC failed: %v\n", err)
		return fmt.Errorf("stop RPC failed: %v", err)
	}

	fmt.Printf("[CISCO-VK] ✅ Stop RPC completed for %s\n", appID)
	log.G(ctx).Infof("✅ Stop RPC completed for %s", appID)
	return nil
}

// Deactivate deactivates an application via RESTCONF RPC
func (r *RESTCONFAppHostingClient) Deactivate(ctx context.Context, appID string) error {
	fmt.Printf("[CISCO-VK] ⏬ Deactivating application via RESTCONF RPC: %s\n", appID)
	log.G(ctx).Infof("⏬ Deactivating application via RESTCONF: %s", appID)

	// Build RPC request payload per Cisco-IOS-XE-rpc.yang schema
	payload := map[string]interface{}{
		"Cisco-IOS-XE-rpc:input": map[string]interface{}{
			"deactivate": map[string]string{
				"appid": appID,
			},
		},
	}

	// Execute RPC - use /restconf/operations/ for RPCs
	endpoint := "/restconf/operations/Cisco-IOS-XE-rpc:app-hosting"
	fmt.Printf("[CISCO-VK] 📤 Sending Deactivate RPC to: %s\n", endpoint)
	if err := r.executeRPC(ctx, endpoint, payload); err != nil {
		fmt.Printf("[CISCO-VK] ❌ Deactivate RPC failed: %v\n", err)
		return fmt.Errorf("deactivate RPC failed: %v", err)
	}

	fmt.Printf("[CISCO-VK] ✅ Deactivate RPC completed for %s\n", appID)
	log.G(ctx).Infof("✅ Deactivate RPC completed for %s", appID)
	return nil
}

// Uninstall uninstalls an application via RESTCONF RPC
func (r *RESTCONFAppHostingClient) Uninstall(ctx context.Context, appID string) error {
	fmt.Printf("[CISCO-VK] 🗑️ Uninstalling application via RESTCONF RPC: %s\n", appID)
	log.G(ctx).Infof("🗑️ Uninstalling application via RESTCONF: %s", appID)

	// Build RPC request payload per Cisco-IOS-XE-rpc.yang schema
	payload := map[string]interface{}{
		"Cisco-IOS-XE-rpc:input": map[string]interface{}{
			"uninstall": map[string]string{
				"appid": appID,
			},
		},
	}

	// Execute RPC - use /restconf/operations/ for RPCs
	endpoint := "/restconf/operations/Cisco-IOS-XE-rpc:app-hosting"
	fmt.Printf("[CISCO-VK] 📤 Sending Uninstall RPC to: %s\n", endpoint)
	if err := r.executeRPC(ctx, endpoint, payload); err != nil {
		fmt.Printf("[CISCO-VK] ❌ Uninstall RPC failed: %v\n", err)
		return fmt.Errorf("uninstall RPC failed: %v", err)
	}

	fmt.Printf("[CISCO-VK] ✅ Uninstall RPC completed for %s\n", appID)
	log.G(ctx).Infof("✅ Uninstall RPC completed for %s", appID)
	return nil
}

// WaitForState polls for a specific app state with timeout (CRITICAL for reliable deployment)
func (r *RESTCONFAppHostingClient) WaitForState(ctx context.Context, appID, expectedState string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	pollInterval := 5 * time.Second // Increased poll interval

	fmt.Printf("[CISCO-VK] ⏳ WaitForState: waiting for app %s to reach state: %s (timeout: %v)\n", appID, expectedState, timeout)
	log.G(ctx).Infof("⏳ Waiting for app %s to reach state: %s (timeout: %v)", appID, expectedState, timeout)

	// Initial delay to allow device to register the app after Install RPC
	fmt.Printf("[CISCO-VK] ⏳ Initial 10s delay to allow device to register app...\n")
	time.Sleep(10 * time.Second)

	attempts := 0
	for time.Now().Before(deadline) {
		attempts++
		// Query current state via RESTCONF
		state, err := r.GetApplicationState(ctx, appID)
		if err != nil {
			fmt.Printf("[CISCO-VK] ⚠️ Attempt %d: Failed to get app state: %v, retrying...\n", attempts, err)
			log.G(ctx).Warnf("Failed to get app state: %v, retrying...", err)
			time.Sleep(pollInterval)
			continue
		}

		fmt.Printf("[CISCO-VK] 🔍 Attempt %d: App %s current state: %s, waiting for: %s\n", attempts, appID, state, expectedState)

		if state == expectedState {
			fmt.Printf("[CISCO-VK] ✅ App %s reached state: %s\n", appID, expectedState)
			log.G(ctx).Infof("✅ App %s reached state: %s", appID, expectedState)
			return nil
		}

		log.G(ctx).Infof("⏳ App %s current state: %s, waiting for: %s", appID, state, expectedState)
		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timeout waiting for state %s after %v (%d attempts)", expectedState, timeout, attempts)
}

// GetApplicationState queries the current app state via RESTCONF operational data
func (r *RESTCONFAppHostingClient) GetApplicationState(ctx context.Context, appID string) (string, error) {
	url := fmt.Sprintf("%s/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data",
		r.client.baseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	req.SetBasicAuth(r.client.config.Username, r.client.config.Password)
	req.Header.Set("Accept", "application/yang-data+json")

	resp, err := r.client.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		fmt.Printf("[CISCO-VK] ⚠️ GetApplicationState failed HTTP %d: %s\n", resp.StatusCode, string(body))
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// fmt.Printf("[CISCO-VK] 🔍 GetApplicationState response: %s\n", string(body)[:min(len(body), 500)])
	fmt.Printf("[CISCO-VK] 🔍 GetApplicationState response: %s\n", string(body))
	// Parse RESTCONF response - structure matches Cisco-IOS-XE-app-hosting-oper.yang
	var result struct {
		AppHostingOperData struct {
			App []struct {
				Name    string `json:"name"` // App name/ID
				Details struct {
					State string `json:"state"` // DEPLOYED, ACTIVATED, RUNNING, etc.
				} `json:"details"`
			} `json:"app"`
		} `json:"Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("[CISCO-VK] ⚠️ Failed to decode response: %v\n", err)
		return "", fmt.Errorf("failed to decode response: %v", err)
	}

	fmt.Printf("[CISCO-VK] 🔍 Found %d apps in response\n", len(result.AppHostingOperData.App))
	for _, app := range result.AppHostingOperData.App {
		fmt.Printf("[CISCO-VK] 🔍 App: %s, State: %s\n", app.Name, app.Details.State)
		if app.Name == appID {
			return app.Details.State, nil
		}
	}

	return "", fmt.Errorf("app %s not found in response", appID)
}

// Configure applies configuration to create app-hosting appid (MUST be called BEFORE install)
func (r *RESTCONFAppHostingClient) Configure(ctx context.Context, appID string, config C9KAppHostingConfig) error {
	fmt.Printf("[CISCO-VK] ⚙️ Configuring application via RESTCONF: %s\n", appID)
	log.G(ctx).Infof("⚙️ Configuring application via RESTCONF: %s", appID)

	// POST to app-hosting-cfg-data using correct YANG schema from Cisco-IOS-XE-app-hosting-cfg.yang
	url := fmt.Sprintf("%s/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps", r.client.baseURL)

	// Use the correct YANG schema structure based on Cisco-IOS-XE-app-hosting-cfg.yang
	// Management interface fields discovered from working device config query
	payload := map[string]interface{}{
		"Cisco-IOS-XE-app-hosting-cfg:app": []map[string]interface{}{
			{
				"application-name": appID,
				// Network resource configuration - MUST include management interface for activation
				"application-network-resource": map[string]interface{}{
					// Management interface configuration (app-vnic management guest-interface)
					"management-interface-name":   fmt.Sprintf("%d", config.AppVnic.Management.GuestInterface),
					"management-guest-ip-address": config.AppVnic.Management.GuestIPAddress,
					"management-guest-ip-netmask": config.AppVnic.Management.Netmask,
					// Default gateway configuration
					"virtualportgroup-application-default-gateway-1":     config.AppVnic.Management.AppDefaultGW,
					"virtualportgroup-guest-interface-default-gateway-1": config.AppVnic.Management.GuestInterface,
				},
				// Resource profile configuration
				"application-resource-profile": map[string]interface{}{
					"cpu-units":          config.AppResource.CPU,
					"memory-capacity-mb": config.AppResource.MemoryMB,
					"disk-size-mb":       config.AppResource.PersistDisk,
					"vcpu":               config.AppResource.VCPU,
					"profile-name":       config.AppResource.Profile,
				},
			},
		},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %v", err)
	}

	fmt.Printf("[CISCO-VK] 📤 POST configuration to: %s\n", url)
	fmt.Printf("[CISCO-VK] 📋 Payload: %s\n", string(jsonPayload))
	log.G(ctx).Infof("📤 Applying configuration: %s", string(jsonPayload))

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return err
	}

	req.SetBasicAuth(r.client.config.Username, r.client.config.Password)
	req.Header.Set("Content-Type", "application/yang-data+json")
	req.Header.Set("Accept", "application/yang-data+json")

	resp, err := r.client.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("[CISCO-VK] ❌ Configure failed (HTTP %d): %s\n", resp.StatusCode, string(body))
		return fmt.Errorf("configure failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	fmt.Printf("[CISCO-VK] ✅ Configuration applied for %s (HTTP %d)\n", appID, resp.StatusCode)
	log.G(ctx).Infof("✅ Configuration applied for %s", appID)
	return nil
}

// GetStatus retrieves the operational status of an application
func (r *RESTCONFAppHostingClient) GetStatus(ctx context.Context, appID string) (*AppStatus, error) {
	// Use discovered operational endpoint
	if r.client.schema == nil || r.client.schema.AppHostingOperPath == "" {
		return nil, fmt.Errorf("no operational endpoint available")
	}

	// Query all apps (device doesn't support /app=<id> endpoint)
	// We'll filter by appID in the response
	url := r.client.baseURL + r.client.schema.AppHostingOperPath

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(r.client.config.Username, r.client.config.Password)
	req.Header.Set("Accept", "application/yang-data+json")

	resp, err := r.client.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("status query failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status query failed (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response structure
	var result struct {
		AppHostingOperData struct {
			App []AppStatus `json:"app"`
		} `json:"Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse status: %v", err)
	}

	// Find the app by name (appID)
	for _, app := range result.AppHostingOperData.App {
		if app.Name == appID {
			return &app, nil
		}
	}

	return nil, fmt.Errorf("application not found: %s", appID)
}

// executeRPC executes a RESTCONF RPC call
func (r *RESTCONFAppHostingClient) executeRPC(ctx context.Context, endpoint string, payload interface{}) error {
	url := r.client.baseURL + endpoint

	// Marshal payload
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %v", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.SetBasicAuth(r.client.config.Username, r.client.config.Password)
	req.Header.Set("Content-Type", "application/yang-data+json")
	req.Header.Set("Accept", "application/yang-data+json")

	// Execute request
	resp, err := r.client.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("RPC request failed: %v", err)
	}
	defer resp.Body.Close()

	// Check response
	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("RPC failed (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	log.G(ctx).Debugf("RPC success: %s (status %d)", endpoint, resp.StatusCode)
	return nil
}

// patchConfiguration executes a RESTCONF PATCH for configuration
func (r *RESTCONFAppHostingClient) patchConfiguration(ctx context.Context, endpoint string, payload interface{}) error {
	url := r.client.baseURL + endpoint

	// Marshal payload
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %v", err)
	}

	// Create request (use PATCH for partial config updates)
	req, err := http.NewRequestWithContext(ctx, "PATCH", url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.SetBasicAuth(r.client.config.Username, r.client.config.Password)
	req.Header.Set("Content-Type", "application/yang-data+json")
	req.Header.Set("Accept", "application/yang-data+json")

	// Execute request
	resp, err := r.client.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("configuration request failed: %v", err)
	}
	defer resp.Body.Close()

	// Check response (200 OK, 201 Created, or 204 No Content are success)
	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("configuration failed (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	log.G(ctx).Debugf("Configuration success: %s (status %d)", endpoint, resp.StatusCode)
	return nil
}

// GetAllApps retrieves all applications from the device
func (r *RESTCONFAppHostingClient) GetAllApps(ctx context.Context) ([]AppStatus, error) {
	// Use discovered operational endpoint
	if r.client.schema == nil || r.client.schema.AppHostingOperPath == "" {
		return nil, fmt.Errorf("no operational endpoint available")
	}

	url := r.client.baseURL + r.client.schema.AppHostingOperPath

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(r.client.config.Username, r.client.config.Password)
	req.Header.Set("Accept", "application/yang-data+json")

	resp, err := r.client.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	fmt.Printf("AppHostingOperPath: %s", r.client.schema.AppHostingOperPath)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result struct {
		AppHostingOperData struct {
			App []AppStatus `json:"app"`
		} `json:"Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %v", err)
	}

	return result.AppHostingOperData.App, nil
}

// ListApplications is an alias for GetAllApps for consistency
func (r *RESTCONFAppHostingClient) ListApplications(ctx context.Context) ([]AppStatus, error) {
	return r.GetAllApps(ctx)
}
