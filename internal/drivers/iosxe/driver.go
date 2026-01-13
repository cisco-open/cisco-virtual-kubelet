package iosxe

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type XEDriver struct {
	config *config.DeviceConfig
	Client common.NetworkClient
	// baseURL string
	// token      string
	// schema     *DiscoveredEndpoints // Dynamically discovered schema endpoints
}

func NewAppHostingDriver(ctx context.Context, config *config.DeviceConfig) (*XEDriver, error) {
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

	BaseUrl := u.String()
	Timeout := 30 * time.Second
	Client, err := common.NewNetworkClient(
		BaseUrl,
		&common.ClientAuth{
			Method:   "BasicAuth",
			Username: config.Username,
			Password: config.Password,
		},
		tlsConfig,
		Timeout,
	)

	d := &XEDriver{
		config: config,
		Client: Client,
	}

	err = d.CheckConnection(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to validate device connection: %v", err)
	}
	log.G(ctx).WithFields(log.Fields{
		"url":      BaseUrl,
		"platform": "IOS-XE",
	}).Info("Connected to IOSXE device")

	return d, nil
}

func (d *XEDriver) CheckConnection(ctx context.Context) error {

	data := &common.HostMeta{}
	err := d.Client.Get(ctx, "/.well-known/host-meta", data)
	if err != nil {
		return fmt.Errorf("connectivity check failed: %w", err)
	}
	fmt.Print(data.XMLName)
	return nil
}

func (d *XEDriver) GetDeviceResources(ctx context.Context) (*v1.ResourceList, error) {

	// TODO: Fake it for now.  Pull from device later
	resources := v1.ResourceList{
		v1.ResourceCPU:     resource.MustParse("8"),
		v1.ResourceMemory:  resource.MustParse("16Gi"),
		v1.ResourceStorage: resource.MustParse("100Gi"),
		v1.ResourcePods:    resource.MustParse("16"),
	}

	return &resources, nil
}

func (d *XEDriver) DeployContainer(ctx context.Context, pod *v1.Pod) error {
	// Check/Download container image
	// Create continaer
	// Start container
	log.G(ctx).WithFields(log.Fields{
		"pod": pod,
	}).Debug("Pod DeployContainer request received")

	err := d.ConfigureAppContainer(ctx, pod)
	if err != nil {
		return fmt.Errorf("app deployment failed: %v", err)
	}

	return nil
}

func (d *XEDriver) UpdateContainer(ctx context.Context, pod *v1.Pod) error {
	// TODO
	log.G(ctx).Info("Pod UpdateContainer request received")
	return nil
}

func (d *XEDriver) StopAndRemoveContainer(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).WithFields(log.Fields{
		"pod": pod,
	}).Info("Pod StopAndRemoveContainer request received")
	return nil
}

func (d *XEDriver) GetContainerStatus(ctx context.Context, namespace, name string) (*v1.Pod, error) {
	// TODO
	log.G(ctx).Info("Pod GetContainerStatus request received")
	return nil, fmt.Errorf("GetContainerStatus NOT YET IMPLEMENTED")
}

func (d *XEDriver) ListContainers(ctx context.Context) ([]*v1.Pod, error) {
	// TODO
	log.G(ctx).Info("Pod ListContainers request received")
	return nil, nil
}
