package iosxe

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/openconfig/ygot/ygot"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type UnmarshalFunc func([]byte, any) error

type XEDriver struct {
	config       *config.DeviceConfig
	client       common.NetworkClient
	marshaller   func(any) ([]byte, error)
	unmarshaller UnmarshalFunc
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

		if config.TLSConfig.CertFile != "" && config.TLSConfig.KeyFile != "" {
			cert, err := tls.LoadX509KeyPair(config.TLSConfig.CertFile, config.TLSConfig.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("failed to load client certificate: %v", err)
			}
			tlsConfig.Certificates = []tls.Certificate{cert}
		}

		if config.TLSConfig.CAFile != "" {
			caCert, err := os.ReadFile(config.TLSConfig.CAFile)
			if err != nil {
				return nil, fmt.Errorf("failed to read CA certificate: %v", err)
			}
			caCertPool := x509.NewCertPool()
			if !caCertPool.AppendCertsFromPEM(caCert) {
				return nil, fmt.Errorf("failed to parse CA certificate")
			}
			tlsConfig.RootCAs = caCertPool
		}
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

func (d *XEDriver) gethostMetaUnmarshaller() UnmarshalFunc {
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
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return fmt.Errorf("failed to parse JSON wrapper: %w", err)
		}

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
	res := &common.HostMeta{}

	err := d.client.Get(ctx, "/.well-known/host-meta", res, d.gethostMetaUnmarshaller())
	if err != nil {
		return fmt.Errorf("connectivity check failed: %w", err)
	}

	log.G(ctx).Debugf("Restconf Root: %s\n", res.Links[0].Href)
	return nil
}

func (d *XEDriver) GetDeviceResources(ctx context.Context) (*v1.ResourceList, error) {
	resources := v1.ResourceList{
		v1.ResourceCPU:     resource.MustParse("8"),
		v1.ResourceMemory:  resource.MustParse("16Gi"),
		v1.ResourceStorage: resource.MustParse("100Gi"),
		v1.ResourcePods:    resource.MustParse("16"),
	}

	return &resources, nil
}

func (d *XEDriver) DeployPod(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).WithFields(log.Fields{
		"pod": pod,
	}).Debug("Pod DeployContainer request received")

	err := d.CreatePodApps(ctx, pod)
	if err != nil {
		return fmt.Errorf("app deployment failed: %v", err)
	}

	return nil
}

func (d *XEDriver) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).Info("Pod UpdateContainer request received")
	return nil
}

func (d *XEDriver) DeletePod(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).WithFields(log.Fields{
		"pod": pod,
	}).Debugf("DeletePod request received for pod: %s", pod.Name)

	discoveredContainers, err := d.DiscoverPodContainersOnDevice(ctx, pod)
	if err != nil {
		log.G(ctx).Errorf("Failed to discover containers for pod %s/%s: %v", pod.Namespace, pod.Name, err)
		return fmt.Errorf("failed to discover containers for pod: %w", err)
	}

	foundCount := len(discoveredContainers)
	expectedCount := len(pod.Spec.Containers)

	log.G(ctx).Infof("Found %d containers on device for pod %s/%s (expected %d)",
		foundCount, pod.Namespace, pod.Name, expectedCount)

	if foundCount != expectedCount {
		log.G(ctx).Errorf("Container count mismatch for pod %s/%s: expected %d, found %d",
			pod.Namespace, pod.Name, expectedCount, foundCount)

		for _, container := range pod.Spec.Containers {
			if _, found := discoveredContainers[container.Name]; !found {
				log.G(ctx).Errorf("Container %s not found on device", container.Name)
			}
		}
	}

	deletionErrors := []string{}

	for containerName, appID := range discoveredContainers {
		log.G(ctx).Infof("Deleting container %s (app: %s)", containerName, appID)

		err = d.DeleteApp(ctx, appID)
		if err != nil {
			errMsg := fmt.Sprintf("failed to delete container %s (app %s): %v", containerName, appID, err)
			log.G(ctx).Error(errMsg)
			deletionErrors = append(deletionErrors, errMsg)
			continue
		}

		log.G(ctx).Infof("Successfully deleted container %s (app: %s)", containerName, appID)
	}

	if len(deletionErrors) > 0 {
		return fmt.Errorf("encountered %d errors during pod cleanup: %s",
			len(deletionErrors), strings.Join(deletionErrors, "; "))
	}

	log.G(ctx).Infof("Pod %s/%s cleanup successfully completed", pod.Namespace, pod.Name)
	return nil
}

