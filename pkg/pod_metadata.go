package cisco

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
)

// addDeploymentMetadata adds deployment metadata to pod annotations for kubectl describe
func (p *CiscoProvider) addDeploymentMetadata(ctx context.Context, pod *v1.Pod, containers []*Container) error {
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}

	log.G(ctx).Infof("📝 Adding deployment metadata annotations for pod %s/%s", pod.Namespace, pod.Name)

	// For each container, add metadata
	for i, container := range containers {
		prefix := fmt.Sprintf("cisco.com/container-%d", i)

		// Get device to query app status
		device, err := p.deviceManager.GetDevice(container.DeviceID)
		if err != nil {
			log.G(ctx).Warnf("Failed to get device for metadata: %v", err)
			continue
		}

		// Basic container information
		pod.Annotations[prefix+".app-id"] = container.ID
		pod.Annotations[prefix+".device-id"] = container.DeviceID
		pod.Annotations[prefix+".image"] = container.Image
		pod.Annotations[prefix+".name"] = container.Name

		// Image information
		if err := p.addImageMetadata(ctx, pod, container, prefix); err != nil {
			log.G(ctx).Warnf("Failed to add image metadata: %v", err)
		}

		// Network information from RESTCONF
		if device.AppHostingMgr != nil && device.AppHostingMgr.restconfClient != nil {
			if err := p.addNetworkMetadata(ctx, pod, container, device, prefix); err != nil {
				log.G(ctx).Warnf("Failed to add network metadata: %v", err)
			}
		}

		// Deployment details
		pod.Annotations[prefix+".deployed-at"] = container.StartedAt.Format("2006-01-02T15:04:05Z07:00")
		if container.State == ContainerStateRunning {
			pod.Annotations[prefix+".status"] = "Running"
		} else {
			pod.Annotations[prefix+".status"] = string(container.State)
		}
	}

	log.G(ctx).Infof("✅ Added deployment metadata annotations")
	return nil
}

// addImageMetadata adds image-related metadata
func (p *CiscoProvider) addImageMetadata(ctx context.Context, pod *v1.Pod, container *Container, prefix string) error {
	imagePath := container.Image

	// Determine copy method
	copyMethod := "unknown"
	if strings.HasPrefix(imagePath, "file://") {
		copyMethod = "local-file"
		imagePath = strings.TrimPrefix(imagePath, "file://")
	} else if strings.HasPrefix(imagePath, "http://") || strings.HasPrefix(imagePath, "https://") {
		copyMethod = "http-download"
	} else if strings.Contains(imagePath, ":") {
		copyMethod = "scp-upload"
	}

	pod.Annotations[prefix+".copy-method"] = copyMethod
	pod.Annotations[prefix+".source-image-path"] = imagePath

	// If local file, get file info
	if copyMethod == "local-file" {
		if info, err := os.Stat(imagePath); err == nil {
			pod.Annotations[prefix+".image-size-bytes"] = fmt.Sprintf("%d", info.Size())
			pod.Annotations[prefix+".image-size-mb"] = fmt.Sprintf("%.2f", float64(info.Size())/(1024*1024))

			// Calculate checksum
			if checksum, err := calculateFileSHA256(imagePath); err == nil {
				pod.Annotations[prefix+".image-checksum-sha256"] = checksum
			}

			pod.Annotations[prefix+".image-name"] = filepath.Base(imagePath)
		}
	}

	return nil
}

// addNetworkMetadata queries device and adds network metadata
func (p *CiscoProvider) addNetworkMetadata(ctx context.Context, pod *v1.Pod, container *Container, device *ManagedDevice, prefix string) error {
	// Query app status via RESTCONF
	status, err := device.AppHostingMgr.restconfClient.GetStatus(ctx, container.ID)
	if err != nil {
		return fmt.Errorf("failed to query app status: %v", err)
	}

	// Add network information
	if status.Details.IPAddress != "" {
		pod.Annotations[prefix+".ip-address"] = status.Details.IPAddress
	}

	// Additional information
	pod.Annotations[prefix+".state"] = status.Details.State
	if status.Details.RunState != "" {
		pod.Annotations[prefix+".run-state"] = status.Details.RunState
	}
	if status.Details.Description != "" {
		pod.Annotations[prefix+".description"] = status.Details.Description
	}

	return nil
}

// calculateFileSHA256 calculates SHA256 checksum of a file
func calculateFileSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
