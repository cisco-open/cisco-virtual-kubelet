// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	core "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReadMetadataConsumerPods is manager-only observation of recognizable device
// workers. A deleted/replaced/unknown source or unqualified legacy actor cannot
// disappear merely because the ordinary device census is now empty. It never
// cancels workers, mutates leases or interprets Pod absence as a dispatch grant.
func ReadMetadataConsumerPods(ctx context.Context, reader client.Reader, binding RuntimeAdmissionBinding, serials []string) error {
	reject := errors.New("metadata cleanup requires known disjoint current device-worker footprints")
	if reader == nil || binding.Validate() != nil || len(serials) == 0 || len(serials) > 256 {
		return reject
	}
	selected := map[string]bool{}
	for _, s := range serials {
		canonical, err := topologyidentity.CanonicalPhysicalIdentity(s)
		if err != nil || canonical != s || selected[s] {
			return reject
		}
		selected[s] = true
	}
	for range 2 {
		var pods core.PodList
		if err := reader.List(ctx, &pods); err != nil {
			return err
		}
		if pods.Continue != "" || pods.RemainingItemCount != nil && *pods.RemainingItemCount > 0 {
			return reject
		}
		for _, pod := range pods.Items {
			if pod.Status.Phase == core.PodSucceeded || pod.Status.Phase == core.PodFailed {
				continue
			}
			_, deviceMarker := pod.Annotations[managedprotocol.AnnotationDeviceUID]
			if pod.Labels["app.kubernetes.io/managed-by"] != "ciscodevice-controller" && !deviceMarker {
				continue
			}
			if len(pod.Spec.Containers) != 1 || len(pod.Spec.InitContainers) != 0 || len(pod.Spec.EphemeralContainers) != 0 {
				return reject
			}
			c := pod.Spec.Containers[0]
			if c.Name != "cisco-vk" {
				return reject
			}
			values := map[string]string{}
			for _, key := range []string{managedprotocol.EnvDeviceNamespace, managedprotocol.EnvDeviceName, managedprotocol.EnvDeviceUID, managedprotocol.EnvWorkerMode, managedprotocol.EnvExpectedWorkerUsername} {
				v, err := MetadataRuntimeLiteralEnv(c, key)
				if err != nil {
					return reject
				}
				values[key] = v
			}
			if values[managedprotocol.EnvDeviceNamespace] != pod.Namespace {
				return reject
			}
			account := binding.AppHostingServiceAccount
			switch values[managedprotocol.EnvWorkerMode] {
			case managedprotocol.WorkerModeAppHosting:
			case managedprotocol.WorkerModeNetworkManagement:
				account = binding.NetworkManagementServiceAccount
			default:
				return reject
			}
			if pod.Spec.ServiceAccountName != account || values[managedprotocol.EnvExpectedWorkerUsername] != "system:serviceaccount:"+pod.Namespace+":"+account {
				return reject
			}
			var device cvk.CiscoDevice
			if err := reader.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: values[managedprotocol.EnvDeviceName]}, &device); err != nil {
				return err
			}
			if string(device.UID) != values[managedprotocol.EnvDeviceUID] || device.UID == "" || device.Generation < 1 {
				return reject
			}
			if recorded := pod.Annotations[managedprotocol.AnnotationDeviceUID]; recorded != "" && recorded != string(device.UID) {
				return reject
			}
			identities, err := DevicePhysicalIdentities(&device)
			if err != nil || len(identities) == 0 {
				return reject
			}
			for _, id := range identities {
				if selected[id] {
					return reject
				}
			}
			var fresh cvk.CiscoDevice
			if err := reader.Get(ctx, client.ObjectKeyFromObject(&device), &fresh); err != nil {
				return err
			}
			if fresh.UID != device.UID || fresh.ResourceVersion != device.ResourceVersion || !reflect.DeepEqual(fresh.Spec, device.Spec) || !reflect.DeepEqual(fresh.Status.NodeIdentity, device.Status.NodeIdentity) {
				return reject
			}
		}
	}
	return nil
}
