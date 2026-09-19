// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build envtest

package controller

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

// TestEnvtest_WorkerTemplateDefaultsAreAPIRoundTripStable guards the real
// API-defaulting boundary that fake clients do not implement. A stored
// PodTemplate must reproduce its injected worker revision, and rebuilding the
// desired Deployment must be a true CreateOrUpdate no-op.
func TestEnvtest_WorkerTemplateDefaultsAreAPIRoundTripStable(t *testing.T) {
	testEnv := &envtest.Environment{}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("envtest start: %v (is KUBEBUILDER_ASSETS set?)", err)
	}
	defer func() {
		if err := testEnv.Stop(); err != nil {
			t.Errorf("envtest stop: %v", err)
		}
	}()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	apiClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const namespace = "worker-template-defaults"
	if err := apiClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
		t.Fatal(err)
	}

	key := types.NamespacedName{Namespace: namespace, Name: "worker"}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}}
	mutate := func() error {
		labels := map[string]string{"app": "worker"}
		deployment.Spec.Replicas = ptr.To[int32](1)
		deployment.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		deployment.Spec.Strategy = appsv1.DeploymentStrategy{
			Type: appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{
				MaxUnavailable: ptr.To(intstr.FromString("25%")),
				MaxSurge:       ptr.To(intstr.FromString("25%")),
			},
		}
		deployment.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec: corev1.PodSpec{
				ServiceAccountName: "worker",
				Containers: []corev1.Container{{
					Name:  "worker",
					Image: "example.invalid/cisco-vk:test",
					Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("0.0001"),
					}},
					Env: []corev1.EnvVar{{
						Name: "POD_NAME",
						ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "metadata.name",
						}},
					}},
				}},
				Volumes: []corev1.Volume{{
					Name: "config",
					VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: "worker-config"},
					}},
				}},
			},
		}
		applyVKPodTemplateDefaults(&deployment.Spec.Template)
		revision, err := managedWorkerPodTemplateRevision(&deployment.Spec.Template)
		if err != nil {
			return err
		}
		deployment.Spec.Template.Annotations = map[string]string{
			managedprotocol.AnnotationWorkerConfigRevision: revision,
		}
		return nil
	}

	if op, err := controllerutil.CreateOrUpdate(ctx, apiClient, deployment, mutate); err != nil {
		t.Fatal(err)
	} else if op != controllerutil.OperationResultCreated {
		t.Fatalf("initial operation=%q, want created", op)
	}
	var stored appsv1.Deployment
	if err := apiClient.Get(ctx, key, &stored); err != nil {
		t.Fatal(err)
	}
	desiredRevision := stored.Spec.Template.Annotations[managedprotocol.AnnotationWorkerConfigRevision]
	storedRevision, err := managedWorkerPodTemplateRevision(&stored.Spec.Template)
	if err != nil {
		t.Fatal(err)
	}
	if storedRevision != desiredRevision {
		t.Fatalf("stored API-defaulted template revision=%q, want %q", storedRevision, desiredRevision)
	}

	deployment = &stored
	if op, err := controllerutil.CreateOrUpdate(ctx, apiClient, deployment, mutate); err != nil {
		t.Fatal(err)
	} else if op != controllerutil.OperationResultNone {
		t.Fatalf("API-round-tripped Deployment operation=%q, want unchanged", op)
	}
}
