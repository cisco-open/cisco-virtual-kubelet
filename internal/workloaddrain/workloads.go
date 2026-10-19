// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package workloaddrain

import (
	"context"
	"errors"
	"fmt"

	core "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// BoundActiveWorkloads reads across namespaces using an authoritative reader.
// Terminating Pods remain active; retained terminal Pods are ignored only when
// not terminating. Device-side inventory must be checked independently.
func BoundActiveWorkloads(ctx context.Context, reader client.Reader, nodeName string) ([]core.Pod, error) {
	if reader == nil || nodeName == "" {
		return nil, errors.New("workload reader or Node identity is unavailable")
	}
	var pods core.PodList
	if err := reader.List(ctx, &pods, client.MatchingFields{"spec.nodeName": nodeName}); err != nil {
		return nil, err
	}
	active := make([]core.Pod, 0, len(pods.Items))
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Spec.NodeName != nodeName {
			continue
		}
		if pod.DeletionTimestamp == nil && (pod.Status.Phase == core.PodSucceeded || pod.Status.Phase == core.PodFailed) {
			continue
		}
		active = append(active, *pod.DeepCopy())
	}
	return active, nil
}

// VerifyEmptyWorkloads requires the explicitly supported strict native scanner;
// ordinary compatibility inventory cannot prove that foreign apps are absent.
// Recheck Kubernetes after the device call to catch Pods bound during the scan.
// Callers independently hold and revalidate their exact maintenance authority.
func VerifyEmptyWorkloads(ctx context.Context, reader client.Reader, nodeName string, strictScan func(context.Context) ([]*core.Pod, error)) error {
	if strictScan == nil {
		return errors.New("strict device application inventory is unavailable")
	}
	check := func() error {
		pods, err := BoundActiveWorkloads(ctx, reader, nodeName)
		if err != nil {
			return err
		}
		if len(pods) != 0 {
			return fmt.Errorf("%d active Kubernetes workload(s) remain", len(pods))
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	apps, err := strictScan(ctx)
	if err != nil {
		return err
	}
	if len(apps) != 0 {
		return errors.New("device application inventory is not empty")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return check()
}
