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

package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// OTELTopologyExporter periodically collects topology data from a Cisco device
// and emits OTLP traces to a configured collector endpoint via gRPC.
//
// It uses the upstream Virtual Kubelet trace/opentelemetry adapter backed by
// the OTEL SDK TracerProvider with an otlptracegrpc exporter. The trace
// structure matches the TCL otel.tcl script:
//   - Root span (kind=SERVER): represents the device node with identity attributes
//   - Child spans (kind=CLIENT): one per neighbor link with peer.service and link attributes
type OTELTopologyExporter struct {
	driver   drivers.CiscoKubernetesDeviceDriver
	topo     drivers.TopologyProvider
	config   *v1alpha1.OTELConfig
	nodeName string
	tp       *sdktrace.TracerProvider
	tracer   oteltrace.Tracer
}

// NewOTELTopologyExporter creates a new exporter instance and initialises the
// OTEL SDK TracerProvider with an otlptracegrpc span exporter.
//
// The caller is responsible for wiring the global OTEL TracerProvider and VK
// trace adapter if desired (see run.go).
func NewOTELTopologyExporter(
	ctx context.Context,
	driver drivers.CiscoKubernetesDeviceDriver,
	topo drivers.TopologyProvider,
	config *v1alpha1.OTELConfig,
	nodeName string,
	deviceAddress string,
) (*OTELTopologyExporter, error) {
	// Build gRPC exporter options
	grpcOpts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(config.Endpoint),
	}
	if config.Insecure {
		grpcOpts = append(grpcOpts, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, grpcOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP gRPC exporter: %w", err)
	}

	serviceName := config.ServiceName
	if serviceName == "" {
		serviceName = "cisco-network"
	}

	// Build SDK resource with service identity
	res, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			semconv.ServiceNameKey.String(fmt.Sprintf("%s.%s", serviceName, nodeName)),
			semconv.ServiceNamespaceKey.String("network.infrastructure"),
			attribute.String("host.name", nodeName),
			attribute.String("device.address", deviceAddress),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTEL resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	tracer := tp.Tracer("cisco-virtual-kubelet/topology")

	return &OTELTopologyExporter{
		driver:   driver,
		topo:     topo,
		config:   config,
		nodeName: nodeName,
		tp:       tp,
		tracer:   tracer,
	}, nil
}

// TracerProvider returns the underlying SDK TracerProvider so callers can wire
// it as the global OTEL provider.
func (e *OTELTopologyExporter) TracerProvider() *sdktrace.TracerProvider {
	return e.tp
}

// Run starts the background topology export loop. It blocks until ctx is cancelled.
func (e *OTELTopologyExporter) Run(ctx context.Context) {
	interval := time.Duration(e.config.IntervalSecs) * time.Second
	if interval < 10*time.Second {
		interval = 60 * time.Second
	}

	log.G(ctx).Infof("OTEL topology exporter started: endpoint=%s interval=%s serviceName=%s",
		e.config.Endpoint, interval, e.config.ServiceName)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.tp.Shutdown(shutdownCtx); err != nil {
			log.G(ctx).WithError(err).Warn("OTEL: TracerProvider shutdown error")
		}
		log.G(ctx).Info("OTEL topology exporter stopped")
	}()

	// Emit immediately on start, then on each tick
	e.emitTopology(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.emitTopology(ctx)
		}
	}
}

