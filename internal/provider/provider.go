package provider

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	v1 "k8s.io/api/core/v1"
)

type AppHostingProvider struct {
	ctx    context.Context
	appCfg *config.Config
	driver drivers.CiscoDeviceDriver // Injected via factory
	mutex  sync.RWMutex
}

func NewAppHostingProvider(
	ctx context.Context,
	appCfg *config.Config,
	vkCfg nodeutil.ProviderConfig,
) (*AppHostingProvider, error) {

	// TODO: We should do auto-discovery (or config) in NewDriver
	d, err := drivers.NewDriver(ctx, &appCfg.Device)
	if err != nil {
		return nil, fmt.Errorf("driver assignment failed: %v", err)
	}
	return &AppHostingProvider{
		ctx:    ctx,
		appCfg: appCfg,
		driver: d,
	}, nil
}

func (p *AppHostingProvider) GetCapacity(ctx context.Context) (v1.ResourceList, error) {
	resources, err := p.driver.GetDeviceResources(p.ctx)
	return *resources, err
}

func (p *AppHostingProvider) CreatePod(ctx context.Context, pod *v1.Pod) error {
	if err := p.driver.DeployContainer(p.ctx, pod); err != nil {
		// Return wrapped errors from github.com/virtual-kubelet/virtual-kubelet/errdefs
		return errdefs.AsInvalidInput(err)
	}
	return nil
}

func (p *AppHostingProvider) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	// IOS-XE/XR may have limited "Update" support (e.g., changing resources requires a restart)
	return p.driver.UpdateContainer(p.ctx, pod)
}

func (p *AppHostingProvider) DeletePod(ctx context.Context, pod *v1.Pod) error {
	return p.driver.StopAndRemoveContainer(p.ctx, pod)
}

func (p *AppHostingProvider) GetPod(ctx context.Context, namespace, name string) (*v1.Pod, error) {
	// Not fully implemented
	// return p.driver.GetContainerAsPod(ctx, name)
	pod, err := p.driver.GetContainerStatus(p.ctx, namespace, name)
	if err != nil {
		return nil, errdefs.AsNotFound(err)
	}
	// Map Cisco container state back to a Kubernetes Pod object
	// return mapCiscoToK8sPod(container)
	return pod, nil
}

func (p *AppHostingProvider) GetPodStatus(ctx context.Context, namespace, name string) (*v1.PodStatus, error) {
	pod, err := p.driver.GetContainerStatus(p.ctx, name, namespace)
	if err != nil {
		return nil, errdefs.AsNotFound(err)
	}
	// Map Cisco container state back to a Kubernetes Pod object
	return &pod.Status, nil
}

func (p *AppHostingProvider) GetPods(ctx context.Context) ([]*v1.Pod, error) {
	pods, err := p.driver.ListContainers(p.ctx)
	if err != nil {
		return nil, errdefs.AsNotFound(err)
	}

	return pods, nil
}

func (p *AppHostingProvider) AttachToContainer(ctx context.Context, namespace, podName, containerName string, attach api.AttachIO) error {
	// log.G(ctx).Infof("Attaching to container %s in pod %s/%s", containerName, namespace, podName)

	// For Cisco IOx containers, attachment is limited
	// We can simulate it by providing a shell prompt
	if attach.Stdout() != nil {
		attach.Stdout().Write([]byte("Cisco IOx container attachment not fully supported\n"))
	}

	return nil
}

// NOT YET IMPLEMENTED

// GetContainerLogs implements nodeutil.Provider.
func (p *AppHostingProvider) GetContainerLogs(ctx context.Context, namespace string, podName string, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	panic("unimplemented")
}

// GetMetricsResource implements nodeutil.Provider.
func (p *AppHostingProvider) GetMetricsResource(context.Context) ([]*io_prometheus_client.MetricFamily, error) {
	panic("unimplemented")
}

// GetStatsSummary implements nodeutil.Provider.
func (p *AppHostingProvider) GetStatsSummary(context.Context) (*statsv1alpha1.Summary, error) {
	panic("unimplemented")
}

// PortForward implements nodeutil.Provider.
func (p *AppHostingProvider) PortForward(ctx context.Context, namespace string, pod string, port int32, stream io.ReadWriteCloser) error {
	panic("unimplemented")
}

// RunInContainer implements nodeutil.Provider.
func (p *AppHostingProvider) RunInContainer(ctx context.Context, namespace string, podName string, containerName string, cmd []string, attach api.AttachIO) error {
	panic("unimplemented")
}

// AppHostingNode implements node.NodeProvider for proper heartbeat management.
// This follows the NaiveNodeProvider pattern from virtual-kubelet.
// The library's NodeController handles periodic heartbeat updates automatically.
type AppHostingNode struct{}

// NewAppHostingNode creates a new AppHostingNode
func NewAppHostingNode(
	ctx context.Context,
	appCfg *config.Config,
	vkCfg nodeutil.ProviderConfig,
) (*AppHostingNode, error) {
	return &AppHostingNode{}, nil
}

// Ping implements node.NodeProvider.
// Called periodically by the library's nodePingController.
// Returning nil indicates the node is healthy.
func (a *AppHostingNode) Ping(ctx context.Context) error {
	return nil
}

// NotifyNodeStatus implements node.NodeProvider.
// This is for async/event-driven status updates (e.g., device health changes).
// The library's controlLoop handles periodic heartbeat updates automatically.
func (a *AppHostingNode) NotifyNodeStatus(ctx context.Context, cb func(*v1.Node)) {
	// No-op - library handles periodic updates via controlLoop and updateNodeStatusHeartbeat()
}
