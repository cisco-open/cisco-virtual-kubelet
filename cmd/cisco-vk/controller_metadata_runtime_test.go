// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
	admission "k8s.io/api/admissionregistration/v1"
	apps "k8s.io/api/apps/v1"
	auth "k8s.io/api/authentication/v1"
	core "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	fakekube "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type metadataParticipantTest struct {
	calls        int
	installation contract.BootstrapFenceInstallation
	policy       string
}

func (p *metadataParticipantTest) ConfigureMetadataHandoff(i contract.BootstrapFenceInstallation, policy string, _ contract.MetadataWorkerAuthorization) error {
	p.calls++
	p.installation = i
	p.policy = policy
	return nil
}
func TestControllerMetadataProtocolOptIn(t *testing.T) {
	b := &contract.RuntimeAdmissionBinding{Protocol: contract.Protocol, ManagerUsername: "system:serviceaccount:system:manager", AdmissionPrefix: "cvk", PolicyNamespace: "system", PolicyName: "policy", LedgerName: "ledger", AppHostingServiceAccount: "apps", NetworkManagementServiceAccount: "network", LeaseNamespace: "leases"}
	authorize := func(context.Context, *cvk.NetworkController) error { return nil }
	if err := configureControllerMetadataProtocol(nil, "", nil, nil); err != nil {
		t.Fatal("disabled protocol changed default adapter setup", err)
	}
	for _, mode := range []string{"enabled", "unknown", "no-binding", "no-namespace", "no-callback", "other-adapter"} {
		t.Run(mode, func(t *testing.T) {
			participant := &metadataParticipantTest{}
			var target any = participant
			copy := *b
			binding := &copy
			protocol := cvk.NetworkControllerMetadataHandoffProtocol
			check := contract.MetadataWorkerAuthorization(authorize)
			switch mode {
			case "unknown":
				protocol = "future"
			case "no-binding":
				binding = nil
			case "no-namespace":
				copy.LeaseNamespace = ""
			case "no-callback":
				check = nil
			case "other-adapter":
				target = struct{}{}
			}
			err := configureControllerMetadataProtocol(target, protocol, binding, check)
			if (err == nil) != (mode == "enabled") || (participant.calls == 1) != (mode == "enabled") {
				t.Fatal("invalid metadata protocol selected a partial runtime", err)
			}
			if mode == "enabled" && (participant.installation.ManagerUsername != b.ManagerUsername || participant.installation.LeaseNamespace != "leases" || participant.policy != "cvk-metadata-handoff") {
				t.Fatal("metadata runtime did not reuse installation identities")
			}
		})
	}
	for _, enabled := range []bool{false, true} {
		for _, managed := range []bool{false, true} {
			for _, leader := range []bool{false, true} {
				for _, aggregated := range []bool{false, true} {
					for _, namespace := range []string{"", "leases"} {
						err := validateControllerMetadataOptions(enabled, managed, leader, aggregated, namespace)
						if (err == nil) != (!enabled || managed && leader && !aggregated && namespace != "") {
							t.Fatal("metadata installation prerequisites changed")
						}
					}
				}
			}
		}
	}
}

