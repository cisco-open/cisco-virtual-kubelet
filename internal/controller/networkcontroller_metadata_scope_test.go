// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	apps "k8s.io/api/apps/v1"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	admission "k8s.io/api/admissionregistration/v1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var registerMetadataScopeTest sync.Once
var metadataScopeFactoryCalls atomic.Int32

func ensureMetadataScopeTestRegistered() {
	registerMetadataScopeTest.Do(func() {
		controlleradapter.Register(controlleradapter.Registration{Descriptor: controlleradapter.Descriptor{Type: "metadata-scope-test", DisplayName: "Metadata scope test", NetAsCode: cvk.NetworkControllerNetAsCodeStatus{Format: "netascode-test", Stripe: "test", ModelVersions: []string{"0.5.0"}, Sections: []string{"inventory"}}, WorkerClusterRole: controlleradapter.DefaultWorkerClusterRole}, Factory: func(controlleradapter.Options) (controlleradapter.Adapter, error) {
			metadataScopeFactoryCalls.Add(1)
			return nil, errors.New("scope observation must not instantiate adapter")
		}, ConfigurationMetadataScope: &controlleradapter.ConfigurationMetadataScope{Cancellation: func(n *cvk.NetworkController) (*cvk.NetworkControllerConfigurationStatus, error) {
			if n.Status.Configuration == nil || n.Status.Configuration.MetadataHandoff == nil || n.Status.Configuration.Operation == nil || n.Status.Configuration.Operation.Phase != "Prepared" {
				return nil, errors.New("synthetic preparation unavailable")
			}
			result := n.Status.Configuration.DeepCopy()
			result.MetadataHandoff = nil
			return result, nil
		}, Settlement: func(nc *cvk.NetworkController) (*cvk.NetworkControllerConfigurationStatus, error) {
			if nc.Status.Configuration == nil || nc.Status.Configuration.MetadataHandoff == nil || (nc.Status.Configuration.Operation == nil && nc.Status.Configuration.MetadataHandoff.CancelledAt == nil) || (nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase != "Verified") {
				return nil, errors.New("synthetic outcome not terminal")
			}
			result := nc.Status.Configuration.DeepCopy()
			result.Operation = result.MetadataHandoff.Operation.DeepCopy()
			result.MetadataHandoff = nil
			result.ActionApplications = nil
			return result, nil
		}, Selected: func(nc *cvk.NetworkController) bool {
			return nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && strings.HasPrefix(nc.Status.Configuration.Operation.Resource, "test-remove:")
		}, PhysicalIdentities: func(_ *cvk.NetworkController, _ *config.NetworkControllerConfig, s controlleradapter.ConfigurationMetadataSource) ([]string, error) {
			if strings.Contains(string(s.Raw), "unsupported") {
				return nil, errors.New("unsupported model")
			}
			return []string{"first123", "second123"}, nil
		}}})
	})
}
func TestNetworkControllerMetadataScopeObservation(t *testing.T) {
	ensureMetadataScopeTestRegistered()
	for _, mode := range []string{"inline", "configmap", "wrong-configmap", "missing-key", "missing-binding", "unknown-namespace", "projection-error", "node-conflict", "reservation-conflict", "fence-conflict", "submitted", "foreign-owner", "missing-worker", "overlap-worker", "old-worker-template", "runtime-no-policy", "runtime-acquire", "runtime-metadata-only", "runtime-recover", "runtime-settle", "runtime-settle-no-source", "runtime-settle-disabled", "unselected", "other-adapter", "runtime-cancel-paused", "runtime-cancel-config-edited", "runtime-cancel-source-gone", "runtime-cancel-disabled", "runtime-cancel-unchanged"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nc := validNetworkController("cc", "metadata-scope-test")
			nc.UID = "controller"
			nc.Generation = 1
			op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "intent", ConfigUID: "intent", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Resource: "test-remove:native", Action: "Delete", Phase: "Prepared", TargetID: "native"}
			if mode == "submitted" {
				op.Phase = "Submitting"
			}
			if mode == "unselected" {
				op.Resource = "other-family"
			}
			if mode == "other-adapter" {
				nc.Spec.Type = "nexus-dashboard"
			}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: op, Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: "intent", ConfigUID: "intent"}}}
			if mode == "foreign-owner" {
				nc.Status.Configuration.Owners[0].ConfigUID = "other"
			}
			r, c := newNetworkControllerReconciler(t, nc)
			r.APIReader = c
			_ = ops.AddToScheme(c.Scheme())
			_ = coordination.AddToScheme(c.Scheme())
			_ = config.AddToScheme(c.Scheme())
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
				t.Fatal(err)
			}
			op = nc.Status.Configuration.Operation
			intent := &config.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: "intent", Namespace: nc.Namespace, UID: "intent", Generation: 1}, Spec: config.NetworkControllerConfigSpec{ControllerRef: config.NetworkControllerRef{Name: nc.Name}, ManagedSections: []string{"inventory"}, Mode: config.NetworkControllerApplyModeApply, ModelSource: config.NetworkControllerNetAsCodeModelSource{Format: "netascode-test", ModelVersion: "0.5.0", Resolved: true}, Source: config.NetworkControllerConfigurationSource{Inline: &runtime.RawExtension{Raw: []byte(`{"inventory":{"devices":[]}}`)}}}}
			if mode == "projection-error" {
				intent.Spec.Source.Inline.Raw = []byte(`{"unsupported":true}`)
			}
			if mode == "configmap" || mode == "wrong-configmap" || mode == "missing-key" {
				cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: nc.Namespace, UID: "model"}, Data: map[string]string{"model": `{"inventory":{"devices":[]}}`}}
				if mode == "missing-key" {
					cm.Data = nil
				}
				if err := c.Create(ctx, cm); err != nil {
					t.Fatal(err)
				}
				intent.Spec.Source = config.NetworkControllerConfigurationSource{ConfigMapRef: &config.ConfigMapKeyRef{Name: cm.Name, Key: "model"}}
				op.SourceUID = string(cm.UID)
				op.SourceVersion = cm.ResourceVersion
				if mode == "wrong-configmap" {
					op.SourceVersion = "stale"
				}
				if err := c.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.Create(ctx, intent); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
				t.Fatal(err)
			}
			r.ConfigurationAdmissionBinding = &contract.RuntimeAdmissionBinding{Protocol: contract.Protocol, ManagerUsername: "system:serviceaccount:system:manager", AdmissionPrefix: "cvk", PolicyNamespace: "system", PolicyName: "policy", LedgerName: "ledger", AppHostingServiceAccount: "app", NetworkManagementServiceAccount: "network", LeaseNamespace: "leases"}
			if mode == "missing-binding" {
				r.ConfigurationAdmissionBinding = nil
			}
			if mode == "unknown-namespace" {
				r.ConfigurationAdmissionBinding.LeaseNamespace = ""
			}
			if mode == "node-conflict" {
				if err := c.Create(ctx, &core.Node{ObjectMeta: metav1.ObjectMeta{Name: "orphan"}, Status: core.NodeStatus{NodeInfo: core.NodeSystemInfo{MachineID: "SECOND123"}}}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "reservation-conflict" {
				if err := c.Create(ctx, &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Name: "orphan", Namespace: "elsewhere"}, Spec: ops.ControllerConfigurationReservationSpec{PhysicalIdentity: "SECOND123"}}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "fence-conflict" {
				name, _ := contract.BootstrapFenceName("second123")
				if err := c.Create(ctx, &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "leases"}}); err != nil {
					t.Fatal(err)
				}
			}

			if mode == "runtime-no-policy" {
				r.AuthorizeMetadataHandoff = func(context.Context, *cvk.NetworkController, contract.MetadataScopeWorkerObservation, contract.AdmissionProtection) error {
					return nil
				}
			}
			installMetadataScopeWorker(t, c, nc, mode)
			active := mode == "runtime-acquire" || mode == "runtime-metadata-only" || mode == "runtime-recover" || strings.HasPrefix(mode, "runtime-settle") || strings.HasPrefix(mode, "runtime-cancel")
			var acquisition *metadataRouteStore
			if active {
				installMetadataRoutePolicies(t, ctx, c, r.ConfigurationAdmissionBinding)
				r.AuthorizeMetadataHandoff = func(context.Context, *cvk.NetworkController, contract.MetadataScopeWorkerObservation, contract.AdmissionProtection) error {
					return nil
				}
				acquisition = &metadataRouteStore{Client: c, loseFirstCreate: mode == "runtime-recover"}
				r.Client = acquisition
			}
			before := nc.DeepCopy()
			var handled bool
			var err error
			if mode == "runtime-metadata-only" {
				r.MetadataHandoffProtocol = cvk.NetworkControllerMetadataHandoffProtocol
				r.ManageConfigurationReservations = false
				r.AuthorizeConfigurationReservation = nil
				handled, err = r.reconcileConfigurationReservationCreation(ctx, nc)
			} else {
				handled, err = r.reconcileConfigurationMetadataScope(ctx, nc)
			}
			if mode == "runtime-recover" {
				if err == nil || acquisition.creates != 1 {
					t.Fatal("manager did not surface lost Create response", err)
				}
				var fresh cvk.NetworkController
				if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &fresh); err != nil {
					t.Fatal(err)
				}
				handled, err = r.reconcileConfigurationMetadataScope(ctx, &fresh)
			}
			skipped := mode == "unselected" || mode == "other-adapter"
			good := mode == "inline" || mode == "configmap" || skipped || active
			if handled == skipped || (err == nil) != good {
				t.Fatal("wrong manager observation", handled, err)
			}
			var after cvk.NetworkController
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, nc) {
				t.Fatal("manager changed caller snapshot")
			}
			if active {
				if after.Status.Configuration.MetadataHandoff == nil || len(after.Status.Configuration.MetadataHandoff.Fences) != 2 || acquisition.creates != 2 || !reflect.DeepEqual(before.Status.Configuration.Operation, after.Status.Configuration.Operation) || !reflect.DeepEqual(before.Status.Configuration.ActionApplications, after.Status.Configuration.ActionApplications) {
					t.Fatal("manager route failed to retain exact custody without dispatch")
				}
				if _, err := r.reconcileConfigurationMetadataScope(ctx, &after); err != nil || acquisition.creates != 2 {
					t.Fatal("manager route replayed completed acquisition", err)
				}
				if strings.HasPrefix(mode, "runtime-cancel") {
					switch mode {
					case "runtime-cancel-paused":
						after.Spec.Paused = true
						after.Generation++
						if err := c.Update(ctx, &after); err != nil {
							t.Fatal(err)
						}
					case "runtime-cancel-config-edited", "runtime-cancel-disabled":
						intent.Generation++
						if err := c.Update(ctx, intent); err != nil {
							t.Fatal(err)
						}
					case "runtime-cancel-source-gone":
						if err := c.Delete(ctx, intent); err != nil {
							t.Fatal(err)
						}
					}
					if mode != "runtime-cancel-disabled" {
						r.AuthorizeMetadataSettlement = func(context.Context, *cvk.NetworkController) error { return nil }
					}
					pending, err := r.reconcileConfigurationReservationRetirement(ctx, &after)
					if mode == "runtime-cancel-disabled" {
						if err == nil || !pending || after.Status.Configuration.MetadataHandoff.CancelledAt != nil {
							t.Fatal("disabled cancellation changed custody", err)
						}
					} else if mode == "runtime-cancel-unchanged" {
						if err != nil || !pending || after.Status.Configuration.MetadataHandoff.CancelledAt != nil || after.Status.Configuration.Operation == nil {
							t.Fatal("unchanged intent cancelled", err)
						}
					} else {
						if err != nil || !pending || after.Status.Configuration.Operation != nil || after.Status.Configuration.MetadataHandoff.CancelledAt == nil || after.Status.Configuration.MetadataHandoff.ReleasedAt != nil {
							t.Fatal("manager cancellation failed or released fences early", err)
						}
						pending, err = r.reconcileConfigurationReservationRetirement(ctx, &after)
						if err != nil || !pending || after.Status.Configuration.MetadataHandoff.ReleasedAt == nil {
							t.Fatal("cancelled custody did not settle", err)
						}
						pending, err = r.reconcileConfigurationReservationRetirement(ctx, &after)
						if err != nil || pending || after.Status.Configuration.MetadataHandoff != nil || after.Status.Configuration.Operation != nil {
							t.Fatal("cancelled custody did not retire", err)
						}
					}
				}
				if strings.HasPrefix(mode, "runtime-settle") {
					at := metav1.Now()
					after.Status.Configuration.Operation.Phase = "Verified"
					after.Status.Configuration.Operation.SubmittedAt = &at
					if err := c.Status().Update(ctx, &after); err != nil {
						t.Fatal(err)
					}
					if mode == "runtime-settle-no-source" {
						if err := c.Delete(ctx, intent); err != nil {
							t.Fatal(err)
						}
					}
					if mode != "runtime-settle-disabled" {
						r.AuthorizeMetadataSettlement = func(context.Context, *cvk.NetworkController) error { return nil }
					}
					pending, err := r.reconcileConfigurationReservationRetirement(ctx, &after)
					if mode == "runtime-settle-disabled" {
						if err == nil || !pending || after.Status.Configuration.MetadataHandoff.ReleasedAt != nil {
							t.Fatal("disabled settlement escaped custody", err)
						}
					} else {
						if err != nil || !pending || after.Status.Configuration.MetadataHandoff.ReleasedAt == nil {
							t.Fatal("manager terminal settlement failed", err)
						}
						pending, err = r.reconcileConfigurationReservationRetirement(ctx, &after)
						if err != nil || pending || after.Status.Configuration.MetadataHandoff != nil || after.Status.Configuration.Operation.Phase != "Verified" {
							t.Fatal("manager retirement lost verified slot", err)
						}
					}
				}

			} else if !reflect.DeepEqual(before.Status.Configuration, after.Status.Configuration) {
				t.Fatal("manager observer changed preparation")
			}
			if metadataScopeFactoryCalls.Load() != 0 {
				t.Fatal("manager constructed product adapter")
			}
		})
	}
}

