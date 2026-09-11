// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
)

func TestReconcile_DisablingMutationGatesCompletesNonOverlappingCleanup(t *testing.T) {
	for _, gate := range []string{envCVKEnableSoftwareUpgrade, envCVKEnableWriteClassGNOI} {
		for _, globalDisable := range []bool{false, true} {
			name := gate
			if globalDisable {
				name += "/global-disable"
			}
			t.Run(name, func(t *testing.T) {
				t.Setenv(envCVKEnableSoftwareUpgrade, "false")
				t.Setenv(envCVKEnableWriteClassGNOI, "false")
				t.Setenv(envCVKGNOIDisabled, "false")
				t.Setenv(gate, "true")
				ctx := context.Background()
				device := newDevice("mutation-cleanup", "default")
				r := reconcilerFor(t, device)
				request := reconcileRequest(device.Namespace, device.Name)
				key := types.NamespacedName{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}
				var deployment appsv1.Deployment
				reconcile := func() {
					t.Helper()
					if _, err := r.Reconcile(ctx, request); err != nil {
						t.Fatal(err)
					}
					if err := r.Get(ctx, key, &deployment); err != nil {
						t.Fatal(err)
					}
				}
				reconcile()
				if deployment.Annotations[gnoiMutationWorkerAnnotation] != "true" {
					t.Fatal("enabled worker did not record mutation lifecycle")
				}
				// Migrate a pre-annotation worker using its existing template.
				delete(deployment.Annotations, gnoiMutationWorkerAnnotation)
				if err := r.Update(ctx, &deployment); err != nil {
					t.Fatal(err)
				}
				if globalDisable {
					t.Setenv(envCVKGNOIDisabled, "true")
				} else {
					t.Setenv(gate, "false")
				}
				reconcile()
				assertPending := func() {
					t.Helper()
					if deployment.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType || deployment.Annotations[gnoiMutationWorkerAnnotation] != "true" {
						t.Fatalf("cleanup lost Recreate before enabled worker stopped: strategy=%s annotations=%v", deployment.Spec.Strategy.Type, deployment.Annotations)
					}
					if podTemplateEnablesGNOIMutations(&deployment.Spec.Template.Spec) {
						t.Fatal("cleanup template still enables mutations")
					}
				}
				assertPending()
				// No rollout status and repeated reconciliation must retain the
				// marker even though the current template is already disabled.
				reconcile()
				assertPending()
				deployment.Generation = 7 // fake client does not increment generations
				if err := r.Update(ctx, &deployment); err != nil {
					t.Fatal(err)
				}
				for _, status := range []appsv1.DeploymentStatus{
					{ObservedGeneration: 6, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1},
					{ObservedGeneration: 7, Replicas: 2, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1},
					{ObservedGeneration: 7, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1, TerminatingReplicas: ptr.To[int32](1)},
				} {
					deployment.Status = status
					if err := r.Status().Update(ctx, &deployment); err != nil {
						t.Fatal(err)
					}
					reconcile()
					assertPending()
				}
				deployment.Status.TerminatingReplicas = nil
				if err := r.Status().Update(ctx, &deployment); err != nil {
					t.Fatal(err)
				}
				reconcile()
				if deployment.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType || deployment.Annotations[gnoiMutationWorkerAnnotation] != "" {
					t.Fatalf("completed disabled rollout did not restore RollingUpdate: strategy=%s annotations=%v", deployment.Spec.Strategy.Type, deployment.Annotations)
				}
				reconcile()
				if deployment.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType {
					t.Fatal("disabled worker reentered mutation lifecycle")
				}
			})
		}
	}
}
