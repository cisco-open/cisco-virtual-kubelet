// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"crypto/sha256"
	"fmt"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/tlsutil"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const deviceTLSCAVolume = "device-tls-ca"
const deviceTLSCAMount = "/var/run/secrets/cisco-vk/device-tls-ca"
const deviceTLSCAConfigKey = "device-ca.crt"

type deviceTLSCAProjection struct {
	name, revision, digest string
	publicPEM              string
	valid                  bool
}

// Invalid or absent material still produces a replacement template: keeping
// the old template would leave its old trust authorized after revocation.
// Projected keys and the driver's CA loader prevent the replacement starting.
// Unexpected API errors leave the existing template unchanged and retry.
func (r *CiscoDeviceReconciler) inspectDeviceTLSCA(ctx context.Context, device *ciskov1.CiscoDevice) (deviceTLSCAProjection, error) {
	var state deviceTLSCAProjection
	tls := device.Spec.TLS
	if tls == nil || tls.CASecretRef == nil {
		return state, nil
	}
	state.name = tls.CASecretRef.Name
	if !tls.Enabled || tls.InsecureSkipVerify || tls.CAFile != "" || len(validation.IsDNS1123Subdomain(state.name)) != 0 {
		return state, fmt.Errorf("device TLS caSecretRef requires a valid Secret name, verified TLS and no caFile")
	}
	var secret corev1.Secret
	if err := r.reader().Get(ctx, client.ObjectKey{Namespace: device.Namespace, Name: state.name}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			state.revision = "missing"
			return state, fmt.Errorf("device TLS CA Secret %s/%s was not found", device.Namespace, state.name)
		}
		return state, &projectedSecretReadError{fmt.Errorf("read device TLS CA Secret: %w", err)}
	}
	state.revision = secret.ResourceVersion
	state.digest = fmt.Sprintf("%x", sha256.Sum256(secret.Data["ca.crt"]))
	if err := tlsutil.ValidatePublicCABundle(secret.Data["ca.crt"]); err != nil {
		return state, fmt.Errorf("device TLS CA Secret %s/%s ca.crt: %w", device.Namespace, state.name, err)
	}
	state.valid = true
	// Freeze validated public bytes. Never project the mutable source Secret:
	// kubelet could otherwise deliver a later key-containing update without
	// passing through this validation, even to an already running worker.
	state.publicPEM = string(secret.Data["ca.crt"])
	return state, nil
}

func (state deviceTLSCAProjection) project(template *corev1.PodTemplateSpec, configMapName string) {
	if state.name == "" {
		return
	}
	if template.Annotations == nil {
		template.Annotations = map[string]string{}
	}
	template.Annotations[managedprotocol.AnnotationDeviceTLSCARevision] = state.revision
	volume := corev1.Volume{Name: deviceTLSCAVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}
	if state.valid {
		volume.VolumeSource = corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
			DefaultMode: ptr.To[int32](0o440),
			Sources: []corev1.VolumeProjection{{ConfigMap: &corev1.ConfigMapProjection{
				LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
				Items:                []corev1.KeyToPath{{Key: deviceTLSCAConfigKey, Path: "ca.crt"}},
			}}},
		}}
	}
	// Never mount invalid material: ca.crt could accidentally contain a signer
	// key. An empty mount both removes old trust and prevents replacement startup.
	template.Spec.Volumes = append(template.Spec.Volumes, volume)
	c := &template.Spec.Containers[0]
	// An older binary must reject this contract rather than silently ignoring
	// the new runtime trust-revision/digest fence carried in environment values.
	c.Args = append(c.Args, "--device-tls-ca-projection="+managedprotocol.DeviceTLSCAProjectionVersion)
	c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: deviceTLSCAVolume, MountPath: deviceTLSCAMount, ReadOnly: true})
	c.Env = append(c.Env, corev1.EnvVar{Name: managedprotocol.EnvDeviceTLSCARevision, Value: state.revision})
	c.Env = append(c.Env, corev1.EnvVar{Name: managedprotocol.EnvDeviceTLSCADigest, Value: state.digest})
}