func installMetadataScopeWorker(t *testing.T, c client.Client, nc *cvk.NetworkController, mode string) {
	t.Helper()
	ctx := context.Background()
	if mode == "missing-worker" {
		return
	}
	replicas, controlled := int32(1), true
	template := core.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"scope-worker": "connector"}, Annotations: map[string]string{"cisco.vk/controller-contract-hash": strings.Repeat("a", 64)}}, Spec: core.PodSpec{ServiceAccountName: "connector", Containers: []core.Container{{Name: "controller-worker", Image: "example.invalid/worker:test", Args: []string{"controller-worker", "--controller-namespace=" + nc.Namespace, "--controller-name=" + nc.Name, "--controller-uid=" + string(nc.UID), "--controller-generation=1", "--controller-type=" + string(nc.Spec.Type)}}}}}
	deployment := &apps.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "connector", Namespace: nc.Namespace, UID: "deployment", Generation: 1, Annotations: map[string]string{"cisco.vk/network-controller-name": nc.Name, "cisco.vk/network-controller-uid": string(nc.UID)}}, Spec: apps.DeploymentSpec{Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: template.Labels}, Strategy: apps.DeploymentStrategy{Type: apps.RecreateDeploymentStrategyType}, Template: template}}
	if err := c.Create(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	deployment.Status = apps.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1}
	if err := c.Status().Update(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	rs := &apps.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "replica", Namespace: nc.Namespace, UID: "replica", OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: deployment.Name, UID: deployment.UID, Controller: &controlled}}}, Spec: apps.ReplicaSetSpec{Template: *template.DeepCopy()}}
	if err := c.Create(ctx, rs); err != nil {
		t.Fatal(err)
	}
	pod := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: nc.Namespace, UID: "worker", Labels: template.Labels, Annotations: template.Annotations, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: rs.Name, UID: rs.UID, Controller: &controlled}}}, Spec: *template.Spec.DeepCopy(), Status: core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().Add(-time.Second))}}}}
	if mode == "old-worker-template" {
		pod.Annotations = map[string]string{"cisco.vk/controller-contract-hash": strings.Repeat("b", 64)}
	}
	if err := c.Create(ctx, pod); err != nil {
		t.Fatal(err)
	}
	if mode == "overlap-worker" {
		other := pod.DeepCopy()
		other.Name = "overlap"
		other.UID = "overlap"
		other.ResourceVersion = ""
		if err := c.Create(ctx, other); err != nil {
			t.Fatal(err)
		}
	}
	nc.Status.Worker = &cvk.NetworkControllerWorkerStatus{Name: "connector", DeploymentName: "connector", ReadyReplicas: 1}
	if err := c.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
}

