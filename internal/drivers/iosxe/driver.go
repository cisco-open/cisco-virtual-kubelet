package iosxe

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/openconfig/ygot/ygot"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
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
		// fmt.Print(wrapper)

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

func (d *XEDriver) DeployPod(ctx context.Context, pod *v1.Pod) error {
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

func (d *XEDriver) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	// TODO
	log.G(ctx).Info("Pod UpdateContainer request received")
	return nil
}

func (d *XEDriver) StopAndRemovePod(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).WithFields(log.Fields{
		"pod": pod,
	}).Debugf("Pod StopAndRemovePod request received for pod: %s", pod.Name)

	apps, err := d.FindAppsByPodLabels(ctx, pod)

	for _, app := range apps {

		appName := ""
		if app.ApplicationName != nil {
			appName = *app.ApplicationName
		} else {
			continue
		}

		log.G(ctx).Infof("Stopping and removing container: %s", appName)

		// 3. Perform the actual removal on the Cisco device
		err = d.StopAndRemoveContainer(ctx, appName)
		if err != nil {
			// If one app fails, we log it but continue trying to delete others
			log.G(ctx).Errorf("failed to delete app %s: %v", appName, err)
			continue
		}
	}

	log.G(ctx).Infof("Pod %s cleanup successfully completed", pod.Name)
	return nil
}

func (d *XEDriver) StopAndRemoveContainer(ctx context.Context, appName string) error {
	path := fmt.Sprintf("/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps/app=%s", appName)

	err := d.client.Delete(ctx, path)
	if err != nil {
		return fmt.Errorf("failed to delete app %s: %w", appName, err)
	}

	log.G(ctx).Infof("Pod %s successfully deleted", appName)

	return nil
}

func (d *XEDriver) GetPodStatus(ctx context.Context, pod *v1.Pod) (*v1.Pod, error) {

	log.G(ctx).Debug("GetContainerStatus request received")

	apps, err := d.FindAppsByPodLabels(ctx, pod)
	if err != nil {
		log.G(ctx).Debugf("failed to fetch app oper data", err)
		return nil, fmt.Errorf("apps for pod %s/%s not found on device", pod.Namespace, pod.Name)
	}

	path := "/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data?fields=app"

	root := &Cisco_IOS_XEAppHostingOper_AppHostingOperData{}
	err = d.client.Get(ctx, path, root, d.getRestconfUnmarshaller())
	if err != nil {
		return nil, fmt.Errorf("bulk status fetch failed: %w", err)
	}

	var appStatuses []*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App

	for _, appCfg := range apps {

		appName := ""
		if appCfg.ApplicationName != nil {
			appName = *appCfg.ApplicationName
		} else {
			continue
		}

		if operData, ok := root.App[appName]; ok {
			appStatuses = append(appStatuses, operData)
		} else {
			// Optional: log if an app is configured but the device has no oper data for it
			log.G(ctx).Warnf("App %s configured but no operational data found", appName)
		}
	}

	d.debugLogJson(ctx, root)
	statusPod := pod.DeepCopy()
	d.FakeContainerStatus(ctx, statusPod, appStatuses)

	// TODO We need some (hopefully) generic k8s-to-apphosting convertion funcs
	// ../common/kubernetes seems to make sense?
	// https://github.com/cisco-open/cisco-virtual-kubelet/issues/14
	return statusPod, nil

}

func (d *XEDriver) ListPods(ctx context.Context) ([]*v1.Pod, error) {

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

func (d *XEDriver) FindAppsByPodLabels(ctx context.Context, pod *v1.Pod) ([]*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App, error) {

	path := "/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps"

	appsContainer := &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData{}

	err := d.client.Get(ctx, path, appsContainer, d.getRestconfUnmarshaller())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch app configs: %w", err)
	}

	var matchedApps []*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App

	for _, app := range appsContainer.Apps.App {
		if app.RunOptss == nil {
			continue
		}

		isMatch := false
		for _, opt := range app.RunOptss.RunOpts {
			var line string
			if opt.LineRunOpts != nil {
				line = *opt.LineRunOpts
			}

			// 3. Check for matching labels
			if strings.Contains(line, fmt.Sprintf("io.kubernetes.pod.name=%s", pod.Name)) &&
				strings.Contains(line, fmt.Sprintf("io.kubernetes.pod.namespace=%s", pod.Namespace)) &&
				strings.Contains(line, fmt.Sprintf("io.kubernetes.pod.uid=%s", pod.UID)) {
				isMatch = true
				break
			}
		}

		if isMatch {
			matchedApps = append(matchedApps, app)
		}
	}

	if len(matchedApps) == 0 {
		return nil, fmt.Errorf("no Cisco apps found for pod %s/%s", pod.Namespace, pod.Name)
	}

	return matchedApps, nil
}

func (d *XEDriver) FakeContainerStatus(ctx context.Context, pod *v1.Pod, appStatuses []*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App) error {

	// Update pod status
	now := metav1.Now()
	pod.Status = v1.PodStatus{
		Phase:     v1.PodRunning,
		HostIP:    "1.1.1.2",
		PodIP:     "1.1.1.1",
		StartTime: &now,
		Conditions: []v1.PodCondition{
			{
				Type:               v1.PodInitialized,
				Status:             v1.ConditionTrue,
				LastTransitionTime: now,
			},
			{
				Type:               v1.PodReady,
				Status:             v1.ConditionTrue,
				LastTransitionTime: now,
			},
			{
				Type:               v1.PodScheduled,
				Status:             v1.ConditionTrue,
				LastTransitionTime: now,
			},
		},
	}

	for _, container := range pod.Spec.Containers {
		fmt.Printf("DEBUG FOUND CONTAINER: %s\n\n", container.Name)
		containerStatus := v1.ContainerStatus{
			Name:  container.Name,
			Image: container.Image,
			Ready: true,
			State: v1.ContainerState{
				Running: &v1.ContainerStateRunning{
					StartedAt: metav1.Now(),
				},
			},
			ContainerID: string(uuid.NewUUID()),
		}
		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, containerStatus)
	}

	return nil
}

func GetSortedAppNames(appStatuses []*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App) []string {
	var names []string

	for _, app := range appStatuses {
		if app.Name != nil {
			names = append(names, *app.Name)
		}
	}

	sort.Strings(names)

	return names
}
