//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	admission "k8s.io/api/admissionregistration/v1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// These clients have broad test RBAC through impersonated system:masters;
// identity denials therefore exercise real admission rather than missing RBAC.
// No product API, running worker or actual retained fence is involved.
func TestMetadataHandoffActorAdmission(t *testing.T) {
	env := &envtest.Environment{CRDDirectoryPaths: []string{"../../config/crd/cisco.vk_networkcontrollers.yaml"}, ErrorIfCRDPathMissing: true}
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
	_ = admission.AddToScheme(scheme)
	_ = cvk.AddToScheme(scheme)
	admin, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	const ns = "metadata-tenant"
	for _, name := range []string{ns, "manager-system", "metadata-leases"} {
		if err := admin.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}); err != nil {
			t.Fatal(err)
		}
	}
	caller := func(username, pod, uid string) client.Client {
		config := rest.CopyConfig(cfg)
		config.Impersonate = rest.ImpersonationConfig{UserName: username, Groups: []string{"system:masters"}}
		if uid != "" {
			config.Impersonate.Extra = map[string][]string{"authentication.kubernetes.io/pod-uid": {uid}}
		}
		if pod != "" {
			if config.Impersonate.Extra == nil {
				config.Impersonate.Extra = map[string][]string{}
			}
			config.Impersonate.Extra["authentication.kubernetes.io/pod-name"] = []string{pod}
		}
		c, err := client.New(config, client.Options{Scheme: scheme})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	const managerUser = "system:serviceaccount:manager-system:cisco-virtual-kubelet-controller"
	const workerUser = "system:serviceaccount:metadata-tenant:connector"
	manager := caller(managerUser, "", "")
	worker := caller(workerUser, "worker", "worker-uid")
	oldWorker := caller(workerUser, "worker", "old-worker-uid")
	missingPod := caller(workerUser, "", "worker-uid")
	other := caller("system:serviceaccount:metadata-tenant:other", "worker", "worker-uid")
	raw, err := exec.CommandContext(ctx, "helm", "template", "cvk", "../../charts/cisco-virtual-kubelet", "--namespace", "manager-system", "--kube-version", "1.35.0", "--set", "topology.enabled=true", "--set", "controller.leaderElect=true", "--set", "rbac.profile=strict", "--set", "config.leaseNamespace=metadata-leases", "--show-only", "templates/metadata-handoff-admission.yaml").CombinedOutput()
	if err != nil {
		t.Fatalf("helm: %v %s", err, raw)
	}
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	policyName := ""
	for {
		var obj unstructured.Unstructured
		err := decoder.Decode(&obj.Object)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if obj.GetKind() == "ValidatingAdmissionPolicy" {
			policyName = obj.GetName()
		}
		if err := admin.Create(ctx, &obj); err != nil {
			t.Fatal(err)
		}
	}
	if policyName == "" {
		t.Fatal("actor policy not rendered")
	}
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "catc", Namespace: ns}, Spec: cvk.NetworkControllerSpec{Type: "catalyst-center", Endpoint: "https://controller.invalid", CredentialSecretRef: cvk.NetworkControllerSecretReference{Name: "not-read"}}}
	if err := admin.Create(ctx, nc); err != nil {
		t.Fatal(err)
	}
	op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "intent", ConfigUID: "intent-uid", ConfigGeneration: 1, ControllerGeneration: nc.Generation, Section: "inventory", Resource: "pnp-remove:v2:native-pnp:" + strings.Repeat("b", 64), Action: "Delete", Phase: "Prepared", TargetID: "native-pnp"}
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: op.DeepCopy(), Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: op.ConfigName, ConfigUID: op.ConfigUID}}}
	if err := admin.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	key := client.ObjectKeyFromObject(nc)
	handoff := &cvk.NetworkControllerConfigurationMetadataHandoff{Protocol: cvk.NetworkControllerMetadataHandoffProtocol, BindingDigest: strings.Repeat("c", 64), LeaseNamespace: "metadata-leases", WorkerUsername: workerUser, WorkerPod: ops.ConfigurationReservationReference{Name: "worker", UID: "worker-uid"}, PhysicalIdentities: []string{"serial123", "serial456"}, Operation: op}
	fetch := func() *cvk.NetworkController {
		t.Helper()
		var n cvk.NetworkController
		if err := admin.Get(ctx, key, &n); err != nil {
			t.Fatal(err)
		}
		return &n
	}
	update := func(c client.Client, modify func(*cvk.NetworkController), dry bool) error {
		n := fetch()
		modify(n)
		if dry {
			return c.Status().Update(ctx, n, client.DryRunAll)
		}
		return c.Status().Update(ctx, n)
	}
	seal := func(n *cvk.NetworkController) { n.Status.Configuration.MetadataHandoff = handoff.DeepCopy() }
	deniedByPolicy := func(err error) bool {
		return err != nil && (apierrors.IsForbidden(err) || apierrors.IsInvalid(err)) && strings.Contains(err.Error(), policyName)
	}
	deny := func(err error) {
		t.Helper()
		if !deniedByPolicy(err) {
			t.Fatalf("expected actor policy denial, got type %T reason %s error %v", err, apierrors.ReasonForError(err), err)
		}
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		err := update(worker, seal, true)
		if deniedByPolicy(err) {
			break
		}
		if err != nil || time.Now().After(deadline) {
			t.Fatal("metadata actor policy not active", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Run("worker-cannot-seal", func(t *testing.T) { deny(update(worker, seal, true)) })
	t.Run("other-cannot-seal", func(t *testing.T) { deny(update(other, seal, true)) })
	t.Run("manager-wrong-namespace", func(t *testing.T) {
		deny(update(manager, func(n *cvk.NetworkController) {
			seal(n)
			n.Status.Configuration.MetadataHandoff.LeaseNamespace = "other"
		}, true))
	})
	t.Run("manager-forged-refusal", func(t *testing.T) {
		deny(update(manager, func(n *cvk.NetworkController) {
			seal(n)
			at := metav1.Now()
			n.Status.Configuration.MetadataHandoff.RefusedAt = &at
			n.Status.Configuration.MetadataHandoff.RefusedSubmissionAt = at.DeepCopy()
		}, true))
	})
	t.Run("manager-cannot-attach-fences-during-initial-seal", func(t *testing.T) {
		deny(update(manager, func(n *cvk.NetworkController) {
			seal(n)
			n.Status.Configuration.MetadataHandoff.Fences = []cvk.NetworkControllerMetadataFenceReference{{PhysicalIdentity: "serial123", Name: "cvk-bootstrap-" + strings.Repeat("d", 32), UID: "first"}, {PhysicalIdentity: "serial456", Name: "cvk-bootstrap-" + strings.Repeat("e", 32), UID: "second"}}
		}, true))
	})

	if err := update(manager, seal, false); err != nil {
		t.Fatal("manager seal denied", err)
	}
	t.Run("manager-delete-unreleased", func(t *testing.T) { deny(manager.Delete(ctx, fetch(), client.DryRunAll)) })
	t.Run("worker-delete-unreleased", func(t *testing.T) { deny(worker.Delete(ctx, fetch(), client.DryRunAll)) })
	begin := func(n *cvk.NetworkController) {
		at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
		n.Status.Configuration.Operation.Phase = "Submitting"
		n.Status.Configuration.Operation.SubmittedAt = &at
	}
	attachFences := func(n *cvk.NetworkController) {
		n.Status.Configuration.MetadataHandoff.Fences = []cvk.NetworkControllerMetadataFenceReference{{PhysicalIdentity: "serial123", Name: "cvk-bootstrap-" + strings.Repeat("d", 32), UID: "original-fence-uid"}, {PhysicalIdentity: "serial456", Name: "cvk-bootstrap-" + strings.Repeat("e", 32), UID: "second-fence-uid"}}
	}
	t.Run("worker-cannot-submit-without-fences", func(t *testing.T) { deny(update(worker, begin, true)) })
	t.Run("worker-cannot-publish-fences", func(t *testing.T) { deny(update(worker, attachFences, true)) })
	t.Run("manager-cannot-publish-and-submit", func(t *testing.T) {
		deny(update(manager, func(n *cvk.NetworkController) { attachFences(n); begin(n) }, true))
	})
	for _, mode := range []string{"duplicate-identity", "foreign-identity", "empty-uid", "wrong-name"} {
		t.Run("fence-publication-shape-"+mode, func(t *testing.T) {
			err := update(manager, func(n *cvk.NetworkController) {
				attachFences(n)
				h := n.Status.Configuration.MetadataHandoff
				switch mode {
				case "duplicate-identity":
					h.Fences[1].PhysicalIdentity = h.Fences[0].PhysicalIdentity
				case "foreign-identity":
					h.Fences[1].PhysicalIdentity = "foreign"
				case "empty-uid":
					h.Fences[0].UID = ""
				case "wrong-name":
					h.Fences[0].Name = "unrelated"
				}
			}, true)
			if !apierrors.IsInvalid(err) && !apierrors.IsForbidden(err) {
				t.Fatal("invalid fence cohort published", err)
			}
		})
	}

	if err := update(manager, func(n *cvk.NetworkController) {
		attachFences(n)
		n.Status.Configuration.MetadataHandoff.Fences = n.Status.Configuration.MetadataHandoff.Fences[:1]
	}, false); err != nil {
		t.Fatal("manager incremental acquisition publication denied", err)
	}
	t.Run("manager-cannot-ready-partial-cohort", func(t *testing.T) {
		err := update(manager, func(n *cvk.NetworkController) {
			at := metav1.Now()
			n.Status.Configuration.MetadataHandoff.ReadyAt = &at
		}, true)
		if !apierrors.IsInvalid(err) && !apierrors.IsForbidden(err) {
			t.Fatal("partial cohort marked ready", err)
		}
	})
	t.Run("manager-cannot-complete-fences-and-ready-together", func(t *testing.T) {
		deny(update(manager, func(n *cvk.NetworkController) {
			attachFences(n)
			at := metav1.Now()
			n.Status.Configuration.MetadataHandoff.ReadyAt = &at
		}, true))
	})
	t.Run("worker-cannot-submit-partial-fences", func(t *testing.T) { deny(update(worker, begin, true)) })
	t.Run("manager-cannot-rewrite-partial-original", func(t *testing.T) {
		err := update(manager, func(n *cvk.NetworkController) { n.Status.Configuration.MetadataHandoff.Fences[0].UID = "replacement" }, true)
		if !apierrors.IsInvalid(err) && !apierrors.IsForbidden(err) {
			t.Fatal("partial original UID rewrite accepted", err)
		}
	})

	if err := update(manager, attachFences, false); err != nil {
		t.Fatal("manager fence publication denied", err)
	}
	for _, mode := range []string{"uid", "name", "drop", "cohort"} {
		t.Run("fence-reference-immutable-"+mode, func(t *testing.T) {
			err := update(manager, func(n *cvk.NetworkController) {
				h := n.Status.Configuration.MetadataHandoff
				switch mode {
				case "uid":
					h.Fences[0].UID = "replacement"
				case "name":
					h.Fences[0].Name = "cvk-bootstrap-" + strings.Repeat("e", 32)
				case "drop":
					h.Fences = nil
				case "cohort":
					h.Fences[0].PhysicalIdentity = "other"
				}
			}, true)
			if !apierrors.IsInvalid(err) && !apierrors.IsForbidden(err) {
				t.Fatal("original fence reference change accepted", err)
			}
		})
	}
	ready := func(n *cvk.NetworkController) {
		at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
		n.Status.Configuration.MetadataHandoff.ReadyAt = &at
	}
	t.Run("worker-cannot-submit-before-manager-ready", func(t *testing.T) { deny(update(worker, begin, true)) })
	t.Run("worker-cannot-mark-ready", func(t *testing.T) { deny(update(worker, ready, true)) })
	if err := update(manager, ready, false); err != nil {
		t.Fatal("manager readiness denied", err)
	}
	t.Run("manager-readiness-immutable", func(t *testing.T) {
		err := update(manager, func(n *cvk.NetworkController) { n.Status.Configuration.MetadataHandoff.ReadyAt = nil }, true)
		if !apierrors.IsInvalid(err) && !apierrors.IsForbidden(err) {
			t.Fatal("readiness removal accepted", err)
		}
	})
	t.Run("manager-cannot-submit", func(t *testing.T) { deny(update(manager, begin, true)) })
	t.Run("old-pod-cannot-submit", func(t *testing.T) { deny(update(oldWorker, begin, true)) })
	t.Run("missing-pod-name", func(t *testing.T) { deny(update(missingPod, begin, true)) })
	t.Run("other-account-cannot-submit", func(t *testing.T) { deny(update(other, begin, true)) })
	t.Run("worker-target-substitution", func(t *testing.T) {
		deny(update(worker, func(n *cvk.NetworkController) { begin(n); n.Status.Configuration.Operation.TargetID = "other" }, true))
	})
	if err := update(worker, begin, false); err != nil {
		t.Fatal("bound worker state transition denied", err)
	}
	refusal := func(n *cvk.NetworkController) {
		h := n.Status.Configuration.MetadataHandoff
		h.RefusedSubmissionAt = n.Status.Configuration.Operation.SubmittedAt.DeepCopy()
		at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
		h.RefusedAt = &at
		n.Status.Configuration.Operation = nil
	}
	t.Run("manager-cannot-refuse", func(t *testing.T) { deny(update(manager, refusal, true)) })
	t.Run("old-pod-cannot-refuse", func(t *testing.T) { deny(update(oldWorker, refusal, true)) })
	t.Run("refusal-cannot-fabricate-effect", func(t *testing.T) {
		deny(update(worker, func(n *cvk.NetworkController) {
			refusal(n)
			at := metav1.Now()
			receipt := op
			receipt.Phase = "Verified"
			receipt.SubmittedAt = &at
			n.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: "inventory", Resource: "fabricated", Operation: receipt, VerifiedAt: &at}}
		}, true))
	})
	t.Run("worker-cannot-reset-preparation", func(t *testing.T) {
		deny(update(worker, func(n *cvk.NetworkController) { n.Status.Configuration.Operation = op.DeepCopy() }, true))
	})
	t.Run("manager-no-release-evidence", func(t *testing.T) {
		deny(update(manager, func(n *cvk.NetworkController) {
			at := metav1.Now()
			n.Status.Configuration.MetadataHandoff.ReleasedAt = &at
		}, true))
	})
	if err := update(worker, refusal, false); err != nil {
		t.Fatal("original bound refusal denied", err)
	}
	t.Run("worker-cannot-ack-release", func(t *testing.T) {
		deny(update(worker, func(n *cvk.NetworkController) {
			at := metav1.Now()
			n.Status.Configuration.MetadataHandoff.ReleasedAt = &at
		}, true))
	})
	settlement := func(n *cvk.NetworkController) {
		n.Status.Configuration.MetadataHandoff.SettlementDigest = strings.Repeat("e", 64)
	}
	t.Run("worker-cannot-publish-settlement", func(t *testing.T) { deny(update(worker, settlement, true)) })
	t.Run("manager-cannot-release-before-settlement", func(t *testing.T) {
		deny(update(manager, func(n *cvk.NetworkController) {
			at := metav1.Now()
			n.Status.Configuration.MetadataHandoff.ReleasedAt = &at
		}, true))
	})
	if err := update(manager, settlement, false); err != nil {
		t.Fatal("manager settlement intent denied", err)
	}
	if err := update(manager, func(n *cvk.NetworkController) {
		at := metav1.Now()
		n.Status.Configuration.MetadataHandoff.ReleasedAt = &at
	}, false); err != nil {
		t.Fatal("manager refusal release acknowledgement denied", err)
	}
	t.Run("worker-cannot-remove-released", func(t *testing.T) {
		deny(update(worker, func(n *cvk.NetworkController) { n.Status.Configuration.MetadataHandoff = nil }, true))
	})
	if err := manager.Delete(ctx, fetch(), client.DryRunAll); err != nil {
		t.Fatal("manager released deletion denied", err)
	}
	if err := update(manager, func(n *cvk.NetworkController) { n.Status.Configuration.MetadataHandoff = nil }, false); err != nil {
		t.Fatal("manager custody retirement denied", err)
	}

	newJournal := func(name string) {
		t.Helper()
		fresh := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Spec: nc.Spec}
		if err := admin.Create(ctx, fresh); err != nil {
			t.Fatal(err)
		}
		fresh.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: op.DeepCopy(), Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: op.ConfigName, ConfigUID: op.ConfigUID}}}
		if err := admin.Status().Update(ctx, fresh); err != nil {
			t.Fatal(err)
		}
		key = client.ObjectKeyFromObject(fresh)
		if err := update(manager, seal, false); err != nil {
			t.Fatal(err)
		}
		if err := update(manager, attachFences, false); err != nil {
			t.Fatal(err)
		}
		if err := update(manager, ready, false); err != nil {
			t.Fatal(err)
		}
		if err := update(worker, begin, false); err != nil {
			t.Fatal(err)
		}
	}
	newJournal("uncertain")
	if err := update(worker, func(n *cvk.NetworkController) { n.Status.Configuration.Operation.Phase = "Unknown" }, false); err != nil {
		t.Fatal(err)
	}
	t.Run("uncertainty-cannot-become-ack", func(t *testing.T) {
		deny(update(worker, func(n *cvk.NetworkController) { n.Status.Configuration.Operation.Phase = "Verifying" }, true))
	})
	t.Run("uncertainty-cannot-become-refusal", func(t *testing.T) { deny(update(worker, refusal, true)) })
	t.Run("manager-cannot-release-uncertainty", func(t *testing.T) {
		deny(update(manager, func(n *cvk.NetworkController) {
			at := metav1.Now()
			n.Status.Configuration.MetadataHandoff.ReleasedAt = &at
		}, true))
	})
	newJournal("acknowledged")
	if err := update(worker, func(n *cvk.NetworkController) { n.Status.Configuration.Operation.Phase = "Verifying" }, false); err != nil {
		t.Fatal(err)
	}
	t.Run("worker-cannot-rewrite-owner", func(t *testing.T) {
		deny(update(worker, func(n *cvk.NetworkController) { n.Status.Configuration.Owners[0].ConfigUID = "other" }, true))
	})
	if err := update(worker, func(n *cvk.NetworkController) {
		current := n.Status.Configuration.Operation
		current.Phase = "Verified"
		at := metav1.Now()
		n.Status.Configuration.ActionApplications = append(n.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: "pnp-removed:v1:native-pnp:" + strings.Repeat("b", 64), Operation: *current.DeepCopy(), VerifiedAt: &at})
	}, false); err != nil {
		t.Fatal("original acknowledged absence publication denied", err)
	}

	t.Run("worker-cannot-append-second-absence", func(t *testing.T) {
		deny(update(worker, func(n *cvk.NetworkController) {
			row := *n.Status.Configuration.ActionApplications[0].DeepCopy()
			row.Resource = "pnp-removed:v1:native-pnp:" + strings.Repeat("d", 64)
			n.Status.Configuration.ActionApplications = append(n.Status.Configuration.ActionApplications, row)
		}, true))
	})
	t.Run("worker-cannot-drop-verified-slot-before-release", func(t *testing.T) {
		deny(update(worker, func(n *cvk.NetworkController) { n.Status.Configuration.Operation = nil }, true))
	})
	if err := update(manager, settlement, false); err != nil {
		t.Fatal("manager verified settlement intent denied", err)
	}
	if err := update(manager, func(n *cvk.NetworkController) {
		at := metav1.Now()
		n.Status.Configuration.MetadataHandoff.ReleasedAt = &at
	}, false); err != nil {
		t.Fatal("manager verified-effect release denied", err)
	}
	if err := update(manager, func(n *cvk.NetworkController) { n.Status.Configuration.MetadataHandoff = nil }, false); err != nil {
		t.Fatal("manager verified-effect custody retirement denied", err)
	}
	if err := update(worker, func(n *cvk.NetworkController) { n.Status.Configuration.Operation = nil }, false); err != nil {
		t.Fatal("legacy slot settlement after custody retirement denied", err)
	}
	var policy admission.ValidatingAdmissionPolicy
	if err := admin.Get(ctx, client.ObjectKey{Name: policyName}, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Status.TypeChecking != nil && len(policy.Status.TypeChecking.ExpressionWarnings) > 0 {
		t.Fatal("actor policy warnings", policy.Status.TypeChecking.ExpressionWarnings)
	}
	if _, err := ReadMetadataHandoffProtection(ctx, admin, policyName, managerUser, "metadata-leases"); err == nil {
		t.Fatal("runtime accepted unobserved metadata compiler status")
	}
	digest, err := bootstrapProtectionDigest(&policy, managerUser, "metadata-leases")
	if err != nil || digest != metadataProtectionSpecDigest {
		t.Fatal("API-defaulted metadata policy differs from compiled contract", digest, err)
	}
	// Envtest has no background type-checking controller. Only its status
	// publication is simulated; policy Spec and all admission outcomes are real.
	policy.Status.ObservedGeneration = policy.Generation
	policy.Status.TypeChecking = &admission.TypeChecking{}
	if err := admin.Status().Update(ctx, &policy); err != nil {
		t.Fatal(err)
	}
	protection, err := ReadMetadataHandoffProtection(ctx, admin, policyName, managerUser, "metadata-leases")
	if err != nil || protection.PolicyUID == "" || protection.BindingUID == "" || protection.NamespaceUID == "" {
		t.Fatal("runtime metadata admission proof", err)
	}
	t.Log("runtime verifier corroborated exact live policy/binding/namespace; absent background compiler status simulated explicitly")
	legacy := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: ns}, Spec: nc.Spec}
	if err := admin.Create(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	legacy.Status.Phase = cvk.NetworkControllerPhaseConnecting
	if err := other.Status().Update(ctx, legacy); err != nil {
		t.Fatal("actor policy changed legacy no-handoff path", err)
	}

	t.Log("actual API manager seal, Pod-bound state/refusal and manager release/retirement passed; all caller clients had broad RBAC; no fences or native operations performed")
}
