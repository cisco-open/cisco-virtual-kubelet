// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build envtest

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/softwareupgrade"
	corev1 "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

// Exercise the actual rendered policy, custom verb authorization, and schema
// together. This is not a CEL string matcher or fake client authorization test.
func TestEnvtest_PreparationInvalidationNativeAuthorization(t *testing.T) {
	env := &envtest.Environment{CRDDirectoryPaths: []string{findControllerCRDPath(t)}, ErrorIfCRDPathMissing: true}
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
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ops.AddToScheme(scheme)
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rollout, leaf, now := invalidationCampaignFixture(t)
	request := rollout.Spec.PreparationInvalidation
	rollout.Spec.PreparationInvalidation = nil
	rollout.UID = ""
	rollout.ResourceVersion = ""
	root := filepath.Dir(filepath.Dir(findControllerCRDPath(t)))
	data, err := os.ReadFile(filepath.Join(root, "examples/topology/iosxe-software-rollout.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var example ops.IOSXESoftwareRollout
	if err := yaml.Unmarshal(data, &example); err != nil {
		t.Fatal(err)
	}
	rollout.Spec.Plan = example.Spec.Plan
	rollout.Spec.Plan.Strategy = ops.IOSXESoftwareRolloutStrategyPrepareOnly
	rollout.Spec.Control.RequestedBy = "operator"
	rollout.Spec.Control.RequestedAt = &metav1.Time{Time: now}
	rollout.Status.FrozenPlan.EncodedSizeBytes = 1
	rollout.Status.FrozenPlan.CreatedAt = metav1.NewTime(now)
	rollout.Status.FrozenPlan.CampaignGeneration = 1
	status := rollout.Status
	rollout.Status = ops.IOSXESoftwareRolloutStatus{}
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: rollout.Namespace}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(ctx, rollout); err != nil {
		t.Fatal(err)
	}
	rollout.Status = status
	if err := c.Status().Update(ctx, rollout); err != nil {
		t.Fatal(err)
	}
	testNativeInvalidationAudit(t, ctx, c, leaf, now, root)
	// Only the rollout policy is installed here; the full shared-account suite
	// separately qualifies the complete rendered policy set.
	rendered, err := exec.CommandContext(ctx, "helm", "template", "cvk", filepath.Join(root, "charts/cisco-virtual-kubelet"), "--namespace", "cvk-system", "--kube-version", "1.35.0", "--set", "topology.enabled=true", "--set", "gnoi.enableSoftwareUpgrade=true", "--set", "topology.workerAccounts.networkManagement.accessMode=readWrite", "--set", "controller.leaderElect=true", "--set", "rbac.profile=strict").CombinedOutput()
	if err != nil {
		t.Fatalf("helm: %v %s", err, rendered)
	}
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(rendered), 4096)
	installed := 0
	for {
		var object unstructured.Unstructured
		err := decoder.Decode(&object.Object)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(object.GetName(), "-managed-rollout") && (object.GetKind() == "ValidatingAdmissionPolicy" || object.GetKind() == "ValidatingAdmissionPolicyBinding") {
			if err := c.Create(ctx, &object); err != nil {
				t.Fatal(err)
			}
			installed++
		}
	}
	if installed != 2 {
		t.Fatalf("installed %d policy resources", installed)
	}
	for _, user := range []string{"planner", "recoverer"} {
		verbs := []string{"get", "update", "patch"}
		if user == "recoverer" {
			verbs = append(verbs, "recover")
		}
		role := &rbac.Role{ObjectMeta: metav1.ObjectMeta{Name: user, Namespace: rollout.Namespace}, Rules: []rbac.PolicyRule{{APIGroups: []string{"ops.cisco.vk"}, Resources: []string{"iosxesoftwarerollouts"}, Verbs: verbs}}}
		if err := c.Create(ctx, role); err != nil {
			t.Fatal(err)
		}
		if err := c.Create(ctx, &rbac.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: user, Namespace: rollout.Namespace}, RoleRef: rbac.RoleRef{APIGroup: rbac.GroupName, Kind: "Role", Name: user}, Subjects: []rbac.Subject{{Kind: "User", Name: user, APIGroup: rbac.GroupName}}}); err != nil {
			t.Fatal(err)
		}
	}
	patch := func(user, asserted string, dry bool) error {
		userCfg := rest.CopyConfig(cfg)
		userCfg.Impersonate = rest.ImpersonationConfig{UserName: user}
		caller, err := client.New(userCfg, client.Options{Scheme: scheme})
		if err != nil {
			return err
		}
		current := rollout.DeepCopy()
		if err := c.Get(ctx, client.ObjectKeyFromObject(rollout), current); err != nil {
			return err
		}
		current.Spec.PreparationInvalidation = request.DeepCopy()
		current.Spec.PreparationInvalidation.RequestedBy = asserted
		if dry {
			return caller.Update(ctx, current, client.DryRunAll)
		}
		return caller.Update(ctx, current)
	}
	// Wait for the admission binding to be enforced, without ever storing an
	// unauthorized request while the informer caches initialize.
	// Envtest has no controller-manager to publish policy TypeChecking status;
	// actual negative and positive requests below prove evaluation. The kind
	// suite also checks the published type-check diagnostics.
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := patch("planner", "planner", true)
		if (apierrors.IsForbidden(err) || apierrors.IsInvalid(err)) && strings.Contains(err.Error(), "distinct recover") {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("planner was not denied")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := patch("recoverer", "forged-actor", true); err == nil || !strings.Contains(err.Error(), "distinct recover") {
		t.Fatalf("actor spoof admitted: %v", err)
	}
	if err := patch("recoverer", "recoverer", false); err != nil {
		t.Fatalf("authorized recovery rejected: %v", err)
	}
	if err := patch("recoverer", "changed-actor", false); !apierrors.IsInvalid(err) {
		t.Fatalf("immutable request rewrite admitted: %v", err)
	}
}

