// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controller

import (
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	adapter "github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	"reflect"
	"testing"
)

func TestConfigurationAdmissionTemplateIsolation(t *testing.T) {
	binding := &contract.RuntimeAdmissionBinding{Protocol: contract.Protocol, ManagerUsername: "system:serviceaccount:system:manager", AdmissionPrefix: "cvk", PolicyNamespace: "system", PolicyName: "policy", LedgerName: "ledger", AppHostingServiceAccount: "app", NetworkManagementServiceAccount: "network", LeaseNamespace: "leases"}
	for _, eligible := range []bool{false, true} {
		app := &core.PodTemplateSpec{Spec: core.PodSpec{Containers: []core.Container{{Name: "worker"}}}}
		network := app.DeepCopy()
		encoded, err := injectConfigurationAdmissionBinding(network, binding, eligible)
		if err != nil {
			t.Fatal(err)
		}
		if len(app.Spec.Containers[0].Env) != 0 {
			t.Fatal("binding leaked to copied app template")
		}
		if eligible {
			if len(network.Spec.Containers[0].Env) != 1 || encoded == "" {
				t.Fatal("eligible worker missing binding")
			}
			got, err := contract.ParseRuntimeAdmissionBinding(network.Spec.Containers[0].Env[0].Value)
			if err != nil || *got != *binding {
				t.Fatal("binding changed", err)
			}
			if _, err := injectConfigurationAdmissionBinding(network, binding, true); err == nil {
				t.Fatal("duplicate environment accepted")
			}
		} else if encoded != "" || len(network.Spec.Containers[0].Env) != 0 {
			t.Fatal("ineligible worker gained binding")
		}
	}
	if encoded, err := injectConfigurationAdmissionBinding(nil, nil, true); encoded != "" || err != nil {
		t.Fatal("nil binding must preserve compatibility")
	}
	invalid := *binding
	invalid.ManagerUsername = ""
	if _, err := injectConfigurationAdmissionBinding(&core.PodTemplateSpec{Spec: core.PodSpec{Containers: []core.Container{{Name: "worker"}}}}, &invalid, true); err == nil {
		t.Fatal("invalid binding published")
	}
}

func TestControllerAdmissionBindingChangesTemplateContract(t *testing.T) {
	b := &contract.RuntimeAdmissionBinding{Protocol: contract.Protocol, ManagerUsername: "system:serviceaccount:system:manager", AdmissionPrefix: "cvk", PolicyNamespace: "system", PolicyName: "policy", LedgerName: "ledger", AppHostingServiceAccount: "app", NetworkManagementServiceAccount: "network", LeaseNamespace: "leases"}
	for _, kind := range []string{"catalyst-center", "nexus-dashboard"} {
		t.Run(kind, func(t *testing.T) {
			nc := validNetworkController("cc", kind)
			r, _ := newNetworkControllerReconciler(t, nc)
			descriptor := adapter.Descriptor{Type: kind}
			before, after := &apps.Deployment{}, &apps.Deployment{}
			if err := r.mutateWorkerDeployment(nc, descriptor, "bootstrap", before, "config", "account", map[string]string{"app": "worker"}, nil); err != nil {
				t.Fatal(err)
			}
			r.ConfigurationAdmissionBinding = b
			if err := r.mutateWorkerDeployment(nc, descriptor, "bootstrap", after, "config", "account", map[string]string{"app": "worker"}, nil); err != nil {
				t.Fatal(err)
			}
			changed := before.Spec.Template.Annotations["cisco.vk/controller-contract-hash"] != after.Spec.Template.Annotations["cisco.vk/controller-contract-hash"]
			found := false
			for _, env := range after.Spec.Template.Spec.Containers[0].Env {
				if env.Name == contract.RuntimeAdmissionEnvironment {
					found = true
				}
			}
			if changed != (kind == "catalyst-center") || found != (kind == "catalyst-center") {
				t.Fatalf("kind=%s hashChanged=%v env=%v", kind, changed, found)
			}
		})
	}
}

func TestControllerMetadataRuntimeTemplate(t *testing.T) {
	b := &contract.RuntimeAdmissionBinding{Protocol: contract.Protocol, ManagerUsername: "system:serviceaccount:system:manager", AdmissionPrefix: "cvk", PolicyNamespace: "system", PolicyName: "policy", LedgerName: "ledger", AppHostingServiceAccount: "app", NetworkManagementServiceAccount: "network", LeaseNamespace: "leases"}
	for _, kind := range []string{"catalyst-center", "nexus-dashboard"} {
		t.Run(kind, func(t *testing.T) {
			nc := validNetworkController("cc", kind)
			r, _ := newNetworkControllerReconciler(t, nc)
			r.ConfigurationAdmissionBinding = b
			descriptor := adapter.Descriptor{Type: kind}
			before, after := &apps.Deployment{}, &apps.Deployment{}
			if err := r.mutateWorkerDeployment(nc, descriptor, "bootstrap", before, "config", "account", map[string]string{"app": "worker"}, nil); err != nil {
				t.Fatal(err)
			}
			r.MetadataHandoffProtocol = cvk.NetworkControllerMetadataHandoffProtocol
			if err := r.mutateWorkerDeployment(nc, descriptor, "bootstrap", after, "config", "account", map[string]string{"app": "worker"}, nil); err != nil {
				t.Fatal(err)
			}
			changed := before.Spec.Template.Annotations["cisco.vk/controller-contract-hash"] != after.Spec.Template.Annotations["cisco.vk/controller-contract-hash"]
			found := 0
			for _, env := range after.Spec.Template.Spec.Containers[0].Env {
				if env.Name == managedprotocol.EnvControllerMetadataProtocol {
					found++
					if env.Value != cvk.NetworkControllerMetadataHandoffProtocol || env.ValueFrom != nil {
						t.Fatal("metadata protocol projection changed")
					}
				}
			}
			if changed != (kind == "catalyst-center") || (found == 1) != (kind == "catalyst-center") {
				t.Fatal("metadata protocol did not remain controller-specific")
			}
			if kind == "nexus-dashboard" && !reflect.DeepEqual(before.Spec.Template, after.Spec.Template) {
				t.Fatal("metadata mode changed ND template")
			}
		})
	}
	for _, mode := range []string{"missing-binding", "empty-namespace", "unknown-protocol"} {
		t.Run(mode, func(t *testing.T) {
			nc := validNetworkController("cc", "catalyst-center")
			r, _ := newNetworkControllerReconciler(t, nc)
			copy := *b
			r.ConfigurationAdmissionBinding = &copy
			r.MetadataHandoffProtocol = cvk.NetworkControllerMetadataHandoffProtocol
			switch mode {
			case "missing-binding":
				r.ConfigurationAdmissionBinding = nil
			case "empty-namespace":
				copy.LeaseNamespace = ""
			case "unknown-protocol":
				r.MetadataHandoffProtocol = "future"
			}
			if err := r.mutateWorkerDeployment(nc, adapter.Descriptor{Type: "catalyst-center"}, "bootstrap", &apps.Deployment{}, "config", "account", map[string]string{"app": "worker"}, nil); err == nil {
				t.Fatal("incomplete metadata installation projected")
			}
		})
	}
}
