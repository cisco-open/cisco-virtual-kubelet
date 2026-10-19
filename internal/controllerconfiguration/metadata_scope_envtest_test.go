//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"strings"
	"testing"
	"time"

	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	apps "k8s.io/api/apps/v1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// All source/controller, global Node/reservation lists and Lease reads use the
// real isolated API server. No installed manager or product API is exercised.
func TestEnvtestUnmanagedMetadataScope(t *testing.T) {
	env := &envtest.Environment{CRDDirectoryPaths: []string{"../../config/crd/cisco.vk_networkcontrollers.yaml", "../../config/crd/cisco.vk_ciscodevices.yaml", "../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml", "../../config/crd/ops.cisco.vk_controllerconfigurationreservations.yaml"}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := env.Stop(); err != nil {
			t.Error(err)
		}
	}()
	scheme := runtime.NewScheme()
	_ = core.AddToScheme(scheme)
	_ = apps.AddToScheme(scheme)
	_ = coordination.AddToScheme(scheme)
	_ = cvk.AddToScheme(scheme)
	_ = config.AddToScheme(scheme)
	_ = ops.AddToScheme(scheme)
	admin, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	const ns = "metadata-scope"
	if err := admin.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
		t.Fatal(err)
	}
	_, nc, intent := bootstrapIdentityFixture(t)
	status := nc.Status.DeepCopy()
	nc.ObjectMeta = metav1.ObjectMeta{Name: "catc", Namespace: ns}
	nc.Status = cvk.NetworkControllerStatus{}
	nc.Spec.Endpoint = "https://controller.invalid"
	nc.Spec.CredentialSecretRef.Name = "not-read"
	if err := admin.Create(ctx, nc); err != nil {
		t.Fatal(err)
	}
	intent.ObjectMeta = metav1.ObjectMeta{Name: "pnp", Namespace: ns}
	if err := admin.Create(ctx, intent); err != nil {
		t.Fatal(err)
	}
	op := status.Configuration.Operation
	op.ConfigUID = string(intent.UID)
	op.ConfigGeneration = intent.Generation
	op.ControllerGeneration = nc.Generation
	op.Action = "Delete"
	op.Resource = "pnp-remove:v2:native-pnp:" + strings.Repeat("b", 64)
	op.TargetSerial = ""
	status.Configuration.Owners[0].ConfigUID = string(intent.UID)
	nc.Status = *status
	if err := admin.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	read := func() error {
		t.Helper()
		if err := admin.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
			t.Fatal(err)
		}
		return CheckUnmanagedMetadataScope(ctx, admin, nc, ns, []string{"bootstrap123", "second123"})
	}
	if err := read(); err != nil {
		t.Fatal("empty actual API scope", err)
	}
	t.Log("source/owner/controller and two global censuses corroborated on actual API")
	installMetadataScopeAPIWorker(t, ctx, admin, nc)
	observation, err := ReadMetadataScopeWorker(ctx, admin, nc, ns, []string{"bootstrap123", "second123"}, time.Now())
	if err != nil || observation == nil || observation.Pod.UID == "" || len(observation.BindingDigest) != 64 {
		t.Fatal("actual API worker-chain metadata binding", err)
	}
	t.Log("actual API persisted Deployment/ReplicaSet/Pod chain bound to Prepared metadata source/cohort; status simulated, no container executes")

	const legacyNS = "metadata-legacy"
	if err := admin.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: legacyNS}}); err != nil {
		t.Fatal(err)
	}
	legacy := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "unknown-consumer", Namespace: legacyNS}, Spec: cvk.DeviceSpec{Address: "198.51.100.101", Driver: cvk.DeviceDriverXE, Username: "fixture"}}
	if err := admin.Create(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := read(); err == nil {
		t.Fatal("address-only consumer in another namespace escaped metadata census")
	}
	legacy.Spec.PhysicalIdentity = "different123"
	if err := admin.Update(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	legacy.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: string(legacy.UID), NodeName: "retained-node", NodeUID: "retained-node-uid", PhysicalIdentity: "different123"}
	if err := admin.Status().Update(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := read(); err != nil {
		t.Fatal("known unrelated retained consumer blocked metadata scope", err)
	}
	alias := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "declared-alias", Namespace: legacyNS}, Spec: cvk.DeviceSpec{Address: "198.51.100.103", Driver: cvk.DeviceDriverXE, Username: "fixture", PhysicalIdentity: "SECOND123"}}
	if err := admin.Create(ctx, alias); err != nil {
		t.Fatal(err)
	}
	alias.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: string(alias.UID), NodeName: "retained-alias", NodeUID: "retained-alias-uid", PhysicalIdentity: "second123"}
	if err := admin.Status().Update(ctx, alias); err != nil {
		t.Fatal(err)
	}
	if err := read(); err == nil {
		t.Fatal("declared/retained serial alias escaped metadata census")
	}
	if err := admin.Delete(ctx, alias); err != nil {
		t.Fatal(err)
	}
	if err := read(); err != nil {
		t.Fatal("removed alias left false conflict with unrelated known consumer", err)
	}
	if err := admin.Delete(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := read(); err != nil {
		t.Fatal("removed unknown/alias consumer left false scope conflict", err)
	}
	t.Log("actual cross-namespace address-only consumer refused; write-once declared/retained unrelated identity accepted; declared/retained cohort alias refused; consumer removal corroborated")

	// Stage foreign released custody under the real schema before actor
	// policies are installed below. This qualifies census retention/retirement,
	// not how an installed manager acquires or authorizes that foreign record.
	other := nc.DeepCopy()
	other.ObjectMeta = metav1.ObjectMeta{Name: nc.Name, Namespace: legacyNS}
	other.Status = cvk.NetworkControllerStatus{}
	if err := admin.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	ready := metav1.NewTime(time.Now().Add(-2 * time.Second))
	submitted := metav1.NewTime(time.Now().Add(-time.Second))
	terminal := metav1.Now()
	retainedKey, _ := BootstrapFenceName("second123")
	retainedOperation := nc.Status.Configuration.Operation.DeepCopy()
	other.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{MetadataHandoff: &cvk.NetworkControllerConfigurationMetadataHandoff{
		Protocol: cvk.NetworkControllerMetadataHandoffProtocol, BindingDigest: strings.Repeat("c", 64), LeaseNamespace: ns,
		WorkerPod: ops.ConfigurationReservationReference{Name: "retained-worker", UID: "retained-worker-uid"}, WorkerUsername: "system:serviceaccount:metadata-legacy:connector",
		PhysicalIdentities: []string{"second123"}, Operation: *retainedOperation,
		Fences:  []cvk.NetworkControllerMetadataFenceReference{{PhysicalIdentity: "second123", Name: retainedKey, UID: "retained-original-fence"}},
		ReadyAt: &ready, RefusedSubmissionAt: &submitted, RefusedAt: &terminal, SettlementDigest: strings.Repeat("d", 64), ReleasedAt: &terminal,
	}}
	if err := admin.Status().Update(ctx, other); err != nil {
		t.Fatal(err)
	}
	beforeRead := other.DeepCopy()
	if err := read(); err == nil {
		t.Fatal("foreign released metadata handoff without an active operation escaped census")
	}
	if err := CheckUnmanagedMetadataScope(ctx, admin, nc, ns, []string{"bootstrap123"}); err != nil {
		t.Fatal("unrelated retained cohort blocked metadata scope", err)
	}
	if err := admin.Get(ctx, client.ObjectKeyFromObject(other), other); err != nil {
		t.Fatal(err)
	}
	if other.ResourceVersion != beforeRead.ResourceVersion || other.Status.Configuration.MetadataHandoff == nil {
		t.Fatal("read-only census changed foreign custody")
	}
	other.Status.Configuration.MetadataHandoff = nil
	if err := admin.Status().Update(ctx, other); err != nil {
		t.Fatal("released schema-staged handoff retirement", err)
	}
	if err := read(); err != nil {
		t.Fatal("explicit retirement did not clear foreign metadata scope", err)
	}
	t.Log("schema-staged released/refused foreign handoff with no active operation and no Lease still holds matching serial; unrelated cohort allowed; only explicit retirement clears it; actor provenance staged")

	node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: "scope-orphan"}}
	if err := admin.Create(ctx, node); err != nil {
		t.Fatal(err)
	}
	if err := read(); err != nil {
		t.Fatal("ordinary compute Node without hardware observation blocked metadata scope", err)
	}
	node.Labels = map[string]string{topology.LabelProvider: topology.ProviderCiscoAppHosting}
	if err := admin.Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	if err := read(); err == nil {
		t.Fatal("recognized CVK Node without hardware identity escaped metadata census")
	}
	node.Status.NodeInfo.MachineID = "different123"
	node.Status.NodeInfo.SystemUUID = "different123"
	if err := admin.Status().Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	if err := read(); err != nil {
		t.Fatal("consistent unrelated CVK Node blocked metadata scope", err)
	}
	node.Status.NodeInfo.SystemUUID = "another123"
	if err := admin.Status().Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	if err := read(); err == nil {
		t.Fatal("contradictory CVK Node observations escaped metadata census")
	}
	node.Status.NodeInfo.MachineID = ""
	node.Status.NodeInfo.SystemUUID = "SECOND123"
	if err := admin.Status().Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	if err := read(); err == nil {
		t.Fatal("orphan Node alias was ignored")
	}
	if err := admin.Delete(ctx, node); err != nil {
		t.Fatal(err)
	}
	if err := read(); err != nil {
		t.Fatal("Node removal did not clear observation", err)
	}
	name, _ := BootstrapFenceName("second123")
	lease := &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
	if err := admin.Create(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if err := read(); err == nil {
		t.Fatal("malformed retained fence treated as absence")
	}
	if err := admin.Delete(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if err := read(); err != nil {
		t.Fatal(err)
	}
	reservation, _, _, _, _ := deviceBindingFixture(t)
	reservation.ObjectMeta = metav1.ObjectMeta{Name: "orphan-reservation", Namespace: ns}
	reservation.Spec.PhysicalIdentity = "SECOND123"
	reservation.Spec.Controller.Name = "old-controller"
	reservation.Spec.ControllerUsername = "system:serviceaccount:metadata-scope:old-controller-worker"
	reservation.Spec.ControllerWorkerPod = ops.ConfigurationReservationReference{Name: "old-controller-worker", UID: "old-controller-worker-uid"}
	if err := admin.Create(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	if err := read(); err == nil {
		t.Fatal("orphan reservation treated as clear")
	}
	if err := admin.Delete(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	if err := read(); err != nil {
		t.Fatal(err)
	}
	// Install retained-fence admission after the intentionally malformed Lease
	// fixtures above; acquisition then runs under the actual protected contract.
	qualifyMetadataSealOnAPI(t, ctx, cfg, admin, nc, observation)

	intent.Spec.ModelSource.Exporter = "external-change"
	if err := admin.Update(ctx, intent); err != nil {
		t.Fatal(err)
	}
	if err := read(); err == nil {
		t.Fatal("stale source generation accepted")
	}
	var unchanged cvk.NetworkController
	if err := admin.Get(ctx, client.ObjectKeyFromObject(nc), &unchanged); err != nil {
		t.Fatal(err)
	}
	if unchanged.Status.Configuration.Operation.Phase != "Submitting" || unchanged.Status.Configuration.Operation.TargetSerial != "" || unchanged.Status.Configuration.Operation.TaskID != "" {
		t.Fatal("staged metadata claim changed physical identity/task or lost Submitting custody")
	}
	// Claim is now Submitting; independently prove the changed source closes
	// binding, rather than relying only on the Prepared-only scope rejection.
	if err := readBoundConfigurationSource(ctx, admin, &unchanged, unchanged.Namespace, ops.ConfigurationReservationReference{Name: op.ConfigName, UID: op.ConfigUID}, op.ConfigGeneration); err == nil {
		t.Fatal("claimed journal ignored changed source generation")
	}
	t.Log("actual API Node alias, malformed bootstrap Lease, orphan reservation and source revision all hold observation closed; no grant/status/lease/native write by helper")
}

func installMetadataScopeAPIWorker(t *testing.T, ctx context.Context, admin client.Client, nc *cvk.NetworkController) {
	t.Helper()
	_, _, deployment, rs, pod, _ := controllerWorkerFixture(t)
	controlled := true
	deployment.ObjectMeta = metav1.ObjectMeta{Name: "connector", Namespace: nc.Namespace, Annotations: map[string]string{"cisco.vk/network-controller-name": nc.Name, "cisco.vk/network-controller-uid": string(nc.UID)}}
	args := []string{"controller-worker", "--controller-namespace=" + nc.Namespace, "--controller-name=" + nc.Name, "--controller-uid=" + string(nc.UID), "--controller-generation=1", "--controller-type=" + string(nc.Spec.Type)}
	deployment.Spec.Template.Spec.Containers[0].Args = args
	deployment.Spec.Template.Spec.Containers[0].Image = "example.invalid/worker:qualification"
	deployment.Status = apps.DeploymentStatus{}
	if err := admin.Create(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	deployment.Status = apps.DeploymentStatus{ObservedGeneration: deployment.Generation, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1}
	if err := admin.Status().Update(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	rs.ObjectMeta = metav1.ObjectMeta{Name: "replica", Namespace: nc.Namespace, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: deployment.Name, UID: deployment.UID, Controller: &controlled}}}
	rs.Spec.Template = *deployment.Spec.Template.DeepCopy()
	rs.Spec.Selector = deployment.Spec.Selector.DeepCopy()
	if err := admin.Create(ctx, rs); err != nil {
		t.Fatal(err)
	}
	if err := admin.Create(ctx, &core.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "connector", Namespace: nc.Namespace}}); err != nil {
		t.Fatal(err)
	}
	pod.ObjectMeta = metav1.ObjectMeta{Name: "worker", Namespace: nc.Namespace, Labels: deployment.Spec.Template.Labels, Annotations: deployment.Spec.Template.Annotations, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: rs.Name, UID: rs.UID, Controller: &controlled}}}
	pod.Spec = *deployment.Spec.Template.Spec.DeepCopy()
	pod.Spec.NodeName = "simulated-worker-node"
	pod.Status = core.PodStatus{}
	if err := admin.Create(ctx, pod); err != nil {
		t.Fatal(err)
	}
	pod.Status = core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().Add(-time.Second))}}}
	if err := admin.Status().Update(ctx, pod); err != nil {
		t.Fatal(err)
	}
	nc.Status.Worker = &cvk.NetworkControllerWorkerStatus{Name: "connector", DeploymentName: deployment.Name, ReadyReplicas: 1}
	if err := admin.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := admin.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
}
