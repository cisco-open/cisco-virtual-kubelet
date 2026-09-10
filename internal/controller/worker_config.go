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

package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const (
	envCVKWorkerResources    = "CVK_WORKER_RESOURCES"
	envCVKWorkerTmpSizeLimit = "CVK_WORKER_TMP_SIZE_LIMIT"
)

// resolveDeviceWorkerConfig combines release defaults with explicit per-device
// overrides. Resources is replaced as a unit, avoiding inherited limits that
// unexpectedly conflict with an override. No setting preserves legacy defaults.
func resolveDeviceWorkerConfig(override *ciskov1.DeviceWorkerConfig) (ciskov1.DeviceWorkerConfig, error) {
	var resolved ciskov1.DeviceWorkerConfig
	if raw := os.Getenv(envCVKWorkerResources); raw != "" {
		var resources ciskov1.DeviceWorkerResources
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&resources); err != nil {
			return resolved, fmt.Errorf("%s: %w", envCVKWorkerResources, err)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return resolved, fmt.Errorf("%s must contain one JSON object", envCVKWorkerResources)
		}
		resolved.Resources = &resources
	}
	if raw := os.Getenv(envCVKWorkerTmpSizeLimit); raw != "" {
		quantity, err := resource.ParseQuantity(raw)
		if err != nil {
			return resolved, fmt.Errorf("%s: %w", envCVKWorkerTmpSizeLimit, err)
		}
		resolved.TmpSizeLimit = &quantity
	}
	if err := resolved.Validate(); err != nil {
		return resolved, fmt.Errorf("manager worker defaults: %w", err)
	}
	if override != nil {
		if override.Resources != nil {
			resolved.Resources = &ciskov1.DeviceWorkerResources{
				Requests: corev1.ResourceList(override.Resources.Requests).DeepCopy(),
				Limits:   corev1.ResourceList(override.Resources.Limits).DeepCopy(),
			}
		}
		if override.TmpSizeLimit != nil {
			quantity := override.TmpSizeLimit.DeepCopy()
			resolved.TmpSizeLimit = &quantity
		}
	}
	return resolved, resolved.Validate()
}

func workerResourceRequirements(resources *ciskov1.DeviceWorkerResources) corev1.ResourceRequirements {
	if resources == nil {
		return corev1.ResourceRequirements{}
	}
	return corev1.ResourceRequirements{Requests: corev1.ResourceList(resources.Requests).DeepCopy(), Limits: corev1.ResourceList(resources.Limits).DeepCopy()}
}