// emitTopology collects device topology data and emits it as OTEL trace spans.
func (e *OTELTopologyExporter) emitTopology(ctx context.Context) {
	// Collect device identity
	deviceInfo, err := e.driver.GetDeviceInfo(ctx)
	if err != nil || deviceInfo == nil {
		log.G(ctx).WithError(err).Warn("OTEL: failed to get device info, skipping emission")
		return
	}

	// Collect neighbors
	cdpNeighbors, cdpErr := e.topo.GetCDPNeighbors(ctx)
	if cdpErr != nil {
		log.G(ctx).WithError(cdpErr).Debug("OTEL: failed to get CDP neighbors")
	}

	ospfNeighbors, ospfErr := e.topo.GetOSPFNeighbors(ctx)
	if ospfErr != nil {
		log.G(ctx).WithError(ospfErr).Debug("OTEL: failed to get OSPF neighbors")
	}

	// Collect interface stats for link utilization
	ifStats, ifErr := e.topo.GetInterfaceStats(ctx)
	if ifErr != nil {
		log.G(ctx).WithError(ifErr).Debug("OTEL: failed to get interface stats")
	}

	// Collect interface IPs for resource attributes
	interfaceIPs, ipErr := e.topo.GetInterfaceIPs(ctx)
	if ipErr != nil {
		log.G(ctx).WithError(ipErr).Debug("OTEL: failed to get interface IPs")
	}

	// Consolidate neighbors (merge CDP + OSPF by interface, like the TCL script)
	consolidated := consolidateNeighbors(cdpNeighbors, ospfNeighbors)

	// Build interface stats lookup
	ifStatsMap := make(map[string]common.InterfaceStats)
	for _, s := range ifStats {
		ifStatsMap[s.Name] = s
	}

	hostname := e.nodeName
	if deviceInfo.Hostname != "" {
		hostname = deviceInfo.Hostname
	}

	serviceName := e.config.ServiceName
	if serviceName == "" {
		serviceName = "cisco-network"
	}

	// Build IP list
	var ipList []string
	for _, ip := range interfaceIPs {
		ipList = append(ipList, ip.IPv4)
	}

	// --- Root span: represents the device node ---
	rootCtx, rootSpan := e.tracer.Start(ctx, fmt.Sprintf("node.%s", hostname),
		oteltrace.WithSpanKind(oteltrace.SpanKindServer),
		oteltrace.WithAttributes(
			attribute.String("node.type", "network_device"),
			attribute.String("node.role", "router"),
			attribute.Int("node.neighbor_count", len(consolidated)),
			attribute.Int("node.interface_count", len(interfaceIPs)),
			attribute.String("router.id", deviceInfo.RouterID),
			attribute.String("router.platform", deviceInfo.ProductID),
			attribute.String("router.os.version", deviceInfo.SoftwareVersion),
			attribute.String("router.serial", deviceInfo.SerialNumber),
			attribute.String("router.ip.addresses", strings.Join(ipList, ",")),
			attribute.String("network.layer", "L3"),
			attribute.String("network.type", "routed"),
		),
	)

	// --- Child spans: one per neighbor link ---
	for _, n := range consolidated {
		peerServiceName := fmt.Sprintf("%s.%s", serviceName, n.DeviceID)
		linkID := fmt.Sprintf("%s:%s->%s:%s", hostname, n.LocalInterface, n.DeviceID, n.RemoteInterface)

		linkState := "up"
		if n.OSPFState != "" && n.OSPFState != "full" && n.OSPFState != "2way" {
			linkState = "degraded"
		}

		linkAttrs := []attribute.KeyValue{
			attribute.String("peer.service", peerServiceName),
			attribute.String("service.type", "network-device"),
			attribute.String("deployment.environment", "network-infrastructure"),
			attribute.String("net.peer.name", n.DeviceID),
			attribute.String("net.peer.ip", n.IP),
			attribute.String("net.host.interface", n.LocalInterface),
			attribute.String("net.peer.interface", n.RemoteInterface),
			attribute.String("link.type", "physical"),
			attribute.String("link.protocols", strings.Join(n.Protocols, "+")),
			attribute.String("topology.link_id", linkID),
			attribute.String("topology.layer", "network"),
			attribute.String("peer.platform", n.Platform),
			attribute.String("peer.capabilities", n.Capabilities),
			attribute.String("link.state", linkState),
		}

		if n.OSPFState != "" {
			linkAttrs = append(linkAttrs,
				attribute.String("ospf.neighbor.state", n.OSPFState),
				attribute.String("ospf.area", n.OSPFArea),
			)
		}

		if stats, ok := ifStatsMap[n.LocalInterface]; ok {
			linkAttrs = append(linkAttrs,
				attribute.Int64("link.utilization.in.bps", int64(stats.InBitsPerSec)),
				attribute.Int64("link.utilization.out.bps", int64(stats.OutBitsPerSec)),
				attribute.Int64("link.speed.bps", int64(stats.Speed)),
			)
		}

		_, linkSpan := e.tracer.Start(rootCtx,
			fmt.Sprintf("link.%s->%s", n.LocalInterface, n.DeviceID),
			oteltrace.WithSpanKind(oteltrace.SpanKindClient),
			oteltrace.WithAttributes(linkAttrs...),
		)
		linkSpan.End()
	}

	// --- Child spans: one per hosted app-hosting container ---
	hostedApps, appErr := e.topo.GetHostedApps(ctx)
	if appErr != nil {
		log.G(ctx).WithError(appErr).Warn("OTEL: failed to get hosted apps")
	}
	for _, app := range hostedApps {
		// Use a distinct peer.service pattern so apps are visually
		// separated from network neighbors on the service map:
		//   Network links:  "cisco-network.<neighbor>"
		//   Hosted apps:    "app.<namespace>/<podName>"
		peerSvc := fmt.Sprintf("app.%s/%s", app.PodNamespace, app.PodName)

		appAttrs := []attribute.KeyValue{
			attribute.String("peer.service", peerSvc),
			attribute.String("service.type", "app-hosting"),
			attribute.String("deployment.environment", "edge-compute"),
			attribute.String("app.id", app.AppID),
			attribute.String("app.state", app.State),
			attribute.String("app.type", "container"),
			attribute.String("k8s.pod.name", app.PodName),
			attribute.String("k8s.pod.namespace", app.PodNamespace),
			attribute.String("k8s.pod.uid", app.PodUID),
			attribute.String("k8s.container.name", app.ContainerName),
			attribute.String("topology.link_id", fmt.Sprintf("%s->%s", hostname, peerSvc)),
			attribute.String("topology.layer", "app-hosting"),
		}
		if app.IPv4Address != "" {
			appAttrs = append(appAttrs, attribute.String("app.ip", app.IPv4Address))
		}
		if app.MACAddress != "" {
			appAttrs = append(appAttrs, attribute.String("app.mac", app.MACAddress))
		}
		if app.AttachedInterface != "" {
			appAttrs = append(appAttrs, attribute.String("net.host.interface", app.AttachedInterface))
		}

		_, appSpan := e.tracer.Start(rootCtx,
			fmt.Sprintf("hosted.%s/%s", app.PodNamespace, app.PodName),
			oteltrace.WithSpanKind(oteltrace.SpanKindClient),
			oteltrace.WithAttributes(appAttrs...),
		)
		appSpan.End()
	}

	rootSpan.End()

	log.G(ctx).Infof("OTEL: emitted topology trace with %d link spans and %d app spans", len(consolidated), len(hostedApps))
}

