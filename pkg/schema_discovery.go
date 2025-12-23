package cisco

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/virtual-kubelet/virtual-kubelet/log"
)

// discoverSchema performs automatic schema discovery on connect
func (c *IOSXEClient) discoverSchema(ctx context.Context) error {
	log.G(ctx).Info("🔍 Starting dynamic schema discovery for IOS XE device")

	endpoints := &DiscoveredEndpoints{}

	// Test various known endpoint patterns for different IOS XE versions
	candidates := []struct {
		name  string
		path  string
		field *string
	}{
		// IOx configuration endpoints
		{"IOx Config (native)", "/restconf/data/Cisco-IOS-XE-native:native/iox", &endpoints.IOxConfigPath},
		{"IOx Config (process)", "/restconf/data/Cisco-IOS-XE-process:processes", &endpoints.IOxConfigPath},

		// App-hosting configuration endpoints
		{"App-hosting (native)", "/restconf/data/Cisco-IOS-XE-native:native/app-hosting", &endpoints.AppHostingConfigPath},
		{"App-hosting (cfg)", "/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data", &endpoints.AppHostingConfigPath},
		{"Virtual Service", "/restconf/data/Cisco-IOS-XE-virtual-service-cfg:virtual-service-cfg-data", &endpoints.VirtualServicePath},

		// Container/application endpoints
		{"Container Config", "/restconf/data/Cisco-IOS-XE-container:containers", &endpoints.ContainerConfigPath},
		{"Application Config", "/restconf/data/Cisco-IOS-XE-application:applications", &endpoints.ContainerConfigPath},

		// Operational data endpoints
		{"App-hosting Oper", "/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data", &endpoints.AppHostingOperPath},
		{"IOx Oper", "/restconf/data/Cisco-IOS-XE-iox-oper:iox-oper", &endpoints.IOxOperPath},
		// {"Virtual Service Oper", "/restconf/data/Cisco-IOS-XE-virtual-service-oper:virtual-service-oper", &endpoints.AppHostingOperPath},
	}

	// Test each candidate endpoint
	foundCount := 0
	for _, candidate := range candidates {
		if c.testEndpoint(ctx, candidate.path) {
			*candidate.field = candidate.path
			log.G(ctx).Infof("✅ Found working endpoint: %s -> %s", candidate.name, candidate.path)
			foundCount++
		}
	}

	// Discover supported operations if we found an app-hosting config path
	if endpoints.AppHostingConfigPath != "" {
		endpoints.SupportedOperations = c.discoverSupportedOperations(ctx, endpoints.AppHostingConfigPath)
	}

	// Log discovered schema
	c.logDiscoveredSchema(ctx, endpoints)

	// Validate that we found at least basic functionality
	if endpoints.IOxConfigPath == "" && endpoints.AppHostingConfigPath == "" && endpoints.VirtualServicePath == "" {
		log.G(ctx).Warn("⚠️  No app-hosting endpoints discovered - app-hosting may not be supported on this IOS XE version")
		log.G(ctx).Info("💡 Hint: Enable IOx on device with 'configure terminal' -> 'iox' -> 'commit'")
		// Don't fail - just log warning and continue with limited functionality
	} else {
		log.G(ctx).Infof("🎯 Schema discovery completed: found %d working endpoints", foundCount)
	}

	c.schema = endpoints
	return nil
}

// testEndpoint tests if a specific RESTCONF endpoint exists
func (c *IOSXEClient) testEndpoint(ctx context.Context, path string) bool {
	url := c.baseURL + path
	fmt.Printf("TEST ENDPOINT: %s\n", url)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false
	}

	req.SetBasicAuth(c.config.Username, c.config.Password)
	req.Header.Set("Accept", "application/yang-data+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	fmt.Printf("STATUS: %d\n", resp.StatusCode)
	// 200 = endpoint exists with data
	// 204 = endpoint exists but no data
	// 404 = endpoint doesn't exist
	// 400 = endpoint exists but request malformed (still means endpoint is there)
	return resp.StatusCode == 200 || resp.StatusCode == 204 || resp.StatusCode == 400
}

