// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// planningWorkerSnapshot is private to one buildFrozenPlan call. Planning
// publishes intent, never execution authority. All subsequent admission,
// promotion and claim paths keep the reconciler's original uncached reader.
// Kubernetes lists are individually consistent, not a cross-kind transaction;
// the existing UID, owner, revision and health checks still validate joins.
type planningWorkerSnapshot struct {
	client.Reader
	namespace   string
	pods        []corev1.Pod
	replicaSets map[string]*appsv1.ReplicaSet
	nodes       map[string]*corev1.Node
	deployments map[client.ObjectKey]*appsv1.Deployment
}

func newPlanningWorkerSnapshot(ctx context.Context, reader client.Reader, namespace string) (*planningWorkerSnapshot, error) {
	if reader == nil || namespace == "" {
		return nil, fmt.Errorf("planning worker snapshot requires a reader and namespace")
	}
	var pods corev1.PodList
	if err := reader.List(ctx, &pods, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list planning worker Pods: %w", err)
	}
	var sets appsv1.ReplicaSetList
	if err := reader.List(ctx, &sets, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list planning worker ReplicaSets: %w", err)
	}
	var nodes corev1.NodeList
	if err := reader.List(ctx, &nodes); err != nil {
		return nil, fmt.Errorf("list planning Nodes: %w", err)
	}
	var deployments appsv1.DeploymentList
	if err := reader.List(ctx, &deployments, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list planning worker Deployments: %w", err)
	}
	s := &planningWorkerSnapshot{Reader: reader, namespace: namespace, pods: pods.Items,
		replicaSets: make(map[string]*appsv1.ReplicaSet, len(sets.Items)),
		nodes:       make(map[string]*corev1.Node), deployments: make(map[client.ObjectKey]*appsv1.Deployment)}
	for i := range sets.Items {
		s.replicaSets[sets.Items[i].Name] = &sets.Items[i]
	}
	for i := range nodes.Items {
		s.nodes[nodes.Items[i].Name] = &nodes.Items[i]
	}
	for i := range deployments.Items {
		s.deployments[client.ObjectKeyFromObject(&deployments.Items[i])] = &deployments.Items[i]
	}
	return s, nil
}

func (s *planningWorkerSnapshot) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(opts) != 0 {
		return s.Reader.Get(ctx, key, obj, opts...)
	}
	switch out := obj.(type) {
	case *appsv1.ReplicaSet:
		if key.Namespace != s.namespace {
			break
		}
		value := s.replicaSets[key.Name]
		if value == nil {
			return apierrors.NewNotFound(appsv1.Resource("replicasets"), key.Name)
		}
		value.DeepCopyInto(out)
		return nil
	case *corev1.Node:
		if key.Namespace != "" {
			break
		}
		value := s.nodes[key.Name]
		if value == nil {
			return apierrors.NewNotFound(corev1.Resource("nodes"), key.Name)
		}
		value.DeepCopyInto(out)
		return nil
	case *appsv1.Deployment:
		if key.Namespace != s.namespace {
			break
		}
		value := s.deployments[key]
		if value == nil {
			return apierrors.NewNotFound(appsv1.Resource("deployments"), key.Name)
		}
		value.DeepCopyInto(out)
		return nil
	}
	return s.Reader.Get(ctx, key, obj, opts...)
}

func (s *planningWorkerSnapshot) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	out, ok := list.(*corev1.PodList)
	options := (&client.ListOptions{}).ApplyOptions(opts)
	if !ok || options.Namespace != s.namespace || options.FieldSelector != nil ||
		options.Limit != 0 || options.Continue != "" || options.Raw != nil {
		return s.Reader.List(ctx, list, opts...)
	}
	out.Items = nil
	for i := range s.pods {
		pod := &s.pods[i]
		if options.LabelSelector == nil || options.LabelSelector.Matches(labels.Set(pod.Labels)) {
			out.Items = append(out.Items, *pod.DeepCopy())
		}
	}
	return nil
}
