// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package fmc

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
	// FMC node. FMC support in CVK is health, operations, telemetry, and config only.
	ErrAppHostingUnsupported = errors.New("fmc does not support Cisco app-hosting")
	fmcAppHostingResource    = schema.GroupResource{Group: "cisco.vk", Resource: "fmc-apphosting"}
	fmcPodResource           = schema.GroupResource{Group: "cisco.vk", Resource: "fmc-pod"}
)

func (d *FMCDriver) DeployPod(ctx context.Context, pod *v1.Pod, _ corev1listers.SecretNamespaceLister, _ corev1listers.ConfigMapNamespaceLister) error {
	return apierrors.NewForbidden(fmcAppHostingResource, pod.Name, ErrAppHostingUnsupported)
}

func (d *FMCDriver) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	if pod.DeletionTimestamp != nil {
		return d.DeletePod(ctx, pod)
	}
	return apierrors.NewForbidden(fmcAppHostingResource, pod.Name, ErrAppHostingUnsupported)
}

func (d *FMCDriver) DeletePod(context.Context, *v1.Pod) error { return nil }

func (d *FMCDriver) GetPodStatus(ctx context.Context, pod *v1.Pod) (*v1.Pod, error) {
	return nil, apierrors.NewNotFound(fmcPodResource, pod.Name)
}

func (d *FMCDriver) ListPods(context.Context) ([]*v1.Pod, error) { return []*v1.Pod{}, nil }
