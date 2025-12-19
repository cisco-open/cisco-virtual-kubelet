package cisco

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
)

// LogStreamer provides streaming health vitals for containers
type LogStreamer struct {
	containerID string
	appID       string
	restconf    *RESTCONFAppHostingClient
	stopChan    chan struct{}
}

// NewLogStreamer creates a new log streamer for a container
func NewLogStreamer(containerID, appID string, restconf *RESTCONFAppHostingClient) *LogStreamer {
	return &LogStreamer{
		containerID: containerID,
		appID:       appID,
		restconf:    restconf,
		stopChan:    make(chan struct{}),
	}
}

// Stream returns a ReadCloser that streams health vitals every 60 seconds
func (ls *LogStreamer) Stream(ctx context.Context) io.ReadCloser {
	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()

		// Write initial header
		header := fmt.Sprintf("===========================================\n")
		header += fmt.Sprintf("Container Health Monitor\n")
		header += fmt.Sprintf("Container ID: %s\n", ls.containerID)
		header += fmt.Sprintf("App ID: %s\n", ls.appID)
		header += fmt.Sprintf("Started: %s\n", time.Now().Format(time.RFC3339))
		header += fmt.Sprintf("===========================================\n\n")
		pw.Write([]byte(header))

		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		// Send first update immediately
		if err := ls.writeHealthVitals(ctx, pw); err != nil {
			log.G(ctx).Errorf("Failed to write health vitals: %v", err)
		}

		for {
			select {
			case <-ctx.Done():
				pw.Write([]byte("\n[Log stream terminated by context]\n"))
				return
			case <-ls.stopChan:
				pw.Write([]byte("\n[Log stream terminated]\n"))
				return
			case <-ticker.C:
				if err := ls.writeHealthVitals(ctx, pw); err != nil {
					log.G(ctx).Errorf("Failed to write health vitals: %v", err)
					// Continue streaming even if one update fails
				}
			}
		}
	}()

	return pr
}

// writeHealthVitals queries the device and writes current health information
func (ls *LogStreamer) writeHealthVitals(ctx context.Context, w io.Writer) error {
	timestamp := time.Now().Format(time.RFC3339)

	// Create context with timeout for RESTCONF query
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Query app status via RESTCONF
	status, err := ls.restconf.GetStatus(queryCtx, ls.appID)
	if err != nil {
		errMsg := fmt.Sprintf("[%s] ❌ ERROR: Failed to query app status: %v\n\n", timestamp, err)
		_, writeErr := w.Write([]byte(errMsg))
		return writeErr
	}

	// Build health vitals report
	var report strings.Builder

	report.WriteString(fmt.Sprintf("-------------------------------------------\n"))
	report.WriteString(fmt.Sprintf("[%s] Health Check\n", timestamp))
	report.WriteString(fmt.Sprintf("-------------------------------------------\n"))

	// Basic Status
	report.WriteString(fmt.Sprintf("App Name:           %s\n", status.Name))
	report.WriteString(fmt.Sprintf("State:              %s\n", status.Details.State))
	if status.Details.RunState != "" {
		report.WriteString(fmt.Sprintf("Run State:          %s\n", status.Details.RunState))
	}

	// Network Configuration
	if status.Details.IPAddress != "" {
		report.WriteString(fmt.Sprintf("\nNetwork Configuration:\n"))
		report.WriteString(fmt.Sprintf("  IP Address:       %s\n", status.Details.IPAddress))
	}

	// Application Details
	if status.Details.Description != "" {
		report.WriteString(fmt.Sprintf("\nApplication Details:\n"))
		report.WriteString(fmt.Sprintf("  Description:      %s\n", status.Details.Description))
	}

	// Health Status
	report.WriteString(fmt.Sprintf("\nHealth Status:\n"))
	healthStatus := getHealthStatus(status.Details.State)
	healthEmoji := getHealthEmoji(healthStatus)
	report.WriteString(fmt.Sprintf("  Overall:          %s %s\n", healthEmoji, healthStatus))

	// Additional diagnostics
	if status.Details.State == "ERROR" {
		report.WriteString(fmt.Sprintf("\n⚠️  WARNING: Application is in ERROR state\n"))
	} else if status.Details.State == "STOPPED" {
		report.WriteString(fmt.Sprintf("\n⚠️  WARNING: Application is STOPPED\n"))
	} else if status.Details.State == "RUNNING" {
		report.WriteString(fmt.Sprintf("\n✅ Application is healthy and running\n"))
	}

	report.WriteString(fmt.Sprintf("\nNext update in 60 seconds...\n\n"))

	_, err = w.Write([]byte(report.String()))
	return err
}

// Stop stops the log stream
func (ls *LogStreamer) Stop() {
	close(ls.stopChan)
}

// getHealthStatus determines health status from state
func getHealthStatus(state string) string {
	switch state {
	case "RUNNING":
		return "Healthy"
	case "ACTIVATED":
		return "Activated (Not Started)"
	case "DEPLOYED":
		return "Deployed (Not Activated)"
	case "STOPPED":
		return "Stopped"
	case "ERROR":
		return "Error"
	default:
		return "Unknown"
	}
}

// getHealthEmoji returns an emoji for the health status
func getHealthEmoji(health string) string {
	switch health {
	case "Healthy":
		return "✅"
	case "Activated (Not Started)":
		return "⚡"
	case "Deployed (Not Activated)":
		return "📦"
	case "Stopped":
		return "⏸️"
	case "Error":
		return "❌"
	default:
		return "❓"
	}
}
