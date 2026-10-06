// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build envtest

package controller

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

// This exercises complete planning reconciles, not the approved execution
// state machine or a sustained manager/watch/RSS benchmark. Fixtures have real
// API identities/defaults but no running worker, credentials or device client.
func TestEnvtest_PlanningReconcileReadBudget(t *testing.T) {
	env := &envtest.Environment{CRDDirectoryPaths: []string{findControllerCRDPath(t)}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Error(err)
		}
	})
	cfg.QPS, cfg.Burst = 1000, 1000
	var requests atomic.Int64
	cfg.WrapTransport = func(rt http.RoundTripper) http.RoundTripper { return planningRequestCounter{rt, &requests} }
	c, err := client.New(cfg, client.Options{Scheme: drainTestScheme(t)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Second)
	for _, name := range []string{"fleet", "cvk-system"} {
		if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 1000; i++ {
		d, n := fleetReadFixture(i)
		d.UID, n.UID = "", ""
		d.Spec.Driver, d.Spec.Address, d.Spec.Username = ciskov1.DeviceDriverXE, "192.0.2.1", "fixture"
		d.Labels = map[string]string{"topology.cisco.vk/managed": "true"}
		d.Status = ciskov1.DeviceStatus{}
		if err := c.Create(ctx, d); err != nil {
			t.Fatal(err)
		}
		if err := c.Create(ctx, n); err != nil {
			t.Fatal(err)
		}
		if i >= 100 {
			continue
		}
		bindFleetReadFixture(d, n)
		workers := planningReadyFixture(t, d, n, now)
		for _, count := range []int{1, 10, 50, 100} {
			if i < count {
				d.Labels[fmt.Sprintf("test.cisco.vk/targets-%d", count)] = "true"
			}
		}
		persistPlanningWorkers(t, ctx, c, d, workers)
		deviceStatus := d.Status.DeepCopy()
		if err := c.Update(ctx, d); err != nil {
			t.Fatal(err)
		}
		// Main-resource updates omit status; preserve the intended fixture first.
		d.Status = *deviceStatus
		d.Status.TopologyProjection.SourceResourceVersion = d.ResourceVersion
		if err := c.Status().Update(ctx, d); err != nil {
			t.Fatal(err)
		}
		nodeStatus := n.Status.DeepCopy()
		if err := c.Update(ctx, n); err != nil {
			t.Fatal(err)
		}
		n.Status = *nodeStatus
		if err := c.Status().Update(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	_, parsed := rolloutPolicyFixture()
	parsed.Config.FleetSelector = metav1.LabelSelector{MatchLabels: map[string]string{"topology.cisco.vk/managed": "true"}}
	policyJSON, err := topologyrollout.CanonicalPolicyJSON(parsed.Config)
	if err != nil {
		t.Fatal(err)
	}
	policy := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "cvk-system", Name: "topology-policy", Annotations: map[string]string{
		topologyrollout.PolicyManagedAnnotation: "true", topologyrollout.ConfigLeaseNamespaceAnnotation: "", topologyrollout.AdmissionPrefixAnnotation: "cvk-topology",
	}}, Data: map[string]string{topologyrollout.PolicyDataKey: policyJSON}}
	for _, object := range []client.Object{policy, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "cvk-system", Name: parsed.Config.LedgerName}}} {
		if err := c.Create(ctx, object); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := topologyrollout.BootstrapAdminPolicy(ctx, c, c, client.ObjectKeyFromObject(policy)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(findControllerCRDPath(t))), "examples/topology/iosxe-software-rollout.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var example ops.IOSXESoftwareRollout
	if err := yaml.Unmarshal(data, &example); err != nil {
		t.Fatal(err)
	}
	r := &IOSXESoftwareRolloutReconciler{Client: c, APIReader: c, Scheme: drainTestScheme(t), Now: func() time.Time { return now }, TopologyPolicyNamespace: policy.Namespace, TopologyPolicyName: policy.Name}
	for _, count := range []int{1, 10, 50, 100} {
		latencies := make([]time.Duration, 20)
		var maxReads int64
		for sample := range latencies {
			campaign := example.DeepCopy()
			campaign.ObjectMeta = metav1.ObjectMeta{Namespace: "fleet", Name: fmt.Sprintf("plan-%d-%d", count, sample), Finalizers: []string{rolloutSafetyFinalizer}}
			campaign.Spec.Plan.Targets = ops.IOSXESoftwareRolloutTargetSpec{MaxTargets: int32(count), Selector: ops.IOSXESoftwareRolloutLabelSelector{MatchLabels: map[string]string{fmt.Sprintf("test.cisco.vk/targets-%d", count): "true"}}}
			campaign.Spec.Plan.Canaries = []ops.IOSXESoftwareRolloutCanaryCohort{{Name: "c9300", Devices: []string{"fleet-0000"}}}
			campaign.Spec.Plan.Image = rolloutSourceImageFixture()
			campaign.Spec.Plan.Image.Sources = campaign.Spec.Plan.Image.Sources[:1]
			campaign.Spec.Plan.Budgets.Domains = nil
			campaign.Spec.Plan.Health.Network = &ops.IOSXESoftwareRolloutNetworkHealthSpec{Enabled: true, RequireCompleteEvidence: true}
			if err := c.Create(ctx, campaign); err != nil {
				t.Fatal(err)
			}
			requests.Store(0)
			started := time.Now()
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(campaign)}); err != nil {
				t.Fatal(err)
			}
			latencies[sample] = time.Since(started)
			calls := requests.Load()
			maxReads = max(maxReads, calls)
			if calls > 250 {
				t.Fatalf("targets=%d reconcile requests=%d exceed 250", count, calls)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(campaign), campaign); err != nil {
				t.Fatal(err)
			}
			if campaign.Status.Phase != ops.IOSXESoftwareRolloutPhaseAwaitingApproval || campaign.Status.FrozenPlan == nil || len(campaign.Status.FrozenPlan.Targets) != count {
				t.Fatalf("planning did not freeze all %d targets: %s %s", count, campaign.Status.Phase, campaign.Status.Message)
			}
		}
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		t.Logf("fleet=1000 targets=%d samples=20 fullPlanningReconcile maxAPIRequests=%d p50=%s p95=%s p99=%s", count, maxReads, latencies[9], latencies[18], latencies[19])
		if latencies[9] > 500*time.Millisecond || latencies[18] > 2*time.Second || latencies[19] > 5*time.Second {
			t.Fatal("planning exceeds the predeclared latency budget")
		}
	}
	var leaves ops.IOSXESoftwareUpgradeList
	if err := c.List(ctx, &leaves); err != nil || len(leaves.Items) != 0 {
		t.Fatalf("unapproved planning produced leaves: %d %v", len(leaves.Items), err)
	}
}