// This fixture uses typed fake storage and rendered policy contracts. Actual
// admission/server UIDs are qualified separately in controllerconfiguration.
type metadataRouteStore struct {
	client.Client
	creates         int
	loseFirstCreate bool
}

func (s *metadataRouteStore) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if lease, ok := obj.(*coordination.Lease); ok {
		s.creates++
		lease.UID = types.UID(fmt.Sprintf("route-fence-%d", s.creates))
	}
	if err := s.Client.Create(ctx, obj, opts...); err != nil {
		return err
	}
	if s.loseFirstCreate && s.creates == 1 {
		return errors.New("route Create acknowledgement lost")
	}
	return nil
}
func installMetadataRoutePolicies(t *testing.T, ctx context.Context, c client.Client, binding *contract.RuntimeAdmissionBinding) {
	t.Helper()
	_ = admission.AddToScheme(c.Scheme())
	binding.AdmissionPrefix = "cvk-cisco-virtual-kubelet"
	binding.ManagerUsername = "system:serviceaccount:system:cisco-virtual-kubelet-controller"
	raw, err := exec.CommandContext(ctx, "helm", "template", "cvk", "../../charts/cisco-virtual-kubelet", "--namespace", "system", "--kube-version", "1.35.0", "--set", "topology.enabled=true", "--set", "controller.leaderElect=true", "--set", "rbac.profile=strict", "--set", "config.leaseNamespace=leases", "--show-only", "templates/bootstrap-fence-admission.yaml", "--show-only", "templates/metadata-handoff-admission.yaml").CombinedOutput()
	if err != nil {
		t.Fatalf("render metadata route contracts: %v %s", err, raw)
	}
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	for {
		var object map[string]any
		if err := decoder.Decode(&object); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(object)
		switch object["kind"] {
		case "ValidatingAdmissionPolicy":
			var p admission.ValidatingAdmissionPolicy
			if err := json.Unmarshal(encoded, &p); err != nil {
				t.Fatal(err)
			}
			p.UID = types.UID(p.Name)
			p.Generation = 1
			p.Status = admission.ValidatingAdmissionPolicyStatus{ObservedGeneration: 1, TypeChecking: &admission.TypeChecking{}}
			if err := c.Create(ctx, &p); err != nil {
				t.Fatal(err)
			}
		case "ValidatingAdmissionPolicyBinding":
			var b admission.ValidatingAdmissionPolicyBinding
			if err := json.Unmarshal(encoded, &b); err != nil {
				t.Fatal(err)
			}
			b.UID = types.UID(b.Name)
			b.Generation = 1
			if err := c.Create(ctx, &b); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := c.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "leases", UID: "lease-namespace"}, Status: core.NamespaceStatus{Phase: core.NamespaceActive}}); err != nil {
		t.Fatal(err)
	}
}
