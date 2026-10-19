// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	admission "k8s.io/api/admissionregistration/v1"
	apps "k8s.io/api/apps/v1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type metadataSubmissionHarness struct {
	fake, skipSeal bool
	manager        ctrlclient.Client
	activate       func(*testing.T, ctrlclient.Client, string, string, *cvk.NetworkController, []string)
	worker         func(*testing.T, *cvk.NetworkController, *core.Pod) ctrlclient.Client
}

type metadataSubmissionManager struct {
	ctrlclient.Client
	creates int
}

func (s *metadataSubmissionManager) Create(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.CreateOption) error {
	if l, ok := obj.(*coordination.Lease); ok {
		s.creates++
		l.UID = types.UID(fmt.Sprintf("metadata-fence-%d-%d", time.Now().UnixNano(), s.creates))
	}
	return s.Client.Create(ctx, obj, opts...)
}

// Namespace-scoped worker and rendered protection contracts in typed fake storage.
// Actual actor admission/server UID semantics are separately qualified in foundation.
func installPnPMetadataSubmissionFixture(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, s *configSourceSnapshot, harness ...*metadataSubmissionHarness) (contract.BootstrapFenceInstallation, string) {
	t.Helper()
	ctx := context.Background()
	client := a.statusWriter
	real := len(harness) > 0 && harness[0] != nil && !harness[0].fake
	_ = ops.AddToScheme(client.Scheme())
	_ = apps.AddToScheme(client.Scheme())
	_ = coordination.AddToScheme(client.Scheme())
	_ = admission.AddToScheme(client.Scheme())
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	one, controlled := int32(1), true
	template := core.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"metadata-worker": "connector"}, Annotations: map[string]string{"cisco.vk/controller-contract-hash": strings.Repeat("a", 64)}}, Spec: core.PodSpec{ServiceAccountName: "connector", Containers: []core.Container{{Name: "controller-worker", Image: "example.invalid/worker:test", Args: []string{"controller-worker", "--controller-namespace=" + nc.Namespace, "--controller-name=" + nc.Name, "--controller-uid=" + string(nc.UID), "--controller-generation=" + strconv.FormatInt(nc.Generation, 10), "--controller-type=catalyst-center"}}}}}
	dep := &apps.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "metadata-connector", Namespace: nc.Namespace, UID: "metadata-deployment", Generation: 1, Annotations: map[string]string{"cisco.vk/network-controller-name": nc.Name, "cisco.vk/network-controller-uid": string(nc.UID)}}, Spec: apps.DeploymentSpec{Replicas: &one, Selector: &metav1.LabelSelector{MatchLabels: template.Labels}, Strategy: apps.DeploymentStrategy{Type: apps.RecreateDeploymentStrategyType}, Template: template}, Status: apps.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1}}
	rs := &apps.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "metadata-replica", Namespace: nc.Namespace, UID: "metadata-replica", OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: dep.Name, UID: dep.UID, Controller: &controlled}}}, Spec: apps.ReplicaSetSpec{Template: template}}
	pod := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "metadata-worker", Namespace: nc.Namespace, UID: "metadata-worker", Labels: template.Labels, Annotations: template.Annotations, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: rs.Name, UID: rs.UID, Controller: &controlled}}}, Spec: template.Spec, Status: core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().Add(-time.Second))}}}}
	namespace := &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "leases"}, Status: core.NamespaceStatus{Phase: core.NamespaceActive}}
	if !real {
		namespace.UID = "metadata-leases"
	}
	if err := client.Create(ctx, namespace); err != nil {
		t.Fatal(err)
	}
	if err := client.Create(ctx, &core.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "connector", Namespace: nc.Namespace}}); err != nil {
		t.Fatal(err)
	}
	depStatus, podStatus := dep.Status, pod.Status
	if real {
		dep.UID = ""
		dep.Status = apps.DeploymentStatus{}
	}
	if err := client.Create(ctx, dep); err != nil {
		t.Fatal(err)
	}
	dep.Status = depStatus
	dep.Status.ObservedGeneration = dep.Generation
	if err := client.Status().Update(ctx, dep); err != nil {
		t.Fatal(err)
	}
	rs.OwnerReferences[0].UID = dep.UID
	if real {
		rs.UID = ""
		rs.Spec.Selector = dep.Spec.Selector.DeepCopy()
	}
	if err := client.Create(ctx, rs); err != nil {
		t.Fatal(err)
	}
	pod.OwnerReferences[0].UID = rs.UID
	if real {
		pod.UID = ""
		pod.Status = core.PodStatus{}
		pod.Spec.NodeName = "qualification-worker"
	}
	if err := client.Create(ctx, pod); err != nil {
		t.Fatal(err)
	}
	pod.Status = podStatus
	if err := client.Status().Update(ctx, pod); err != nil {
		t.Fatal(err)
	}
	nc.Status.Worker = &cvk.NetworkControllerWorkerStatus{Name: dep.Name, DeploymentName: dep.Name}
	if err := client.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := client.Get(ctx, ctrlclient.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	raw, err := exec.CommandContext(ctx, "helm", "template", "cvk", "../../../charts/cisco-virtual-kubelet", "--namespace", "system", "--kube-version", "1.35.0", "--set", "topology.enabled=true", "--set", "controller.leaderElect=true", "--set", "rbac.profile=strict", "--set", "config.leaseNamespace=leases", "--show-only", "templates/bootstrap-fence-admission.yaml", "--show-only", "templates/metadata-handoff-admission.yaml").CombinedOutput()
	if err != nil {
		t.Fatalf("metadata policy render: %v %s", err, raw)
	}
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	for {
		var object map[string]any
		err := decoder.Decode(&object)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(object)
		switch object["kind"] {
		case "ValidatingAdmissionPolicy":
			var p admission.ValidatingAdmissionPolicy
			if err := json.Unmarshal(encoded, &p); err != nil {
				t.Fatal(err)
			}
			if !real {
				p.UID = types.UID(p.Name)
			}
			p.Generation = 1
			p.Status = admission.ValidatingAdmissionPolicyStatus{ObservedGeneration: 1, TypeChecking: &admission.TypeChecking{}}
			if err := client.Create(ctx, &p); err != nil {
				t.Fatal(err)
			}
		case "ValidatingAdmissionPolicyBinding":
			var b admission.ValidatingAdmissionPolicyBinding
			if err := json.Unmarshal(encoded, &b); err != nil {
				t.Fatal(err)
			}
			if !real {
				b.UID = types.UID(b.Name)
			}
			b.Generation = 1
			if err := client.Create(ctx, &b); err != nil {
				t.Fatal(err)
			}
		}
	}
	installation := contract.BootstrapFenceInstallation{PolicyName: "cvk-cisco-virtual-kubelet-bootstrap-fence", ManagerUsername: "system:serviceaccount:system:cisco-virtual-kubelet-controller", LeaseNamespace: "leases"}
	policy := "cvk-cisco-virtual-kubelet-metadata-handoff"
	var manager ctrlclient.Client = &metadataSubmissionManager{Client: client}
	if real {
		serials, err := preparedPnPRemovalPhysicalIdentities(nc, c, controlleradapter.ConfigurationMetadataSource{Raw: s.raw, UID: string(s.uid), Version: s.version})
		if err != nil {
			t.Fatal(err)
		}
		manager = harness[0].manager
		harness[0].activate(t, client, installation.PolicyName, policy, nc, serials)
	}
	if len(harness) == 0 || harness[0] == nil || !harness[0].skipSeal {
		sealPnPMetadataSubmissionFixture(t, a, c, s, manager, installation, policy)
	}
	if real {
		a.statusWriter = harness[0].worker(t, nc, pod)
	}
	a.workerPodName, a.workerPodUID = pod.Name, string(pod.UID)
	return installation, policy
}

