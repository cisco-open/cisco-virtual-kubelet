// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package ise

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
	// ISE node. ISE support in CVK is health, operations, telemetry, and config only.
	ErrAppHostingUnsupported = errors.New("ise does not support Cisco app-hosting")
	iseAppHostingResource    = schema.GroupResource{Group: "cisco.vk", Resource: "ise-apphosting"}
	isePodResource           = schema.GroupResource{Group: "cisco.vk", Resource: "ise-pod"}
)

func (d *ISEDriver) DeployPod(ctx context.Context, pod *v1.Pod, _ corev1listers.SecretNamespaceLister, _ corev1listers.ConfigMapNamespaceLister) error {
	return apierrors.NewForbidden(iseAppHostingResource, pod.Name, ErrAppHostingUnsupported)
}

func (d *ISEDriver) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	if pod.DeletionTimestamp != nil {
		return d.DeletePod(ctx, pod)
	}
	return apierrors.NewForbidden(iseAppHostingResource, pod.Name, ErrAppHostingUnsupported)
}

func (d *ISEDriver) DeletePod(context.Context, *v1.Pod) error { return nil }

func (d *ISEDriver) GetPodStatus(ctx context.Context, pod *v1.Pod) (*v1.Pod, error) {
	return nil, apierrors.NewNotFound(isePodResource, pod.Name)
}

func (d *ISEDriver) ListPods(context.Context) ([]*v1.Pod, error) { return []*v1.Pod{}, nil }