func (d *XEDriver) DeleteApp(ctx context.Context, appID string) error {
	log.G(ctx).Infof("Stopping app %s", appID)
	if err := d.StopApp(ctx, appID); err != nil {
		return fmt.Errorf("failed to stop app: %w", err)
	}
	if err := d.WaitForAppStatus(ctx, appID, "ACTIVATED", 30*time.Second); err != nil {
		log.G(ctx).Warnf("App %s did not reach ACTIVATED status after stop: %v", appID, err)
	}

	log.G(ctx).Infof("Deactivating app %s", appID)
	if err := d.DeactivateApp(ctx, appID); err != nil {
		return fmt.Errorf("failed to deactivate app: %w", err)
	}
	if err := d.WaitForAppStatus(ctx, appID, "DEPLOYED", 30*time.Second); err != nil {
		log.G(ctx).Warnf("App %s did not reach DEPLOYED status after deactivate: %v", appID, err)
	}

	log.G(ctx).Infof("Uninstalling app %s", appID)
	if err := d.UninstallApp(ctx, appID); err != nil {
		return fmt.Errorf("failed to uninstall app: %w", err)
	}
	if err := d.WaitForAppNotPresent(ctx, appID, 60*time.Second); err != nil {
		log.G(ctx).Warnf("App %s still present in oper data after uninstall: %v", appID, err)
	}

	log.G(ctx).Infof("Removing app %s config", appID)
	path := fmt.Sprintf("/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps/app=%s", appID)
	if err := d.client.Delete(ctx, path); err != nil {
		return fmt.Errorf("failed to delete app config: %w", err)
	}

	log.G(ctx).Infof("Successfully deleted app %s", appID)
	return nil
}

func (d *XEDriver) GetPodStatus(ctx context.Context, pod *v1.Pod) (*v1.Pod, error) {

	log.G(ctx).Debug("GetPodStatus request received")

	discoveredContainers, err := d.DiscoverPodContainersOnDevice(ctx, pod)
	if err != nil {
		log.G(ctx).Debugf("failed to discover containers: %v", err)
		return nil, fmt.Errorf("apps for pod %s/%s not found on device", pod.Namespace, pod.Name)
	}

	if len(discoveredContainers) == 0 {
		log.G(ctx).Warnf("No containers found on device for pod %s/%s", pod.Namespace, pod.Name)
		return nil, fmt.Errorf("no containers found for pod %s/%s", pod.Namespace, pod.Name)
	}

	path := "/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data?fields=app"

	root := &Cisco_IOS_XEAppHostingOper_AppHostingOperData{}
	err = d.client.Get(ctx, path, root, d.getRestconfUnmarshaller())
	if err != nil {
		return nil, fmt.Errorf("bulk status fetch failed: %w", err)
	}

	appOperDataMap := make(map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App)

	for _, appID := range discoveredContainers {
		if operData, ok := root.App[appID]; ok {
			appOperDataMap[appID] = operData
		} else {
			log.G(ctx).Warnf("App %s configured but no operational data found", appID)
		}
	}

	d.debugLogJson(ctx, root)
	statusPod := pod.DeepCopy()

	// Use the new GetContainerStatus function
	err = d.GetContainerStatus(ctx, statusPod, discoveredContainers, appOperDataMap)
	if err != nil {
		return nil, fmt.Errorf("failed to get container status: %w", err)
	}

	return statusPod, nil
}