type planningRequestCounter struct {
	http.RoundTripper
	calls *atomic.Int64
}

func (p planningRequestCounter) RoundTrip(request *http.Request) (*http.Response, error) {
	p.calls.Add(1)
	return p.RoundTripper.RoundTrip(request)
}

func persistPlanningWorkers(t *testing.T, ctx context.Context, c client.Client, d *ciskov1.CiscoDevice, workers []client.Object) {
	t.Helper()
	deployment := workers[0].(*appsv1.Deployment)
	rs := workers[1].(*appsv1.ReplicaSet)
	pod := workers[2].(*corev1.Pod)
	deployment.UID, deployment.ResourceVersion, deployment.Generation = "", "", 0
	deployment.Spec.Selector = &metav1.LabelSelector{MatchLabels: deployment.Spec.Template.Labels}
	applyVKPodTemplateDefaults(&deployment.Spec.Template)
	revision, err := managedWorkerPodTemplateRevision(&deployment.Spec.Template)
	if err != nil {
		t.Fatal(err)
	}
	deployment.Spec.Template.Annotations[managedprotocol.AnnotationWorkerConfigRevision] = revision
	if err := c.Create(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	deployment.Status = appsv1.DeploymentStatus{ObservedGeneration: deployment.Generation, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1}
	if err := c.Status().Update(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	rs.UID, rs.ResourceVersion = "", ""
	rs.OwnerReferences[0].UID = deployment.UID
	rs.Spec.Selector, rs.Spec.Template = deployment.Spec.Selector.DeepCopy(), *deployment.Spec.Template.DeepCopy()
	if err := c.Create(ctx, rs); err != nil {
		t.Fatal(err)
	}
	podStatus := pod.Status.DeepCopy()
	pod.UID, pod.ResourceVersion = "", ""
	pod.OwnerReferences[0].UID = rs.UID
	pod.Annotations[managedprotocol.AnnotationWorkerConfigRevision] = revision
	pod.Spec = *deployment.Spec.Template.Spec.DeepCopy()
	if err := c.Create(ctx, pod); err != nil {
		t.Fatal(err)
	}
	pod.Status = *podStatus
	if err := c.Status().Update(ctx, pod); err != nil {
		t.Fatal(err)
	}
	d.Status.NetworkWorkerRevision.DesiredRevision, d.Status.NetworkWorkerRevision.ObservedRevision = revision, revision
	d.Status.NetworkWorkerRevision.DeploymentUID, d.Status.NetworkWorkerRevision.DeploymentGeneration = string(deployment.UID), deployment.Generation
	d.Status.NetworkWorkerRevision.PodUID = string(pod.UID)
	d.Status.HealthObservation.AcceptedNetwork.WorkerPodUID, d.Status.HealthObservation.AcceptedNetwork.ProducerRevision = string(pod.UID), revision
}
