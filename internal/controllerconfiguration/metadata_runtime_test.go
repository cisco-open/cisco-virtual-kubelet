// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func metadataRuntimeTestBinding() RuntimeAdmissionBinding {
	return RuntimeAdmissionBinding{Protocol: Protocol, ManagerUsername: "system:serviceaccount:system:manager", AdmissionPrefix: "cvk", PolicyNamespace: "system", PolicyName: "policy", LedgerName: "ledger", AppHostingServiceAccount: "apps", NetworkManagementServiceAccount: "network", LeaseNamespace: "leases"}
}
func metadataRuntimeTestManager() *core.Pod {
	now := metav1.NewTime(time.Now().Add(-time.Minute))
	return &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "manager", Namespace: "system", UID: "manager-pod", Labels: map[string]string{"app.kubernetes.io/component": "controller"}}, Spec: core.PodSpec{ServiceAccountName: "manager", Containers: []core.Container{{Name: "controller", Command: []string{"cisco-vk", "manager", "--leader-elect", "--enable-managed-topology", "--" + managedprotocol.ControllerMetadataManagerFlag, "--topology-policy-namespace=system", "--topology-policy-name=policy", "--app-hosting-service-account=apps", "--network-management-service-account=network"}, Env: []core.EnvVar{{Name: "CONFIG_LEASE_NAMESPACE", Value: "leases"}}}}}, Status: core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue, LastTransitionTime: now}}, ContainerStatuses: []core.ContainerStatus{{Name: "controller", ImageID: "containerd://sha256:qualified-image", Ready: true, State: core.ContainerState{Running: &core.ContainerStateRunning{StartedAt: now}}}}}}
}

type metadataRuntimeReader struct {
	client.Reader
	mode  string
	lists int
}

