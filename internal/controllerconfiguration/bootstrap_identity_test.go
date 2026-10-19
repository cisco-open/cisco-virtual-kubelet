// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"encoding/json"
	"errors"
	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"
	"testing"
)

func bootstrapIdentityFixture(t *testing.T) (client.Client, *cvk.NetworkController, *config.NetworkControllerConfig) {
	t.Helper()
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "catc", Namespace: "lab", UID: "controller", Generation: 1}, Spec: cvk.NetworkControllerSpec{Type: "catalyst-center"}, Status: cvk.NetworkControllerStatus{Configuration: &cvk.NetworkControllerConfigurationStatus{
		Operation: &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "pnp", ConfigUID: "intent", ConfigGeneration: 2, ControllerGeneration: 1, Section: "inventory", Resource: "pnp-claim:v2:" + strings.Repeat("b", 64) + ":" + strings.Repeat("c", 64), TargetID: "native-pnp", TargetSerial: "bootstrap123", Action: "Update", Phase: "Prepared"},
		Owners:    []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: "pnp", ConfigUID: "intent"}},
	}}}
	intent := &config.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: "pnp", Namespace: "lab", UID: "intent", Generation: 2}, Spec: config.NetworkControllerConfigSpec{ControllerRef: config.NetworkControllerRef{Name: nc.Name}, ManagedSections: []string{"inventory"}, Mode: config.NetworkControllerApplyModeApply, ModelSource: config.NetworkControllerNetAsCodeModelSource{Format: "netascode-catalyst-center", ModelVersion: "0.5.0", Resolved: true}, Source: config.NetworkControllerConfigurationSource{Inline: &runtime.RawExtension{Raw: []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"edge","state":"PNP","serial_number":"BOOTSTRAP123","pid":"C9300-48P","site":"Global/Lab/Building","onboarding_template":{"name":"bootstrap","variables":[{"name":"password","value":"DO-NOT-PRINT"}]}}]}}}`)}}}}
	scheme := runtime.NewScheme()
	_ = cvk.AddToScheme(scheme)
	_ = config.AddToScheme(scheme)
	_ = core.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(nc).WithObjects(nc, intent).Build()
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	return c, nc, intent
}

type bootstrapRaceReader struct {
	client.Reader
	mode                                                                  string
	deviceLists, nodeLists, controllerLists, configReads, controllerReads int
}

func (r *bootstrapRaceReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	switch o := obj.(type) {
	case *config.NetworkControllerConfig:
		r.configReads++
		if r.mode == "late-source" && r.configReads >= 3 {
			o.Generation++
		}
	case *cvk.NetworkController:
		r.controllerReads++
		if r.mode == "late-controller" && r.controllerReads >= 3 {
			o.ResourceVersion = "changed"
		}
	case *core.ConfigMap:
		if r.mode == "late-configmap" && r.deviceLists > 0 {
			o.ResourceVersion = "changed"
		}
	}
	return nil
}
func (r *bootstrapRaceReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	options := &client.ListOptions{}
	for _, opt := range opts {
		opt.ApplyToList(options)
	}
	if options.Namespace != "" || options.LabelSelector != nil || options.FieldSelector != nil {
		return errors.New("bootstrap identity must observe all cluster declarations")
	}
	if r.mode == "list-denied" {
		return errors.New("global list forbidden")
	}
	if err := r.Reader.List(ctx, list, opts...); err != nil {
		return err
	}
	switch l := list.(type) {
	case *cvk.CiscoDeviceList:
		r.deviceLists++
		if r.mode == "late-device" && r.deviceLists == 2 {
			l.Items = append(l.Items, cvk.CiscoDevice{Spec: cvk.DeviceSpec{PhysicalIdentity: "BOOTSTRAP123"}})
		}
	case *core.NodeList:
		r.nodeLists++
		if r.mode == "late-node" && r.nodeLists == 2 {
			l.Items = append(l.Items, core.Node{Status: core.NodeStatus{NodeInfo: core.NodeSystemInfo{MachineID: "BOOTSTRAP123"}}})
		}
	case *cvk.NetworkControllerList:
		r.controllerLists++
		if r.mode == "late-other-operation" && r.controllerLists == 2 {
			l.Items = append(l.Items, cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "other", UID: "other"}, Status: cvk.NetworkControllerStatus{Configuration: &cvk.NetworkControllerConfigurationStatus{Operation: &cvk.NetworkControllerConfigurationOperation{TargetSerial: "BOOTSTRAP123"}}}})
		}
	}
	return nil
}

func TestBootstrapIdentityObservation(t *testing.T) {
	for _, mode := range []string{"valid", "unrelated", "configmap", "device", "foreign-device", "retained-node-identity", "node-machine", "node-uuid", "other-operation", "other-group", "late-device", "late-node", "late-other-operation", "list-denied", "late-source", "late-controller", "late-configmap", "report", "foreign-owner", "duplicate-owner", "changed-generation", "submitted", "noncanonical", "changed-controller"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, intent := bootstrapIdentityFixture(t)
			switch mode {
			case "device", "foreign-device", "retained-node-identity", "unrelated":
				d := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "device", Namespace: "lab", UID: "device"}, Spec: cvk.DeviceSpec{PhysicalIdentity: "BOOTSTRAP123"}}
				if mode == "foreign-device" {
					d.Namespace = "another-tenant"
				}
				if mode == "retained-node-identity" {
					d.Spec.PhysicalIdentity = "different"
					d.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{PhysicalIdentity: "bootstrap123"}
				}
				if mode == "unrelated" {
					d.Spec.PhysicalIdentity = "unrelated"
				}
				if err := c.Create(ctx, d); err != nil {
					t.Fatal(err)
				}
			case "node-machine", "node-uuid":
				n := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: "orphan", UID: "node"}}
				if mode == "node-machine" {
					n.Status.NodeInfo.MachineID = "BOOTSTRAP123"
				} else {
					n.Status.NodeInfo.SystemUUID = "bootstrap123"
				}
				if err := c.Create(ctx, n); err != nil {
					t.Fatal(err)
				}
			case "other-operation", "other-group":
				other := nc.DeepCopy()
				other.Name = "other"
				other.Namespace = "elsewhere"
				other.UID = "other-controller"
				other.ResourceVersion = ""
				other.Status.Configuration.Operation.TargetSerial = "BOOTSTRAP123"
				if mode == "other-group" {
					other.Status.Configuration.Operation.TargetSerial = ""
					other.Status.Configuration.Operation.PhysicalGroup = &cvk.NetworkControllerConfigurationGroupIdentity{Targets: []cvk.NetworkControllerConfigurationGroupTarget{{PhysicalIdentity: "BOOTSTRAP123"}}}
				}
				if err := c.Create(ctx, other); err != nil {
					t.Fatal(err)
				}
			case "report":
				intent.Spec.Mode = config.NetworkControllerApplyModeReport
			case "foreign-owner":
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			case "duplicate-owner":
				nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, nc.Status.Configuration.Owners[0])
			case "changed-generation":
				intent.Generation++
			case "submitted":
				nc.Status.Configuration.Operation.Phase = "Submitting"
			case "noncanonical":
				nc.Status.Configuration.Operation.TargetSerial = "BOOTSTRAP123"
			}
			if mode == "configmap" || mode == "late-configmap" {
				cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "source", Namespace: "lab", UID: "source"}, Data: map[string]string{"model": "private source"}}
				if err := c.Create(ctx, cm); err != nil {
					t.Fatal(err)
				}
				intent.Spec.Source = config.NetworkControllerConfigurationSource{ConfigMapRef: &config.ConfigMapKeyRef{Name: cm.Name, Key: "model"}}
				nc.Status.Configuration.Operation.SourceUID = string(cm.UID)
				nc.Status.Configuration.Operation.SourceVersion = cm.ResourceVersion
			}
			if err := c.Update(ctx, intent); err != nil {
				t.Fatal(err)
			}
			if err := c.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if mode == "changed-controller" {
				nc = nc.DeepCopy()
				nc.UID = "stale"
			}
			reader := &bootstrapRaceReader{Reader: c, mode: mode}
			result, err := ReadBootstrapIdentity(ctx, reader, nc)
			valid := mode == "valid" || mode == "unrelated" || mode == "configmap"
			if (err == nil) != valid {
				t.Fatal("bootstrap observation outcome", err)
			}
			if !valid {
				if result != nil {
					t.Fatal("unproven identity returned")
				}
				return
			}
			if result.PhysicalIdentity != "bootstrap123" || result.NativeTargetID != "native-pnp" || result.Controller.UID != "controller" || result.Config.UID != "intent" || len(result.OperationDigest) != 64 || reader.deviceLists != 2 || reader.nodeLists != 2 || reader.controllerLists != 2 {
				t.Fatal("incomplete or scoped observation", result)
			}
			raw, _ := json.Marshal(result)
			if strings.Contains(string(raw), "DO-NOT-PRINT") || strings.Contains(string(raw), "private source") {
				t.Fatal("source contents exposed")
			}
		})
	}
}