type metadataSubmitStatus struct {
	ctrlclient.Client
	mode string
}

func (s *metadataSubmitStatus) Status() ctrlclient.SubResourceWriter {
	return metadataSubmitStatusWriter{s.Client.Status(), s}
}

type metadataSubmitStatusWriter struct {
	ctrlclient.SubResourceWriter
	store *metadataSubmitStatus
}

func (w metadataSubmitStatusWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	n := obj.(*cvk.NetworkController)
	claim := n.Status.Configuration != nil && n.Status.Configuration.Operation != nil && n.Status.Configuration.Operation.Phase == "Submitting"
	if claim && w.store.mode == "claim-conflict" {
		var concurrent cvk.NetworkController
		if err := w.store.Client.Get(ctx, ctrlclient.ObjectKeyFromObject(n), &concurrent); err != nil {
			return err
		}
		concurrent.Status.Phase = cvk.NetworkControllerPhaseConnecting
		if err := w.SubResourceWriter.Update(ctx, &concurrent); err != nil {
			return err
		}
	}
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if claim && w.store.mode == "claim-lost-ack" {
		return errors.New("claim response lost")
	}
	return nil
}

type metadataSubmitRoundTrip func(*http.Request) (*http.Response, error)

func (f metadataSubmitRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPnPMetadataPreparedSubmission(t *testing.T) {
	for _, mode := range []string{"verified", "unsealed", "disabled", "wrong-worker", "claim-lost-ack", "claim-conflict", "native-lost", "late-sibling", "source-drift", "policy-drift", "settle-lost-intent", "settle-lost-marker", "settle-lost-delete", "settle-lost-ack", "settle-lost-retire", "settle-replacement", "settle-no-runtime", "settle-foreign-history", "settle-pruned-intent", "settle-late-replacement", "settle-worker-gone", "settle-missing-source", "settle-edit-before", "settle-edit-marker", "settle-edit-delete", "settle-edit-release"} {
		t.Run(mode, func(t *testing.T) { exercisePnPMetadataPreparedSubmission(t, mode, nil, nil) })
	}
}
func exercisePnPMetadataPreparedSubmission(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig), harness *metadataSubmissionHarness) {
	nativeMode := "cycle"
	if mode == "native-lost" {
		nativeMode = "metadata-lost"
	}
	if mode == "late-sibling" {
		nativeMode = "late-blocked-second"
	}
	exercisePnPRemovalCohort(t, nativeMode, install, func(a *adapter, c *configapi.NetworkControllerConfig, s *configSourceSnapshot, original *configurationWrite) {
		ctx := context.Background()
		if mode == "unsealed" {
			a.workerPodName, a.workerPodUID = "configured-worker", "configured-worker-uid"
			if err := a.ConfigureMetadataHandoff(contract.BootstrapFenceInstallation{PolicyName: "bootstrap", ManagerUsername: "system:serviceaccount:system:manager", LeaseNamespace: "leases"}, "metadata", func(context.Context, *cvk.NetworkController) error { return nil }); err != nil {
				t.Fatal(err)
			}
			if err := a.submitConfigurationWrite(ctx, c, s, "inventory", original); err == nil {
				t.Fatal("configured metadata runtime used unfenced legacy submission")
			}
			state := journalState(t, a)
			if state.Operation == nil || state.Operation.Phase != "Prepared" || state.MetadataHandoff != nil || len(state.ActionApplications) != 2 {
				t.Fatal("unsealed work did not remain Prepared without native receipt")
			}
			// Even a tampered Submitting slot cannot let the old unfenced plan
			// bypass a configured metadata runtime's native authority guard.
			nc, e := a.currentController(ctx)
			if e != nil {
				t.Fatal(e)
			}
			nc.Status.Configuration.Operation.Phase = "Submitting"
			at := metav1.Now()
			nc.Status.Configuration.Operation.SubmittedAt = &at
			if e := a.statusWriter.Status().Update(ctx, nc); e != nil {
				t.Fatal(e)
			}
			if e := original.submitCompleted(ctx); e == nil {
				t.Fatal("tampered unsealed slot bypassed native guard")
			}
			rows, err := a.client.pnpRemovalDevices(ctx)
			if err != nil || len(rows) != 2 {
				t.Fatal("unsealed submission changed native catalog", err)
			}
			return
		}
		op, err := a.prepareConfiguration(ctx, c, s, "inventory", original.resource, "Delete", original.identity)
		if err != nil {
			t.Fatal(err)
		}
		installation, policy := installPnPMetadataSubmissionFixture(t, a, c, s, harness)
		if mode != "disabled" {
			if err := a.ConfigureMetadataHandoff(installation, policy, func(context.Context, *cvk.NetworkController) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
		if mode == "wrong-worker" {
			a.workerPodUID = "replaced-worker"
		}
		rawClient := a.statusWriter
		if strings.HasPrefix(mode, "cancel-") {
			manager := rawClient
			if harness != nil && harness.manager != nil {
				manager = harness.manager
			}
			qualifyPnPMetadataCancellation(t, ctx, a, c, manager, rawClient, installation, policy, mode, harness != nil && !harness.fake)
			return
		}
		fault := &metadataSubmitStatus{Client: rawClient, mode: mode}
		a.statusWriter = fault
		var deletes atomic.Int32
		injected := false
		a.client.mu.Lock()
		a.client.builtAt = time.Time{}
		a.client.wrapTransport = func(base http.RoundTripper) http.RoundTripper {
			return metadataSubmitRoundTrip(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodDelete {
					deletes.Add(1)
				}
				if !injected && (mode == "source-drift" || mode == "policy-drift") && req.Method == http.MethodGet && req.URL.Path == pnpDevicesPath {
					current, e := a.currentController(ctx)
					if e != nil {
						return nil, e
					}
					if current.Status.Configuration.Operation != nil && current.Status.Configuration.Operation.Phase == "Submitting" {
						injected = true
						if mode == "source-drift" {
							fresh := c.DeepCopy()
							fresh.Generation++
							fresh.Spec.ModelSource.Exporter = "changed"
							if e := rawClient.Update(ctx, fresh); e != nil {
								return nil, e
							}
						} else {
							var p admission.ValidatingAdmissionPolicy
							if e := rawClient.Get(ctx, ctrlclient.ObjectKey{Name: policy}, &p); e != nil {
								return nil, e
							}
							ignore := admission.Ignore
							p.Spec.FailurePolicy = &ignore
							if e := rawClient.Update(ctx, &p); e != nil {
								return nil, e
							}
						}
					}
				}
				return base.RoundTrip(req)
			})
		}
		a.client.mu.Unlock()
		before := journalState(t, a).DeepCopy()
		err = a.resumeOwnedPnPMetadataRemoval(ctx, c, s, op)
		state := journalState(t, a)
		if mode == "claim-conflict" {
			if err == nil || deletes.Load() != 0 || state.Operation.Phase != "Prepared" {
				t.Fatal("conflicted claim dispatched", err)
			}
			fault.mode = "valid"
			err = a.resumeOwnedPnPMetadataRemoval(ctx, c, s, op)
			state = journalState(t, a)
		}
		good := mode == "verified" || mode == "claim-conflict" || strings.HasPrefix(mode, "settle-")
		if good {
			if err != nil || deletes.Load() != 1 || state.Operation.Phase != "Verifying" || state.Operation.ID != op.ID {
				t.Fatal("exact acknowledged resume failed", err, deletes.Load())
			}
			if err := a.recordOwnedPnPRemoval(ctx, c, s, op.ID); err != nil {
				t.Fatal(err)
			}
			state = journalState(t, a)
			if state.Operation.Phase != "Verified" || !pnpRemovedReceiptPresent(state, state.Operation) || len(state.ActionApplications) != len(before.ActionApplications)+1 || !reflect.DeepEqual(before.ActionApplications, state.ActionApplications[:len(before.ActionApplications)]) {
				t.Fatal("verified removal lost exact creator/tombstone history")
			}
		} else if mode == "native-lost" {
			if err == nil || deletes.Load() != 1 || state.Operation == nil || state.Operation.Phase != "Unknown" || state.MetadataHandoff.RefusedAt != nil {
				t.Fatal("lost native response was replayed or certified absent", err)
			}
			if err := a.recordOwnedPnPRemoval(ctx, c, s, op.ID); err == nil {
				t.Fatal("absence without original acknowledgement became success")
			}
		} else if mode == "late-sibling" || mode == "source-drift" || mode == "policy-drift" {
			if !errors.Is(err, errConfigurationNotDispatched) || deletes.Load() != 0 || state.Operation != nil || state.MetadataHandoff.RefusedAt == nil || !reflect.DeepEqual(before.ActionApplications, state.ActionApplications) {
				t.Fatal("original guard refusal lost certified custody", err, deletes.Load())
			}
		} else {
			if err == nil || deletes.Load() != 0 || !reflect.DeepEqual(before.ActionApplications, state.ActionApplications) {
				t.Fatal("unadmitted resume mutated native state", err)
			}
			if mode == "claim-lost-ack" && (state.Operation == nil || state.Operation.Phase != "Submitting" || state.MetadataHandoff.RefusedAt != nil) {
				t.Fatal("uncertain claim was not retained")
			}
		}
		count := deletes.Load()
		if err := a.resumeOwnedPnPMetadataRemoval(ctx, c, s, op); err == nil || deletes.Load() != count {
			t.Fatal("resumed completed/uncertain/refused operation replayed", err)
		}
		if _, err := a.planOwnedPnPRemoval(ctx, c, s, op.TargetID); err == nil {
			t.Fatal("new planning bypassed retained handoff")
		}
		if err := a.finishConfiguration(ctx, op.ID); err == nil {
			t.Fatal("worker released manager custody")
		}
		nc, e := a.currentController(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if nc.Status.Configuration.MetadataHandoff.ReleasedAt != nil {
			t.Fatal("submission fabricated manager release")
		}
		if _, e := contract.ReadMetadataFenceCohort(ctx, a.statusReader, nc); e != nil {
			t.Fatal("original fences lost after execution/refusal", e)
		}
		if good || state.MetadataHandoff.RefusedAt != nil {
			manager := rawClient
			if harness != nil {
				manager = harness.manager
			}
			qualifyPnPMetadataSettlement(t, ctx, a, c, s, nc, manager, installation, policy, mode)
		} else {
			if _, e := contract.SettleMetadataHandoff(ctx, a.statusReader, rawClient, nc, installation, policy, pnpMetadataSettlementState, func(context.Context, *cvk.NetworkController) error { return nil }, time.Now()); e == nil {
				t.Fatal("nonterminal outcome released custody")
			}
		}

	})
}

func sealPnPMetadataSubmissionFixture(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, s *configSourceSnapshot, manager ctrlclient.Client, installation contract.BootstrapFenceInstallation, policy string) {
	t.Helper()
	ctx := context.Background()
	client := a.statusWriter
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	serials, err := preparedPnPRemovalPhysicalIdentities(nc, c, controlleradapter.ConfigurationMetadataSource{Raw: s.raw, UID: string(s.uid), Version: s.version})
	if err != nil {
		t.Fatal(err)
	}
	compatible := func(context.Context, *cvk.NetworkController, contract.MetadataScopeWorkerObservation, contract.AdmissionProtection) error {
		return nil
	}
	if _, err := contract.EnsureMetadataHandoff(ctx, client, manager, nc, policy, installation.ManagerUsername, "leases", serials, compatible, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := client.Get(ctx, ctrlclient.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	if _, err := contract.AcquireMetadataFenceCohort(ctx, client, manager, nc, installation, policy, serials, compatible, time.Now()); err != nil {
		t.Fatal(err)
	}

}
