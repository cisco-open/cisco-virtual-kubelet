// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
)

func intentFixture(t *testing.T) (client.Client, *ops.ControllerConfigurationReservation, *config.NetworkControllerConfig, *cvk.NetworkController) {
	t.Helper()
	c, r, _ := grantFixture(t)
	_ = config.AddToScheme(c.Scheme())
	ctx := context.Background()
	var nc cvk.NetworkController
	if err := c.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Controller.Name}, &nc); err != nil {
		t.Fatal(err)
	}
	nc.Spec.Type = "catalyst-center"
	if err := c.Update(ctx, &nc); err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration.Owners = []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: r.Spec.Config.Name, ConfigUID: r.Spec.Config.UID}}
	if err := c.Status().Update(ctx, &nc); err != nil {
		t.Fatal(err)
	}
	intent := &config.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: r.Spec.Config.Name, Namespace: r.Namespace, UID: "config-uid", Generation: r.Spec.ConfigGeneration}, Spec: config.NetworkControllerConfigSpec{ControllerRef: config.NetworkControllerRef{Name: nc.Name}, Scope: "global", ManagedSections: []string{"inventory"}, Mode: config.NetworkControllerApplyModeApply, ModelSource: config.NetworkControllerNetAsCodeModelSource{Resolved: true, ModelVersion: "0.5.0"}, Source: config.NetworkControllerConfigurationSource{Inline: &runtime.RawExtension{Raw: []byte(`{}`)}}}}
	if err := c.Create(ctx, intent); err != nil {
		t.Fatal(err)
	}
	return c, r, intent, &nc
}
func TestIntentBinding(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(*config.NetworkControllerConfig, *cvk.NetworkController){
		"report mode": func(i *config.NetworkControllerConfig, n *cvk.NetworkController) {
			i.Spec.Mode = config.NetworkControllerApplyModeReport
		},
		"changed config":    func(i *config.NetworkControllerConfig, n *cvk.NetworkController) { i.Generation++ },
		"paused controller": func(i *config.NetworkControllerConfig, n *cvk.NetworkController) { n.Spec.Paused = true },
		"other adapter":     func(i *config.NetworkControllerConfig, n *cvk.NetworkController) { n.Spec.Type = "nexus-dashboard" },
		"unresolved model":  func(i *config.NetworkControllerConfig, n *cvk.NetworkController) { i.Spec.ModelSource.Resolved = false },
		"section removed":   func(i *config.NetworkControllerConfig, n *cvk.NetworkController) { i.Spec.ManagedSections = nil },
		"foreign owner": func(i *config.NetworkControllerConfig, n *cvk.NetworkController) {
			n.Status.Configuration.Owners[0].ConfigUID = "other"
		},
		"journal already submitted": func(i *config.NetworkControllerConfig, n *cvk.NetworkController) {
			n.Status.Configuration.Operation.Phase = "Accepted"
		},
		"credential injection": func(i *config.NetworkControllerConfig, n *cvk.NetworkController) {
			i.Spec.SecretRefs = []config.NetworkControllerSecretRef{{Source: "credential"}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c, r, intent, nc := intentFixture(t)
			if err := ReadIntentBinding(ctx, c, r, "catalyst-center"); err != nil {
				t.Fatal(err)
			}
			mutate(intent, nc)
			// Preserve the test's status mutation across the spec update subresource split.
			status := nc.Status.DeepCopy()
			if err := c.Update(ctx, intent); err != nil {
				t.Fatal(err)
			}
			if err := c.Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			nc.Status = *status
			if err := c.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if ReadIntentBinding(ctx, c, r, "catalyst-center") == nil {
				t.Fatal("revoked intent accepted")
			}
		})
	}
}
func TestIntentConfigMapRevision(t *testing.T) {
	ctx := context.Background()
	c, r, intent, nc := intentFixture(t)
	cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "source", Namespace: r.Namespace, UID: "source-uid"}, Data: map[string]string{"model.yaml": "catalyst_center: {}"}}
	if err := c.Create(ctx, cm); err != nil {
		t.Fatal(err)
	}
	intent.Spec.Source = config.NetworkControllerConfigurationSource{ConfigMapRef: &config.ConfigMapKeyRef{Name: cm.Name, Key: "model.yaml"}}
	if err := c.Update(ctx, intent); err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration.Operation.SourceUID = string(cm.UID)
	nc.Status.Configuration.Operation.SourceVersion = cm.ResourceVersion
	r.Spec.OperationDigest, _ = OperationDigest(nc.Status.Configuration.Operation)
	if err := c.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := ReadIntentBinding(ctx, c, r, "catalyst-center"); err != nil {
		t.Fatal(err)
	}
	cm.Data["model.yaml"] = "changed"
	if err := c.Update(ctx, cm); err != nil {
		t.Fatal(err)
	}
	if ReadIntentBinding(ctx, c, r, "catalyst-center") == nil {
		t.Fatal("changed source revision accepted")
	}
}

type changingIntentReader struct {
	client.Reader
	reads int
}

func (r *changingIntentReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if intent, ok := obj.(*config.NetworkControllerConfig); ok {
		r.reads++
		if r.reads == 2 {
			intent.ResourceVersion = "changed"
		}
	}
	return nil
}
func TestIntentBindingRechecksDeclaration(t *testing.T) {
	c, r, _, _ := intentFixture(t)
	if ReadIntentBinding(context.Background(), &changingIntentReader{Reader: c}, r, "catalyst-center") == nil {
		t.Fatal("accepted intent changed during source reads")
	}
}
