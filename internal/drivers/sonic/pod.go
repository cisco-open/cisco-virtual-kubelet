// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package sonic

import (
	"context"
	"errors"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

var (
	// ErrAppHostingUnsupported is returned when a workload is scheduled to a
	// SONiC node. SONiC support in CVK is health, operations, telemetry, and
	// OpenConfig configuration only.
	ErrAppHostingUnsupported = errors.New("sonic does not support Cisco app-hosting")
	sonicAppHostingResource  = schema.GroupResource{Group: "cisco.vk", Resource: "sonic-apphosting"}
	sonicPodResource         = schema.GroupResource{Group: "cisco.vk", Resource: "sonic-pod"}
)

func (d *SONICDriver) DeployPod(ctx context.Context, pod *v1.Pod, _ corev1listers.SecretNamespaceLister, _ corev1listers.ConfigMapNamespaceLister) error {
	return apierrors.NewForbidden(sonicAppHostingResource, pod.Name, ErrAppHostingUnsupported)
}

func (d *SONICDriver) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	if pod.DeletionTimestamp != nil {
		return d.DeletePod(ctx, pod)
	}
	return apierrors.NewForbidden(sonicAppHostingResource, pod.Name, ErrAppHostingUnsupported)
}

func (d *SONICDriver) DeletePod(context.Context, *v1.Pod) error { return nil }

func (d *SONICDriver) GetPodStatus(ctx context.Context, pod *v1.Pod) (*v1.Pod, error) {
	return nil, apierrors.NewNotFound(sonicPodResource, pod.Name)
}

func (d *SONICDriver) ListPods(context.Context) ([]*v1.Pod, error) { return []*v1.Pod{}, nil }