func testNativeInvalidationAudit(t *testing.T, ctx context.Context, c client.Client, leaf *ops.IOSXESoftwareUpgrade, now time.Time, root string) {
	t.Helper()
	status := leaf.Status
	leaf.UID = ""
	leaf.ResourceVersion = ""
	leaf.Status = ops.IOSXESoftwareUpgradeStatus{}
	if err := c.Create(ctx, leaf); err != nil {
		t.Fatal(err)
	}
	status.ManagerAdmission.LeafUID = string(leaf.UID)
	status.PreparedReceipt.UpgradeUID = string(leaf.UID)
	status.PreparedReceipt.ReceiptHash, _ = softwareupgrade.PreparedReceiptHash(*status.PreparedReceipt)
	status.PrimarySupervisorInstallRequested = true
	status.WorkerControl.ObservedWorkerConfigRevision = "sha256:" + strings.Repeat("e", 64)
	status.WorkerControl.UpdatedAt = metav1.NewTime(now)
	leaf.Status = status
	if err := c.Status().Update(ctx, leaf); err != nil {
		t.Fatalf("prepared audit fixture: %v", err)
	}
	oldWriter := buildReleasedUpgradeSerializer(t, ctx, root)
	testReleasedUpgradeWriteDenied(t, ctx, c, oldWriter, leaf)
	leaf.Status.ManagerInvalidation = &ops.UpgradePreparedInvalidationRequest{
		ReceiptHash: status.PreparedReceipt.ReceiptHash, PlanHash: status.PreparedReceipt.PlanHash, CampaignUID: status.PreparedReceipt.CampaignUID,
		ControlRevision: status.ManagerControl.Revision, RequestedBy: "recoverer", RequestedAt: metav1.NewTime(now), Reason: "cancelled preparation",
	}
	if err := c.Status().Update(ctx, leaf); err != nil {
		t.Fatalf("recovery authority: %v", err)
	}
	invalid := leaf.DeepCopy()
	invalid.Status.Phase = ops.UpgradePhasePreparedInvalidated
	if err := c.Status().Update(ctx, invalid); !apierrors.IsInvalid(err) {
		t.Fatalf("phase without proof accepted: %v", err)
	}
	leaf.Status.Phase = ops.UpgradePhasePreparedInvalidated
	leaf.Status.PreparedInvalidation = &ops.UpgradePreparedInvalidationStatus{
		RequestHash: softwareupgrade.PreparedInvalidationRequestHash(leaf.Status.ManagerInvalidation), NativeEvidenceHash: "sha256:" + strings.Repeat("f", 64),
		ObservedAt: metav1.NewTime(now), WorkerRevision: "sha256:" + strings.Repeat("e", 64), WorkerPodUID: "worker-pod", RunningVersion: status.PreparedReceipt.RunningVersion, TargetState: "Absent",
	}
	if err := c.Status().Update(ctx, leaf); err != nil {
		t.Fatalf("valid proof rejected: %v", err)
	}
	if !softwareupgrade.PreparedReceiptInvalidated(leaf) {
		t.Fatal("API round trip invalidated proof")
	}
	testReleasedUpgradeWriteDenied(t, ctx, c, oldWriter, leaf)
	for name, mutate := range map[string]func(*ops.IOSXESoftwareUpgrade){
		"old writer drops new fields": func(u *ops.IOSXESoftwareUpgrade) {
			u.Status.ManagerInvalidation = nil
			u.Status.PreparedInvalidation = nil
		},
		"restore activation eligibility": func(u *ops.IOSXESoftwareUpgrade) { u.Status.Phase = ops.UpgradePhasePrepared },
		"rewrite native evidence": func(u *ops.IOSXESoftwareUpgrade) {
			u.Status.PreparedInvalidation.NativeEvidenceHash = "sha256:" + strings.Repeat("0", 64)
		},
		"delete receipt": func(u *ops.IOSXESoftwareUpgrade) { u.Status.PreparedReceipt = nil },
	} {
		candidate := leaf.DeepCopy()
		mutate(candidate)
		if err := c.Status().Update(ctx, candidate); !apierrors.IsInvalid(err) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
}

func buildReleasedUpgradeSerializer(t *testing.T, ctx context.Context, root string) string {
	t.Helper()
	const baseline = "cf33e51c8ffc6d47acb313857665366d74eefe6c"
	work := t.TempDir()
	archive := filepath.Join(work, "source.tar")
	for _, command := range [][]string{
		{"git", "-C", root, "archive", "--format=tar", "--output", archive, baseline},
		{"tar", "-xf", archive, "-C", work},
	} {
		if out, err := exec.CommandContext(ctx, command[0], command[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("prepare exact released source %s: %v: %s", baseline, err, out)
		}
	}
	binary := filepath.Join(work, "released-upgrade-roundtrip")
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-o", binary,
		filepath.Join(root, "scripts/testdata/october_upgrade_roundtrip.go"))
	build.Dir = work // Resolves the imports against the released go.mod/API.
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile released API serializer: %v: %s", err, out)
	}
	return binary
}