// GetContainerStatus maps device app containers to Kubernetes container statuses
func (d *XEDriver) GetContainerStatus(ctx context.Context, pod *v1.Pod,
	discoveredContainers map[string]string,
	appOperData map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App) error {

	now := metav1.Now()

	pod.Status = v1.PodStatus{
		Phase:     v1.PodPending,
		HostIP:    "1.1.1.2",
		PodIP:     "1.1.1.1",
		StartTime: &now,
		Conditions: []v1.PodCondition{
			{
				Type:               v1.PodInitialized,
				Status:             v1.ConditionFalse,
				LastTransitionTime: now,
			},
			{
				Type:               v1.PodReady,
				Status:             v1.ConditionFalse,
				LastTransitionTime: now,
			},
			{
				Type:               v1.PodScheduled,
				Status:             v1.ConditionTrue,
				LastTransitionTime: now,
			},
		},
	}

	allReady := true
	anyRunning := false

	for containerName, appID := range discoveredContainers {
		var containerSpec *v1.Container
		for i := range pod.Spec.Containers {
			if pod.Spec.Containers[i].Name == containerName {
				containerSpec = &pod.Spec.Containers[i]
				break
			}
		}

		if containerSpec == nil {
			log.G(ctx).Warnf("Container spec not found for %s (appID: %s)", containerName, appID)
			continue
		}

		operData := appOperData[appID]

		containerStatus := v1.ContainerStatus{
			Name:        containerName,
			Image:       containerSpec.Image,
			ImageID:     containerSpec.Image,
			ContainerID: fmt.Sprintf("cisco://%s", appID),
			Ready:       false,
		}

		if operData != nil && operData.Details != nil && operData.Details.State != nil {
			state := *operData.Details.State

			switch state {
			case "RUNNING":
				containerStatus.State = v1.ContainerState{
					Running: &v1.ContainerStateRunning{
						StartedAt: now,
					},
				}
				containerStatus.Ready = true
				anyRunning = true
			case "DEPLOYED", "Activated":
				containerStatus.State = v1.ContainerState{
					Waiting: &v1.ContainerStateWaiting{
						Reason:  "ContainerCreating",
						Message: fmt.Sprintf("App state: %s", state),
					},
				}
				allReady = false
			case "STOPPED", "Uninstalled":
				containerStatus.State = v1.ContainerState{
					Terminated: &v1.ContainerStateTerminated{
						ExitCode:   0,
						Reason:     "Completed",
						FinishedAt: now,
					},
				}
				allReady = false
			default:
				containerStatus.State = v1.ContainerState{
					Waiting: &v1.ContainerStateWaiting{
						Reason:  "Unknown",
						Message: fmt.Sprintf("App state: %s", state),
					},
				}
				allReady = false
			}

			log.G(ctx).Infof("Container %s (app: %s) state: %s, ready: %v",
				containerName, appID, state, containerStatus.Ready)
		} else {
			containerStatus.State = v1.ContainerState{
				Waiting: &v1.ContainerStateWaiting{
					Reason:  "ContainerCreating",
					Message: "No operational data available",
				},
			}
			allReady = false
			log.G(ctx).Warnf("No operational data for container %s (app: %s)", containerName, appID)
		}

		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, containerStatus)
	}

	if anyRunning && allReady {
		pod.Status.Phase = v1.PodRunning
		for i := range pod.Status.Conditions {
			if pod.Status.Conditions[i].Type == v1.PodReady ||
				pod.Status.Conditions[i].Type == v1.PodInitialized {
				pod.Status.Conditions[i].Status = v1.ConditionTrue
			}
		}
	} else if anyRunning {
		pod.Status.Phase = v1.PodRunning
	}

	log.G(ctx).Infof("Pod %s/%s status: Phase=%s, Containers=%d/%d ready",
		pod.Namespace, pod.Name, pod.Status.Phase,
		len(pod.Status.ContainerStatuses), len(pod.Spec.Containers))

	return nil
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

// DiscoverPodContainersOnDevice queries the device for configured apps matching the pod UID,
// then maps them back to container names using RunOpts labels.
// Returns a map of containerName -> appID (similar to GenerateContainerAppIDs but from device state).
func (d *XEDriver) DiscoverPodContainersOnDevice(ctx context.Context, pod *v1.Pod) (map[string]string, error) {
	path := "/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data"

	appsContainer := &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData{}

	err := d.client.Get(ctx, path, appsContainer, d.getRestconfUnmarshaller())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch app configs: %w", err)
	}

	// Clean the pod UID (remove hyphens) as that's how it appears in app names
	cleanUID := strings.ReplaceAll(string(pod.UID), "-", "")

	containerToAppID := make(map[string]string)

	for _, app := range appsContainer.Apps.App {
		if app.ApplicationName == nil {
			continue
		}

		appName := *app.ApplicationName

		// Check if app name contains the cleaned pod UID
		if !strings.Contains(appName, cleanUID) {
			continue
		}

		log.G(ctx).Debugf("Found app %s with matching pod UID", appName)

		// Extract container name from RunOpts labels
		var containerName string
		var runOptsLine string

		if app.RunOptss != nil {
			for _, opt := range app.RunOptss.RunOpts {
				if opt.LineRunOpts != nil {
					line := *opt.LineRunOpts
					runOptsLine = line

					log.G(ctx).Debugf("App %s RunOpts: %s", appName, line)

					// Verify this app belongs to our pod by checking all pod labels
					if strings.Contains(line, fmt.Sprintf("%s=%s", common.LabelPodName, pod.Name)) &&
						strings.Contains(line, fmt.Sprintf("%s=%s", common.LabelPodNamespace, pod.Namespace)) &&
						strings.Contains(line, fmt.Sprintf("%s=%s", common.LabelPodUID, pod.UID)) {

						// Extract the container name from the label
						containerName = common.ExtractContainerNameFromLabels(line)

						if containerName != "" {
							log.G(ctx).Debugf("Extracted container name: %s from app %s", containerName, appName)
						} else {
							log.G(ctx).Warnf("App %s has pod labels but no container name label in line: %s", appName, line)
						}
						break
					}
				}
			}
		}

		if containerName != "" {
			containerToAppID[containerName] = appName
			log.G(ctx).Infof("Discovered container %s -> app %s", containerName, appName)
		} else {
			log.G(ctx).Errorf("Found app %s with pod UID but couldn't extract container name from labels. RunOpts: %s",
				appName, runOptsLine)
		}
	}

	return containerToAppID, nil
}
