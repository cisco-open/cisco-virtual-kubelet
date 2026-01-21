package iosxe

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/openconfig/ygot/ygot"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type UnmarshalFunc func([]byte, any) error

type XEDriver struct {
	config       *config.DeviceConfig
	client       common.NetworkClient
	marshaller   func(any) ([]byte, error)
	unmarshaller UnmarshalFunc
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
		client: Client,
	}

	// Signal future intent for client marshaller selection
	protocol := "restconf"
	if protocol == "restconf" {
		d.marshaller = d.getRestconfMarshaller()
		d.unmarshaller = d.getRestconfUnmarshaller()
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

// These marshallers/unmarshallers may be more generic later
// depending on how we implement Netconf support
// Therefore could move to common package

func (d *XEDriver) gethostMetaUnmarshaller() UnmarshalFunc {
	// If we find a more useful checkConnection path
	// we can deprecate this awkward function
	return func(data []byte, v any) error {
		decoder := xml.NewDecoder(bytes.NewReader(data))
		decoder.Strict = false
		return decoder.Decode(v)
	}
}

func (d *XEDriver) getRestconfMarshaller() func(any) ([]byte, error) {
	return func(v any) ([]byte, error) {
		gs, ok := v.(ygot.GoStruct)
		if !ok {
			return nil, fmt.Errorf("value is not a ygot.GoStruct")
		}
		// EmitJSON returns (string, error)
		jsonStr, err := ygot.EmitJSON(gs, &ygot.EmitJSONConfig{
			Format: ygot.RFC7951,
			RFC7951Config: &ygot.RFC7951JSONConfig{
				AppendModuleName: true,
			},
			SkipValidation: true,
		})
		return []byte(jsonStr), err
	}
}

func (d *XEDriver) getRestconfUnmarshaller() UnmarshalFunc {
	return func(data []byte, v any) error {
		// RESTconf always wraps responses in the object name.
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return fmt.Errorf("failed to parse JSON wrapper: %w", err)
		}

		// Check if the JSON data is wrapped
		// Not sure how robust this will be ...
		var innerData []byte
		if len(wrapper) == 1 {
			for _, val := range wrapper {
				innerData = val
			}
		} else {
			innerData = data
		}

		gs, ok := v.(ygot.GoStruct)
		if !ok {
			return fmt.Errorf("target is not a ygot.GoStruct")
		}

		return Unmarshal(innerData, gs)
	}
}

func (d *XEDriver) CheckConnection(ctx context.Context) error {

	// There may be a more useful func for this
	// where we can glean some device-info
	res := &common.HostMeta{}

	err := d.client.Get(ctx, "/.well-known/host-meta", res, d.gethostMetaUnmarshaller())
	if err != nil {
		return fmt.Errorf("connectivity check failed: %w", err)
	}

	log.G(ctx).Debugf("Restconf Root: %s\n", res.Links[0].Href)
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
	}).Debugf("Pod StopAndRemoveContainer request received for pod: %s", pod.Name)

	path := fmt.Sprintf("/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps/app=%s", pod.Name)

	err := d.client.Delete(ctx, path)
	if err != nil {
		return fmt.Errorf("failed to delete app %s: %w", pod.Name, err)
	}

	log.G(ctx).Infof("Pod %s successfully deleted", pod.Name)

	return nil
}

func (d *XEDriver) GetContainerStatus(ctx context.Context, namespace, name string) (*v1.Pod, error) {

	path := "/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data?fields=app"

	root := &Cisco_IOS_XEAppHostingOper_AppHostingOperData{}

	log.G(ctx).Debug("GetContainerStatus request received")

	err := d.client.Get(ctx, path, root, d.unmarshaller)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch app oper data: %w", err)
	}

	app, ok := root.App[name]
	if !ok {
		return nil, fmt.Errorf("app %s not found on device", name)
	}

	d.debugLogJson(ctx, app)

	// TODO We need some (hopefully) generic k8s-to-apphosting convertion funcs
	// ../common/kubernetes seems to make sense?
	// https://github.com/cisco-open/cisco-virtual-kubelet/issues/14
	return &v1.Pod{}, nil

}

func (d *XEDriver) ListContainers(ctx context.Context) ([]*v1.Pod, error) {

	path := "/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data?fields=app"

	res := &Cisco_IOS_XEAppHostingOper_AppHostingOperData{}

	err := d.client.Get(ctx, path, res, d.unmarshaller)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch app oper data: %w", err)
	}

	pods := []*v1.Pod{}
	return pods, nil
}

func (d *XEDriver) debugLogJson(ctx context.Context, obj ygot.GoStruct) error {
	// EmitJSON expects a ygot.GoStruct as its first argument
	jsonStr, err := ygot.EmitJSON(obj, &ygot.EmitJSONConfig{
		Format: ygot.RFC7951,
		Indent: "  ",
		RFC7951Config: &ygot.RFC7951JSONConfig{
			AppendModuleName: true,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to serialize ygot object: %w", err)
	}

	// Print to console (or use your logger)
	log.G(ctx).Debug(jsonStr)
	return nil
}