func testReleasedUpgradeWriteDenied(t *testing.T, ctx context.Context, c client.Client, binary string, leaf *ops.IOSXESoftwareUpgrade) {
	t.Helper()
	data, err := json.Marshal(leaf)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binary)
	command.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	encoded, err := command.Output()
	if err != nil {
		t.Fatalf("released serializer: %v: %s", err, &stderr)
	}
	var old unstructured.Unstructured
	if err := json.Unmarshal(encoded, &old.Object); err != nil {
		t.Fatal(err)
	}
	if _, found, err := unstructured.NestedFieldNoCopy(old.Object, "status", "preparedReceipt"); err != nil || found {
		t.Fatal("probe did not reproduce the released client's missing receipt field")
	}
	old.SetAPIVersion(ops.GroupVersion.String())
	old.SetKind("IOSXESoftwareUpgrade")
	// The retained new CRD must protect audit data even without native policy:
	// a rollback to an older typed writer is not permission to prune its state.
	if err := c.Status().Update(ctx, &old); !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "preparedReceipt") {
		t.Fatalf("released %s writer did not receive exact receipt preservation denial: %v", leaf.Status.Phase, err)
	}
	var after ops.IOSXESoftwareUpgrade
	if err := c.Get(ctx, client.ObjectKeyFromObject(leaf), &after); err != nil {
		t.Fatal(err)
	}
	if after.ResourceVersion != leaf.ResourceVersion {
		t.Fatal("rejected released write changed persisted audit/ownership")
	}
	t.Logf("released typed writer denied for %s; receipt and resourceVersion preserved", leaf.Status.Phase)
}