func (r *metadataRuntimeReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	o := &client.ListOptions{}
	for _, opt := range opts {
		opt.ApplyToList(o)
	}
	if o.Namespace != "system" || o.LabelSelector != nil || o.FieldSelector != nil {
		return errors.New("manager proof requires complete account namespace")
	}
	r.lists++
	if r.mode == "denied" {
		return errors.New("Pod list forbidden")
	}
	if err := r.Reader.List(ctx, list, opts...); err != nil {
		return err
	}
	rows := list.(*core.PodList)
	if r.mode == "late-pod" && r.lists == 2 {
		other := *rows.Items[0].DeepCopy()
		other.Name = "overlap"
		other.UID = "overlap"
		rows.Items = append(rows.Items, other)
	}
	if r.mode == "late-version" && r.lists == 2 {
		rows.Items[0].ResourceVersion = "changed"
	}
	if r.mode == "truncated" {
		rows.Continue = "next"
	}
	return nil
}
func TestMetadataRuntimeManagerProof(t *testing.T) {
	for _, mode := range []string{"current", "disabled", "recovery-disabled", "duplicate-flag", "wrong-policy", "wrong-namespace", "value-from", "duplicate-env", "leader-off", "managed-off", "aggregated", "pending-peer", "terminal-peer", "foreign-account-peer", "future-ready", "not-running", "no-image", "sidecar", "init", "ephemeral", "wrong-command", "late-pod", "late-version", "denied", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			p := metadataRuntimeTestManager()
			b := metadataRuntimeTestBinding()
			execution := true
			switch mode {
			case "disabled", "recovery-disabled":
				p.Spec.Containers[0].Command = append(p.Spec.Containers[0].Command[:4], p.Spec.Containers[0].Command[5:]...)
				execution = mode != "recovery-disabled"
			case "duplicate-flag":
				p.Spec.Containers[0].Args = []string{"--" + managedprotocol.ControllerMetadataManagerFlag + "=false"}
			case "wrong-policy":
				b.PolicyName = "different"
			case "wrong-namespace":
				b.LeaseNamespace = "different"
			case "value-from":
				p.Spec.Containers[0].Env[0].ValueFrom = &core.EnvVarSource{FieldRef: &core.ObjectFieldSelector{FieldPath: "metadata.namespace"}}
			case "duplicate-env":
				p.Spec.Containers[0].Env = append(p.Spec.Containers[0].Env, p.Spec.Containers[0].Env[0])
			case "leader-off":
				p.Spec.Containers[0].Command[2] = "--leader-elect=false"
			case "managed-off":
				p.Spec.Containers[0].Command[3] = "--enable-managed-topology=false"
			case "aggregated":
				p.Spec.Containers[0].Args = []string{"--enable-config-aggregator"}
			case "future-ready":
				p.Status.Conditions[0].LastTransitionTime = metav1.NewTime(time.Now().Add(time.Hour))
			case "not-running":
				p.Status.ContainerStatuses[0].State.Running = nil
			case "no-image":
				p.Status.ContainerStatuses[0].ImageID = ""
			case "sidecar":
				p.Spec.Containers = append(p.Spec.Containers, core.Container{Name: "unqualified"})
			case "init":
				p.Spec.InitContainers = []core.Container{{Name: "unqualified"}}
			case "ephemeral":
				p.Spec.EphemeralContainers = []core.EphemeralContainer{{EphemeralContainerCommon: core.EphemeralContainerCommon{Name: "unqualified"}}}
			case "wrong-command":
				p.Spec.Containers[0].Command[0] = "unrelated"
			}
			scheme := runtime.NewScheme()
			_ = core.AddToScheme(scheme)
			objects := []client.Object{p}
			if strings.HasSuffix(mode, "peer") {
				peer := p.DeepCopy()
				peer.Name = "peer"
				peer.UID = "peer"
				if mode == "pending-peer" {
					peer.Status.Phase = core.PodPending
				}
				if mode == "terminal-peer" {
					peer.Status.Phase = core.PodSucceeded
				}
				if mode == "foreign-account-peer" {
					peer.Spec.ServiceAccountName = "unrelated"
				}
				objects = append(objects, peer)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			reader := &metadataRuntimeReader{Reader: c, mode: mode}
			got, err := ReadMetadataRuntimeManager(context.Background(), reader, b, execution)
			allowed := mode == "current" || mode == "recovery-disabled" || mode == "terminal-peer" || mode == "foreign-account-peer"
			if (err == nil) != allowed {
				t.Fatal("unexpected manager compatibility proof", err)
			}
			if allowed && (reader.lists != 2 || got.UID != p.UID) {
				t.Fatal("manager proof did not corroborate original singleton")
			}
		})
	}
}

func TestMetadataPlanningWorkerIncarnation(t *testing.T) {
	for _, mode := range []string{"current", "wrong-pod", "old-generation", "paused"} {
		t.Run(mode, func(t *testing.T) {
			_, nc, dep, rs, pod, now := controllerWorkerFixture(t)
			if mode == "old-generation" {
				nc.Generation++
			}
			if mode == "paused" {
				nc.Spec.Paused = true
			}
			scheme := runtime.NewScheme()
			_ = cvk.AddToScheme(scheme)
			_ = core.AddToScheme(scheme)
			_ = apps.AddToScheme(scheme)
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nc, dep, rs, pod).Build()
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(nc), nc); err != nil {
				t.Fatal(err)
			}
			ref := ops.ConfigurationReservationReference{Name: pod.Name, UID: string(pod.UID)}
			if mode == "wrong-pod" {
				ref.UID = "old-pod"
			}
			username, err := ReadMetadataPlanningWorker(context.Background(), c, nc, ref, now)
			if (err == nil) != (mode == "current") {
				t.Fatal("planning accepted an incompatible worker incarnation", err)
			}
			if mode == "current" && username != "system:serviceaccount:lab:connector" {
				t.Fatal("planning changed bound account identity")
			}
		})
	}
}