// discoverSupportedOperations determines what HTTP methods are supported
func (c *IOSXEClient) discoverSupportedOperations(ctx context.Context, basePath string) []string {
	operations := []string{}
	testMethods := []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

	for _, method := range testMethods {
		if c.testHTTPMethod(ctx, basePath, method) {
			operations = append(operations, method)
		}
	}

	return operations
}

// testHTTPMethod tests if a specific HTTP method is supported on an endpoint
func (c *IOSXEClient) testHTTPMethod(ctx context.Context, path, method string) bool {
	url := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader("{}"))
	if err != nil {
		return false
	}

	req.SetBasicAuth(c.config.Username, c.config.Password)
	req.Header.Set("Accept", "application/yang-data+json")
	req.Header.Set("Content-Type", "application/yang-data+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	// Method supported if we don't get 405 (Method Not Allowed)
	return resp.StatusCode != 405
}

// logDiscoveredSchema logs the discovered schema for debugging
func (c *IOSXEClient) logDiscoveredSchema(ctx context.Context, endpoints *DiscoveredEndpoints) {
	log.G(ctx).Info("📋 Discovered IOS XE Schema:")

	if endpoints.IOxConfigPath != "" {
		log.G(ctx).Infof("  ✅ IOx Config: %s", endpoints.IOxConfigPath)
	}
	if endpoints.AppHostingConfigPath != "" {
		log.G(ctx).Infof("  ✅ App-hosting Config: %s", endpoints.AppHostingConfigPath)
	}
	if endpoints.VirtualServicePath != "" {
		log.G(ctx).Infof("  ✅ Virtual Service: %s", endpoints.VirtualServicePath)
	}
	if endpoints.ContainerConfigPath != "" {
		log.G(ctx).Infof("  ✅ Container Config: %s", endpoints.ContainerConfigPath)
	}
	if endpoints.AppHostingOperPath != "" {
		log.G(ctx).Infof("  ✅ App-hosting Oper: %s", endpoints.AppHostingOperPath)
	}
	if endpoints.IOxOperPath != "" {
		log.G(ctx).Infof("  ✅ IOx Oper: %s", endpoints.IOxOperPath)
	}

	if len(endpoints.SupportedOperations) > 0 {
		log.G(ctx).Infof("  🔧 Supported Operations: %v", endpoints.SupportedOperations)
	}
}

// GetAppHostingOperationalData retrieves current app-hosting status using discovered schema
func (c *IOSXEClient) GetAppHostingOperationalData(ctx context.Context) (map[string]interface{}, error) {
	if c.schema == nil {
		return nil, fmt.Errorf("schema not discovered yet")
	}

	var operPath string

	// Use the first available operational endpoint
	if c.schema.AppHostingOperPath != "" {
		operPath = c.schema.AppHostingOperPath
	} else if c.schema.IOxOperPath != "" {
		operPath = c.schema.IOxOperPath
	} else {
		return nil, fmt.Errorf("no operational data endpoints available")
	}

	url := c.baseURL + operPath

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(c.config.Username, c.config.Password)
	req.Header.Set("Accept", "application/yang-data+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get operational data: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("operational data query failed (status %d): %s", resp.StatusCode, string(body))
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to parse operational data: %v", err)
	}

	log.G(ctx).Infof("📊 Retrieved operational data from %s", operPath)
	return data, nil
}

// GetDiscoveredSchema returns the discovered schema endpoints
func (c *IOSXEClient) GetDiscoveredSchema() *DiscoveredEndpoints {
	return c.schema
}

// HasAppHostingSupport checks if the device supports app-hosting
func (c *IOSXEClient) HasAppHostingSupport() bool {
	if c.schema == nil {
		return false
	}
	return c.schema.IOxConfigPath != "" ||
		c.schema.AppHostingConfigPath != "" ||
		c.schema.VirtualServicePath != ""
}