// consolidatedNeighbor merges CDP and OSPF data for the same link.
type consolidatedNeighbor struct {
	DeviceID        string
	IP              string
	LocalInterface  string
	RemoteInterface string
	Platform        string
	Capabilities    string
	Protocols       []string
	OSPFState       string
	OSPFArea        string
}

// consolidateNeighbors merges CDP and OSPF neighbor lists by local interface.
func consolidateNeighbors(cdp []common.CDPNeighbor, ospf []common.OSPFNeighbor) []consolidatedNeighbor {
	byInterface := make(map[string]*consolidatedNeighbor)

	for _, n := range cdp {
		key := n.LocalInterface
		if _, ok := byInterface[key]; !ok {
			byInterface[key] = &consolidatedNeighbor{
				LocalInterface: n.LocalInterface,
			}
		}
		cn := byInterface[key]
		cn.DeviceID = n.DeviceID
		cn.IP = n.IP
		cn.RemoteInterface = n.RemoteInterface
		cn.Platform = n.Platform
		cn.Capabilities = n.Capabilities
		cn.Protocols = append(cn.Protocols, "cdp")
	}

	for _, n := range ospf {
		key := n.Interface
		if _, ok := byInterface[key]; !ok {
			byInterface[key] = &consolidatedNeighbor{
				LocalInterface: n.Interface,
				DeviceID:       n.NeighborID,
				IP:             n.Address,
			}
		}
		cn := byInterface[key]
		cn.Protocols = append(cn.Protocols, "ospf")
		cn.OSPFState = n.State
		cn.OSPFArea = n.Area
		if cn.IP == "" {
			cn.IP = n.Address
		}
		if cn.DeviceID == "" {
			cn.DeviceID = n.NeighborID
		}
	}

	var result []consolidatedNeighbor
	for _, cn := range byInterface {
		result = append(result, *cn)
	}
	return result
}
