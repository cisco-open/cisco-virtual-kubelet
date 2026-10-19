// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"errors"
	"testing"

	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type metadataCancellationReadError struct {
	client.Reader
	key client.ObjectKey
}

func (r metadataCancellationReadError) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if key == r.key {
		return apierrors.NewForbidden(schema.GroupResource{Resource: "configuration-source"}, key.Name, errors.New("qualification read denied"))
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}

func TestMetadataCancellationInvalidation(t *testing.T) {
	for _, mode := range []string{"inline-current", "paused", "controller-generation", "config-gone", "config-generation", "config-uid", "config-denied", "model-current", "model-version", "model-uid", "model-gone", "model-denied"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			n := validNetworkController("catc", "catalyst-center")
			n.Generation = 1
			op := &cvk.NetworkControllerConfigurationOperation{ControllerGeneration: 1, ConfigName: "intent", ConfigUID: "intent", ConfigGeneration: 1}
			n.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: op}
			c := &config.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: "intent", Namespace: n.Namespace, UID: "intent", Generation: 1}, Spec: config.NetworkControllerConfigSpec{ControllerRef: config.NetworkControllerRef{Name: n.Name}, Source: config.NetworkControllerConfigurationSource{Inline: &runtime.RawExtension{Raw: []byte(`{}`)}}}}
			r, k := newNetworkControllerReconciler(t, n, c)
			r.APIReader = k
			if len(mode) >= 6 && mode[:6] == "model-" {
				cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: n.Namespace, UID: "model"}, Data: map[string]string{"intent": "{}"}}
				if err := k.Create(ctx, cm); err != nil {
					t.Fatal(err)
				}
				op.SourceUID = string(cm.UID)
				op.SourceVersion = cm.ResourceVersion
				c.Spec.Source.Inline = nil
				c.Spec.Source.ConfigMapRef = &config.ConfigMapKeyRef{Name: cm.Name, Key: "intent"}
				if err := k.Get(ctx, client.ObjectKeyFromObject(c), c); err != nil {
					t.Fatal(err)
				}
				c.Spec.Source.Inline = nil
				c.Spec.Source.ConfigMapRef = &config.ConfigMapKeyRef{Name: cm.Name, Key: "intent"}
				if err := k.Update(ctx, c); err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "model-version":
					op.SourceVersion = "old"
				case "model-uid":
					op.SourceUID = "old"
				case "model-gone":
					if err := k.Delete(ctx, cm); err != nil {
						t.Fatal(err)
					}
				case "model-denied":
					r.APIReader = metadataCancellationReadError{Reader: k, key: client.ObjectKeyFromObject(cm)}
				}
			}
			switch mode {
			case "paused":
				n.Spec.Paused = true
			case "controller-generation":
				n.Generation++
			case "config-gone":
				if err := k.Delete(ctx, c); err != nil {
					t.Fatal(err)
				}
			case "config-generation":
				op.ConfigGeneration = 2
			case "config-uid":
				op.ConfigUID = "old"
			case "config-denied":
				r.APIReader = metadataCancellationReadError{Reader: k, key: client.ObjectKeyFromObject(c)}
			}
			invalid, err := r.metadataPreparationInvalidated(ctx, n)
			denied := mode == "model-denied" || mode == "config-denied"
			want := mode != "inline-current" && mode != "model-current" && !denied
			if invalid != want || (err != nil) != denied {
				t.Fatal("incorrect source invalidation", mode, invalid, err)
			}
		})
	}
}