// Typed storage and caller/policy compiler observations are staged here. The
// real API/schema/admission and installed runtime are separate qualification.
func metadataRuntimeAuthorizationFixture(t *testing.T, rendered []byte) (controllerMetadataRuntime, *fakekube.Clientset, client.Client, *cvk.NetworkController, func(bool)) {
	t.Helper()
	ctx := context.Background()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{core.AddToScheme, cvk.AddToScheme, admission.AddToScheme, rbac.AddToScheme, apps.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	var objects []client.Object
	var apiObjects []runtime.Object
	var policy *core.ConfigMap
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(rendered), 4096)
	for {
		var doc map[string]any
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(doc)
		switch doc["kind"] {
		case "ValidatingAdmissionPolicy":
			var p admission.ValidatingAdmissionPolicy
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			p.UID = types.UID(p.Name)
			p.Generation = 1
			p.ResourceVersion = "1"
			p.Status = admission.ValidatingAdmissionPolicyStatus{ObservedGeneration: 1, TypeChecking: &admission.TypeChecking{}}
			objects = append(objects, &p)
			apiObjects = append(apiObjects, &p)
		case "ValidatingAdmissionPolicyBinding":
			var p admission.ValidatingAdmissionPolicyBinding
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			p.UID = types.UID(p.Name)
			p.Generation = 1
			p.ResourceVersion = "1"
			objects = append(objects, &p)
			apiObjects = append(apiObjects, &p)
		case "ConfigMap":
			var p core.ConfigMap
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			if p.Data[topologyrollout.PolicyDataKey] != "" {
				p.UID = "policy"
				policy = &p
				objects = append(objects, &p)
			}
		}
	}
	if policy == nil {
		t.Fatal("rendered administrator policy missing")
	}
	for name, rules := range managedprotocol.WorkerClusterRoleContracts() {
		objects = append(objects, &rbac.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name)}, Rules: rules})
	}
	objects = append(objects, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "leases", UID: "leases"}, Status: core.NamespaceStatus{Phase: core.NamespaceActive}})
	store := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	preflight, err := topologyrollout.ReadAdminPolicyPreflight(ctx, store, client.ObjectKeyFromObject(policy))
	if err != nil {
		t.Fatal(err)
	}
	b := contract.RuntimeAdmissionBinding{Protocol: contract.Protocol, ManagerUsername: "system:serviceaccount:system:cisco-virtual-kubelet-controller", AdmissionPrefix: preflight.AdmissionPrefix, PolicyNamespace: policy.Namespace, PolicyName: policy.Name, LedgerName: preflight.Config.LedgerName, AppHostingServiceAccount: preflight.Config.AppHostingServiceAccountName, NetworkManagementServiceAccount: preflight.Config.NetworkManagementServiceAccountName, LeaseNamespace: "leases"}
	encoded, err := b.Encode()
	if err != nil {
		t.Fatal(err)
	}
	now := metav1.NewTime(time.Now().Add(-time.Minute))
	status := func(name string) core.PodStatus {
		return core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue, LastTransitionTime: now}}, ContainerStatuses: []core.ContainerStatus{{Name: name, ImageID: "containerd://sha256:qualified-image", Ready: true, State: core.ContainerState{Running: &core.ContainerStateRunning{StartedAt: now}}}}}
	}
	manager := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "manager", Namespace: "system", UID: "manager", Labels: map[string]string{"app.kubernetes.io/component": "controller"}}, Spec: core.PodSpec{ServiceAccountName: "cisco-virtual-kubelet-controller", Containers: []core.Container{{Name: "controller", Command: []string{"cisco-vk", "manager", "--leader-elect", "--enable-managed-topology", "--" + managedprotocol.ControllerMetadataManagerFlag, "--topology-policy-namespace=" + b.PolicyNamespace, "--topology-policy-name=" + b.PolicyName, "--app-hosting-service-account=" + b.AppHostingServiceAccount, "--network-management-service-account=" + b.NetworkManagementServiceAccount}, Env: []core.EnvVar{{Name: "CONFIG_LEASE_NAMESPACE", Value: "leases"}}}}}, Status: status("controller")}
	descriptor, ok := controlleradapter.DescriptorFor("catalyst-center")
	if !ok {
		t.Fatal("CC descriptor missing")
	}
	worker := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "connector", Namespace: "system", UID: "connector"}, Spec: core.PodSpec{ServiceAccountName: "connector", Containers: []core.Container{{Name: "controller-worker", Args: []string{"controller-worker", "--controller-descriptor-digest=" + controlleradapter.DescriptorDigest(descriptor)}, Env: []core.EnvVar{{Name: contract.RuntimeAdmissionEnvironment, Value: encoded}, {Name: managedprotocol.EnvControllerMetadataProtocol, Value: cvk.NetworkControllerMetadataHandoffProtocol}}}}}, Status: status("controller-worker")}
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "catc", Namespace: "system", UID: "catc", Generation: 1}, Spec: cvk.NetworkControllerSpec{Type: "catalyst-center"}, Status: cvk.NetworkControllerStatus{Configuration: &cvk.NetworkControllerConfigurationStatus{MetadataHandoff: &cvk.NetworkControllerConfigurationMetadataHandoff{WorkerPod: ops.ConfigurationReservationReference{Name: worker.Name, UID: string(worker.UID)}, WorkerUsername: "system:serviceaccount:system:connector"}}}}
	worker.Spec.Containers[0].Args = append(worker.Spec.Containers[0].Args, "--controller-namespace=system", "--controller-name=catc", "--controller-uid=catc", "--controller-generation=1", "--controller-type=catalyst-center")
	labels := map[string]string{"metadata-worker": "connector"}
	annotations := map[string]string{"cisco.vk/controller-contract-hash": strings.Repeat("a", 64)}
	worker.Labels = labels
	worker.Annotations = annotations
	one, controlled := int32(1), true
	template := core.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels, Annotations: annotations}, Spec: *worker.Spec.DeepCopy()}
	dep := &apps.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "connector-deployment", Namespace: nc.Namespace, UID: "connector-deployment", Generation: 1, Annotations: map[string]string{"cisco.vk/network-controller-name": nc.Name, "cisco.vk/network-controller-uid": string(nc.UID)}}, Spec: apps.DeploymentSpec{Replicas: &one, Selector: &metav1.LabelSelector{MatchLabels: labels}, Strategy: apps.DeploymentStrategy{Type: apps.RecreateDeploymentStrategyType}, Template: template}, Status: apps.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1}}
	rs := &apps.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "connector-replica", Namespace: nc.Namespace, UID: "connector-replica", OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: dep.Name, UID: dep.UID, Controller: &controlled}}}, Spec: apps.ReplicaSetSpec{Template: template, Selector: dep.Spec.Selector.DeepCopy()}}
	worker.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: rs.Name, UID: rs.UID, Controller: &controlled}}
	nc.Status.Worker = &cvk.NetworkControllerWorkerStatus{Name: dep.Name, DeploymentName: dep.Name}
	for _, o := range []client.Object{manager, dep, rs, worker, nc} {
		if err := store.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	api := fakekube.NewSimpleClientset(apiObjects...)
	asWorker := false
	api.PrependReactor("create", "selfsubjectreviews", func(ktesting.Action) (bool, runtime.Object, error) {
		username, name, uid := b.ManagerUsername, manager.Name, string(manager.UID)
		if asWorker {
			username, name, uid = "system:serviceaccount:"+worker.Namespace+":"+worker.Spec.ServiceAccountName, worker.Name, string(worker.UID)
		}
		return true, &auth.SelfSubjectReview{Status: auth.SelfSubjectReviewStatus{UserInfo: auth.UserInfo{Username: username, Extra: map[string]auth.ExtraValue{boundTokenPodNameClaim: {name}, boundTokenPodUIDClaim: {uid}}}}}, nil
	})
	r := controllerMetadataRuntime{api: api, reader: store, binding: b, podName: manager.Name, podUID: string(manager.UID)}
	return r, api, store, nc, func(enabled bool) { asWorker = enabled }
}
func TestControllerMetadataRuntimeAuthorization(t *testing.T) {
	raw, err := exec.Command("helm", "template", "cvk", "../../charts/cisco-virtual-kubelet", "--namespace", "system", "--kube-version", "1.35.0", "--set", "topology.enabled=true", "--set", "controller.leaderElect=true", "--set", "rbac.profile=strict", "--set", "config.leaseNamespace=leases", "--set", "topology.controllerMetadataHandoff=true").CombinedOutput()
	if err != nil {
		t.Fatalf("metadata chart: %v %s", err, raw)
	}
	for _, mode := range []string{"handoff", "worker", "planning", "planning-disabled", "settlement-no-worker", "disabled-new-work", "disabled-settlement", "old-image", "old-worker-env", "old-descriptor", "wrong-caller", "old-roles", "broadened-roles"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			r, api, store, nc, caller := metadataRuntimeAuthorizationFixture(t, raw)
			var manager, worker core.Pod
			if err := store.Get(ctx, client.ObjectKey{Namespace: "system", Name: "manager"}, &manager); err != nil {
				t.Fatal(err)
			}
			if err := store.Get(ctx, client.ObjectKey{Namespace: "system", Name: "connector"}, &worker); err != nil {
				t.Fatal(err)
			}
			asWorker := mode == "worker" || mode == "planning" || mode == "planning-disabled" || mode == "old-image" || mode == "old-worker-env" || mode == "old-descriptor" || mode == "wrong-caller"
			if asWorker {
				r.podName = worker.Name
				r.podUID = string(worker.UID)
				caller(true)
			}
			if mode == "planning" || mode == "planning-disabled" {
				nc.Status.Configuration.MetadataHandoff = nil
				if err := store.Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				if err := store.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "settlement-no-worker":
				if err := store.Delete(ctx, &worker); err != nil {
					t.Fatal(err)
				}
			case "disabled-new-work", "disabled-settlement", "planning-disabled":
				manager.Spec.Containers[0].Command = append(manager.Spec.Containers[0].Command[:4], manager.Spec.Containers[0].Command[5:]...)
				if err := store.Update(ctx, &manager); err != nil {
					t.Fatal(err)
				}
			case "old-image":
				worker.Status.ContainerStatuses[0].ImageID = "old-image"
				if err := store.Status().Update(ctx, &worker); err != nil {
					t.Fatal(err)
				}
			case "old-worker-env":
				worker.Spec.Containers[0].Env = worker.Spec.Containers[0].Env[:1]
				if err := store.Update(ctx, &worker); err != nil {
					t.Fatal(err)
				}
			case "old-descriptor":
				worker.Spec.Containers[0].Args = []string{"controller-worker", "--controller-descriptor-digest=old"}
				if err := store.Update(ctx, &worker); err != nil {
					t.Fatal(err)
				}
			case "wrong-caller":
				caller(false)
			case "old-roles", "broadened-roles":
				var role rbac.ClusterRole
				if err := store.Get(ctx, client.ObjectKey{Name: managedprotocol.NetworkManagementGlobalReadClusterRole}, &role); err != nil {
					t.Fatal(err)
				}
				if mode == "broadened-roles" {
					role.Rules = append(role.Rules, rbac.PolicyRule{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get"}})
				} else {
					rules := role.Rules[:0]
					for _, rule := range role.Rules {
						namespace := false
						for _, resource := range rule.Resources {
							if resource == "namespaces" {
								namespace = true
							}
						}
						if !namespace {
							rules = append(rules, rule)
						}
					}
					role.Rules = rules
				}
				if err := store.Update(ctx, &role); err != nil {
					t.Fatal(err)
				}
			}
			var result error
			if mode == "settlement-no-worker" || mode == "disabled-settlement" {
				result = r.settlement(ctx, nc)
			} else if asWorker {
				result = r.worker(ctx, nc)
			} else {
				result = r.handoff(ctx, nc, contract.MetadataScopeWorkerObservation{Controller: ops.ConfigurationReservationReference{Name: nc.Name, UID: string(nc.UID)}, Pod: nc.Status.Configuration.MetadataHandoff.WorkerPod, PhysicalIdentities: []string{"selected123"}}, contract.AdmissionProtection{})
			}
			allowed := mode == "handoff" || mode == "worker" || mode == "planning" || mode == "settlement-no-worker" || mode == "disabled-settlement"
			if (result == nil) != allowed {
				t.Fatal("unexpected composed metadata authority", result)
			}
			for _, action := range api.Actions() {
				if action.GetVerb() != "get" && action.GetVerb() != "list" && !(action.GetVerb() == "create" && action.GetResource().Resource == "selfsubjectreviews") {
					t.Fatal("runtime authorization mutated Kubernetes state")
				}
			}
			if mode == "wrong-caller" {
				for _, action := range api.Actions() {
					if !strings.Contains(action.GetResource().Resource, "selfsubjectreviews") {
						t.Fatal("wrong bound caller reached installation observations")
					}
				}
			}
		})
	}
}
