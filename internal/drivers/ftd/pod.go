// Copyright (c) 2026 Cisco Systems Inc.
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

package ftd

import (
	"context"
	"errors"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

var (
	// ErrAppHostingUnsupported is returned when a workload is scheduled to an
	// FTD node. FTD support in CVK is health, operations, and telemetry only.
	ErrAppHostingUnsupported = errors.New("ftd does not support Cisco app-hosting")
	ftdAppHostingResource    = schema.GroupResource{Group: "cisco.vk", Resource: "ftd-apphosting"}
	ftdPodResource           = schema.GroupResource{Group: "cisco.vk", Resource: "ftd-pod"}
)

func (d *FTDDriver) DeployPod(ctx context.Context, pod *v1.Pod, _ corev1listers.SecretNamespaceLister, _ corev1listers.ConfigMapNamespaceLister) error {
	return apierrors.NewForbidden(ftdAppHostingResource, pod.Name, ErrAppHostingUnsupported)
}

func (d *FTDDriver) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	if pod.DeletionTimestamp != nil {
		return d.DeletePod(ctx, pod)
	}
	return apierrors.NewForbidden(ftdAppHostingResource, pod.Name, ErrAppHostingUnsupported)
}

func (d *FTDDriver) DeletePod(ctx context.Context, pod *v1.Pod) error {
	return nil
}

func (d *FTDDriver) GetPodStatus(ctx context.Context, pod *v1.Pod) (*v1.Pod, error) {
	return nil, apierrors.NewNotFound(ftdPodResource, pod.Name)
}

func (d *FTDDriver) ListPods(ctx context.Context) ([]*v1.Pod, error) {
	return []*v1.Pod{}, nil
}
