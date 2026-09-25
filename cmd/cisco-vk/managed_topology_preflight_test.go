// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	strictYAML "sigs.k8s.io/yaml"

	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

func TestRenderedManagedAdmissionContract(t *testing.T) {
	manifestPath := os.Getenv("CVK_ADMISSION_MANIFEST")
	if manifestPath == "" {
		t.Skip("CVK_ADMISSION_MANIFEST is not set")
	}

	prefix := envOrDefault("CVK_ADMISSION_PREFIX", "cvk-cisco-virtual-kubelet")
	bindings := admissionContractBindings{
		AdmissionPrefix:                 prefix,
		ManagerUsername:                 envOrDefault("CVK_ADMISSION_MANAGER_USERNAME", "system:serviceaccount:cisco-vk-system:cisco-virtual-kubelet-controller"),
		PolicyNamespace:                 envOrDefault("CVK_ADMISSION_POLICY_NAMESPACE", "cisco-vk-system"),
		PolicyName:                      envOrDefault("CVK_ADMISSION_POLICY_NAME", prefix+"-topology-policy"),
		LedgerName:                      envOrDefault("CVK_ADMISSION_LEDGER_NAME", prefix+"-topology-ledger"),
		AppHostingServiceAccount:        envOrDefault("CVK_ADMISSION_APP_SERVICE_ACCOUNT", prefix+"-app-hosting"),
		NetworkManagementServiceAccount: envOrDefault("CVK_ADMISSION_NETWORK_SERVICE_ACCOUNT", prefix+"-network-management"),
	}
	assertStrictYAMLDocuments(t, manifestPath)

	file, err := os.Open(filepath.Clean(manifestPath))
	if err != nil {
		t.Fatalf("open rendered admission manifest: %v", err)
	}
	defer file.Close()

	seen := make(map[string]bool, len(managedAdmissionPolicySuffixes))
	decoder := yaml.NewYAMLOrJSONDecoder(file, 4096)
	for {
		var document map[string]any
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode rendered admission manifest: %v", err)
		}
		if document["kind"] != "ValidatingAdmissionPolicy" {
			continue
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("encode rendered policy: %v", err)
		}
		var policy admissionv1.ValidatingAdmissionPolicy
		if err := json.Unmarshal(encoded, &policy); err != nil {
			t.Fatalf("decode rendered policy: %v", err)
		}
		suffix := strings.TrimPrefix(policy.Name, prefix+"-")
		expected, managed := managedAdmissionExpectations[suffix]
		if !managed {
			continue
		}
		seen[suffix] = true
		if err := validateRenderedManagedAdmissionPolicy(&policy, expected); err != nil {
			t.Errorf("ValidatingAdmissionPolicy %q: %v", policy.Name, err)
		}
		if err := validateManagedAdmissionPolicyDigest(&policy, expected, bindings); err != nil {
			actual, digestErr := managedAdmissionPolicyDigest(&policy, bindings)
			if digestErr != nil {
				t.Errorf("ValidatingAdmissionPolicy %q digest: %v", policy.Name, digestErr)
				continue
			}
			t.Errorf("ValidatingAdmissionPolicy %q digest: %v (rendered %s)", policy.Name, err, actual)
		}
	}
	for _, suffix := range managedAdmissionPolicySuffixes {
		if !seen[suffix] {
			t.Errorf("required ValidatingAdmissionPolicy %q is absent", prefix+"-"+suffix)
		}
	}
}

// assertStrictYAMLDocuments catches duplicate mapping keys before the typed
// admission checks. Helm can render such YAML successfully even though a
// downstream decoder may silently keep only one value.
func assertStrictYAMLDocuments(t *testing.T, manifestPath string) {
	t.Helper()
	file, err := os.Open(filepath.Clean(manifestPath))
	if err != nil {
		t.Fatalf("open rendered admission manifest for strict YAML validation: %v", err)
	}
	defer file.Close()

	reader := yaml.NewYAMLReader(bufio.NewReader(file))
	for documentNumber := 1; ; documentNumber++ {
		raw, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatalf("read rendered admission document %d: %v", documentNumber, err)
		}
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var document any
		if err := strictYAML.UnmarshalStrict(raw, &document); err != nil {
			t.Fatalf("strictly decode rendered admission document %d: %v", documentNumber, err)
		}
	}
}

// Helm output has no admission status. Exercise every static invariant while
// leaving observedGeneration and API-server type checking to the kind test and
// manager startup preflight.
func validateRenderedManagedAdmissionPolicy(policy *admissionv1.ValidatingAdmissionPolicy, expected admissionContractExpectation) error {
	policy = policy.DeepCopy()
	policy.Generation = 1
	policy.Status.ObservedGeneration = 1
	if expected.coreTyped {
		policy.Status.TypeChecking = &admissionv1.TypeChecking{}
	}
	return validateManagedAdmissionPolicy(policy, expected)
}

func TestManagedAdmissionPolicyDigestDetectsExpressionChange(t *testing.T) {
	expected := managedAdmissionExpectations["managed-node"]
	policy := validManagedAdmissionPolicy(expected)
	bindings := admissionContractBindings{
		AdmissionPrefix:                 "prefix",
		ManagerUsername:                 "system:serviceaccount:system:manager",
		PolicyNamespace:                 "system",
		PolicyName:                      "policy",
		LedgerName:                      "ledger",
		AppHostingServiceAccount:        "app-worker",
		NetworkManagementServiceAccount: "network-worker",
	}
	digest, err := managedAdmissionPolicyDigest(policy, bindings)
	if err != nil {
		t.Fatalf("digest valid policy: %v", err)
	}
	expected.digest = digest
	if err := validateManagedAdmissionPolicyDigest(policy, expected, bindings); err != nil {
		t.Fatalf("valid policy digest rejected: %v", err)
	}

	policy.Spec.Validations[0].Expression += " && request.operation != 'DELETE'"
	if err := validateManagedAdmissionPolicyDigest(policy, expected, bindings); err == nil || !strings.Contains(err.Error(), "compiled contract digest") {
		t.Fatalf("changed expression error = %v, want digest mismatch", err)
	}
}

func TestManagedAdmissionPolicyDigestNormalizesAPIServerEmptySelectors(t *testing.T) {
	expected := managedAdmissionExpectations["managed-node"]
	policy := validManagedAdmissionPolicy(expected)
	bindings := admissionContractBindings{
		AdmissionPrefix:                 "prefix",
		ManagerUsername:                 "system:serviceaccount:system:manager",
		PolicyNamespace:                 "system",
		PolicyName:                      "policy",
		LedgerName:                      "ledger",
		AppHostingServiceAccount:        "app-worker",
		NetworkManagementServiceAccount: "network-worker",
	}
	before, err := managedAdmissionPolicyDigest(policy, bindings)
	if err != nil {
		t.Fatal(err)
	}
	policy.Spec.MatchConstraints.NamespaceSelector = &metav1.LabelSelector{}
	policy.Spec.MatchConstraints.ObjectSelector = &metav1.LabelSelector{}
	after, err := managedAdmissionPolicyDigest(policy, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("API-server empty selectors changed contract digest: %s != %s", before, after)
	}
	if err := validateManagedAdmissionPolicy(policy, expected); err != nil {
		t.Fatalf("API-server empty selectors rejected: %v", err)
	}
}

func TestManagedAdmissionPolicyDigestNormalizesAdmissionPrefix(t *testing.T) {
	expected := managedAdmissionExpectations["topology-policy"]
	policyA := validManagedAdmissionPolicy(expected)
	policyA.Spec.Validations[0].Expression = `"prefix-a" == "prefix-a"`
	bindingsA := admissionContractBindings{
		AdmissionPrefix: "prefix-a", ManagerUsername: "manager",
		PolicyNamespace: "namespace", PolicyName: "policy", LedgerName: "ledger",
		AppHostingServiceAccount: "app-worker", NetworkManagementServiceAccount: "network-worker",
	}
	digestA, err := managedAdmissionPolicyDigest(policyA, bindingsA)
	if err != nil {
		t.Fatal(err)
	}
	policyB := policyA.DeepCopy()
	policyB.Spec.Validations[0].Expression = `"prefix-b" == "prefix-b"`
	bindingsB := bindingsA
	bindingsB.AdmissionPrefix = "prefix-b"
	digestB, err := managedAdmissionPolicyDigest(policyB, bindingsB)
	if err != nil {
		t.Fatal(err)
	}
	if digestA != digestB {
		t.Fatalf("release-specific admission prefix changed contract digest: %s != %s", digestA, digestB)
	}
}

func TestManagedAdmissionPolicyDigestNormalizesOnlyExactBoundCELLiterals(t *testing.T) {
	expected := managedAdmissionExpectations["shared-worker-serviceaccount"]
	policyA := validManagedAdmissionPolicy(expected)
	policyA.Spec.Validations[0].Expression = `
		object.metadata.annotations['topology.cisco.vk/managed'] == 'managed' &&
		request.name in ["managed", "network"] &&
		request.userInfo.username.endsWith(':managed') &&
		object.metadata.annotations['network'] == 'network'`
	policyA.Spec.Validations[0].Message = "the manager's managed policy remains fixed"
	bindingsA := admissionContractBindings{
		AdmissionPrefix: "prefix", ManagerUsername: "system:serviceaccount:system:manager",
		PolicyNamespace: "system", PolicyName: "policy", LedgerName: "ledger",
		AppHostingServiceAccount: "managed", NetworkManagementServiceAccount: "network",
	}
	digestA, err := managedAdmissionPolicyDigest(policyA, bindingsA)
	if err != nil {
		t.Fatal(err)
	}

	policyB := policyA.DeepCopy()
	policyB.Spec.Validations[0].Expression = `
		object.metadata.annotations['topology.cisco.vk/managed'] == 'managed' &&
		request.name in ["app-worker", "network-worker"] &&
		request.userInfo.username.endsWith(':app-worker') &&
		object.metadata.annotations['network'] == 'network'`
	bindingsB := bindingsA
	bindingsB.AppHostingServiceAccount = "app-worker"
	bindingsB.NetworkManagementServiceAccount = "network-worker"
	digestB, err := managedAdmissionPolicyDigest(policyB, bindingsB)
	if err != nil {
		t.Fatal(err)
	}
	if digestA != digestB {
		t.Fatalf("equivalent short worker identities changed contract digest: %s != %s", digestA, digestB)
	}

	messageChanged := policyB.DeepCopy()
	messageChanged.Spec.Validations[0].Message = "changed validation prose"
	changed, err := managedAdmissionPolicyDigest(messageChanged, bindingsB)
	if err != nil {
		t.Fatal(err)
	}
	if changed == digestA {
		t.Fatal("change to fixed validation prose was normalized away")
	}

	policyB.Spec.Validations[0].Expression = strings.Replace(
		policyB.Spec.Validations[0].Expression,
		"object.metadata.annotations['network'] == 'network'",
		"object.metadata.annotations['network'] == 'changed'", 1,
	)
	changed, err = managedAdmissionPolicyDigest(policyB, bindingsB)
	if err != nil {
		t.Fatal(err)
	}
	if changed == digestA {
		t.Fatal("change to fixed single-quoted contract vocabulary was normalized away")
	}
}

func TestAdmissionContractBindingsRequireEveryIdentity(t *testing.T) {
	valid := admissionContractBindings{
		AdmissionPrefix:                 "prefix",
		ManagerUsername:                 "manager",
		PolicyNamespace:                 "namespace",
		PolicyName:                      "policy",
		LedgerName:                      "ledger",
		AppHostingServiceAccount:        "app-worker",
		NetworkManagementServiceAccount: "network-worker",
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid bindings rejected: %v", err)
	}
	for _, field := range []string{"prefix", "manager", "namespace", "policy", "ledger", "app", "network"} {
		t.Run(field, func(t *testing.T) {
			candidate := valid
			switch field {
			case "prefix":
				candidate.AdmissionPrefix = ""
			case "manager":
				candidate.ManagerUsername = ""
			case "namespace":
				candidate.PolicyNamespace = ""
			case "policy":
				candidate.PolicyName = ""
			case "ledger":
				candidate.LedgerName = ""
			case "app":
				candidate.AppHostingServiceAccount = ""
			case "network":
				candidate.NetworkManagementServiceAccount = ""
			}
			if err := candidate.validate(); err == nil {
				t.Fatal("incomplete bindings were accepted")
			}
		})
	}
	collision := valid
	collision.AppHostingServiceAccount = collision.PolicyNamespace
	if err := collision.validate(); err == nil ||
		!strings.Contains(err.Error(), "must be pairwise distinct") ||
		!strings.Contains(err.Error(), "app-hosting ServiceAccount") ||
		!strings.Contains(err.Error(), "policy namespace") {
		t.Fatalf("binding collision error = %v, want both colliding fields", err)
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func TestValidateManagedAdmissionPolicyRejectsNarrowedContract(t *testing.T) {
	expected := managedAdmissionExpectations["managed-node"]
	if err := validateManagedAdmissionPolicy(validManagedAdmissionPolicy(expected), expected); err != nil {
		t.Fatalf("valid compiled policy rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*admissionv1.ValidatingAdmissionPolicy)
		want   string
	}{
		{name: "parameterized", mutate: func(p *admissionv1.ValidatingAdmissionPolicy) {
			p.Spec.ParamKind = &admissionv1.ParamKind{APIVersion: "v1", Kind: "ConfigMap"}
		}, want: "paramKind"},
		{name: "named resource", mutate: func(p *admissionv1.ValidatingAdmissionPolicy) {
			p.Spec.MatchConstraints.ResourceRules[0].ResourceNames = []string{"one-node"}
		}, want: "resource rule"},
		{name: "extra match condition", mutate: func(p *admissionv1.ValidatingAdmissionPolicy) {
			p.Spec.MatchConditions = append(p.Spec.MatchConditions, admissionv1.MatchCondition{Name: "bypass", Expression: "false"})
		}, want: "match conditions"},
		{name: "renamed variable", mutate: func(p *admissionv1.ValidatingAdmissionPolicy) {
			p.Spec.Variables[0].Name = "other"
		}, want: "variables"},
		{name: "extra validation", mutate: func(p *admissionv1.ValidatingAdmissionPolicy) {
			p.Spec.Validations = append(p.Spec.Validations, admissionv1.Validation{Expression: "object != null"})
		}, want: "exactly"},
		{name: "unconditional match", mutate: func(p *admissionv1.ValidatingAdmissionPolicy) {
			p.Spec.MatchConditions[0].Expression = "( false )"
		}, want: "unconditional match"},
		{name: "unconditional validation", mutate: func(p *admissionv1.ValidatingAdmissionPolicy) {
			p.Spec.Validations[0].Expression = "true"
		}, want: "unconditional validation"},
		{name: "stale generation", mutate: func(p *admissionv1.ValidatingAdmissionPolicy) {
			p.Status.ObservedGeneration--
		}, want: "not observed"},
		{name: "type warning", mutate: func(p *admissionv1.ValidatingAdmissionPolicy) {
			p.Status.TypeChecking.ExpressionWarnings = []admissionv1.ExpressionWarning{{FieldRef: "spec.validations[0].expression", Warning: "test warning"}}
		}, want: "expression warning"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := validManagedAdmissionPolicy(expected)
			tc.mutate(policy)
			if err := validateManagedAdmissionPolicy(policy, expected); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestValidateManagedAdmissionBindingRejectsNarrowing(t *testing.T) {
	valid := func() *admissionv1.ValidatingAdmissionPolicyBinding {
		return &admissionv1.ValidatingAdmissionPolicyBinding{
			ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
				managedprotocol.AnnotationAdmissionContractVersion: managedprotocol.AdmissionContractVersion,
			}},
			Spec: admissionv1.ValidatingAdmissionPolicyBindingSpec{
				PolicyName: "cvk-managed-node", ValidationActions: []admissionv1.ValidationAction{admissionv1.Deny},
				MatchResources: &admissionv1.MatchResources{},
			},
		}
	}
	if err := validateManagedAdmissionBinding(valid(), "cvk-managed-node"); err != nil {
		t.Fatalf("valid compiled binding rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*admissionv1.ValidatingAdmissionPolicyBinding)
	}{
		{name: "warn action", mutate: func(b *admissionv1.ValidatingAdmissionPolicyBinding) {
			b.Spec.ValidationActions = []admissionv1.ValidationAction{admissionv1.Warn}
		}},
		{name: "parameter", mutate: func(b *admissionv1.ValidatingAdmissionPolicyBinding) {
			b.Spec.ParamRef = &admissionv1.ParamRef{Name: "parameters"}
		}},
		{name: "object selector", mutate: func(b *admissionv1.ValidatingAdmissionPolicyBinding) {
			b.Spec.MatchResources.ObjectSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"narrow": "true"}}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			binding := valid()
			tc.mutate(binding)
			if err := validateManagedAdmissionBinding(binding, "cvk-managed-node"); err == nil {
				t.Fatal("narrowed admission binding was accepted")
			}
		})
	}

	defaulted := valid()
	matchPolicy := admissionv1.Equivalent
	defaulted.Spec.MatchResources.MatchPolicy = &matchPolicy
	defaulted.Spec.MatchResources.NamespaceSelector = &metav1.LabelSelector{}
	defaulted.Spec.MatchResources.ObjectSelector = &metav1.LabelSelector{}
	if err := validateManagedAdmissionBinding(defaulted, "cvk-managed-node"); err != nil {
		t.Fatalf("API-server semantic defaults rejected: %v", err)
	}
}

func validWorkerServiceAccountAdmissionGenerations() []workerServiceAccountAdmissionGeneration {
	generations := make([]workerServiceAccountAdmissionGeneration, 0,
		len(workerServiceAccountPolicyEpochSuffixes))
	for _, suffix := range workerServiceAccountPolicyEpochSuffixes {
		generations = append(generations, workerServiceAccountAdmissionGeneration{
			suffix: suffix, policyUID: types.UID(suffix + "-policy-uid"), policyGeneration: 1,
			policySpecDigest: "sha256:" + suffix + "-policy-spec",
			bindingUID:       types.UID(suffix + "-binding-uid"), bindingGeneration: 1,
			bindingSpecDigest: "sha256:" + suffix + "-binding-spec",
		})
	}
	return generations
}

func TestDeriveWorkerServiceAccountPolicyEpochIsStableAndOrderIndependent(t *testing.T) {
	binding := &admissionv1.ValidatingAdmissionPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cvk-shared-worker-serviceaccount", UID: "shared-binding-uid", Generation: 3,
			ResourceVersion: "11", Labels: map[string]string{"chart": "old"},
		},
		Spec: admissionv1.ValidatingAdmissionPolicyBindingSpec{
			PolicyName:        "cvk-shared-worker-serviceaccount",
			ValidationActions: []admissionv1.ValidationAction{admissionv1.Deny},
		},
	}
	bindingDigest, err := managedAdmissionBindingSpecDigest(binding)
	if err != nil {
		t.Fatal(err)
	}
	metadataOnly := binding.DeepCopy()
	metadataOnly.ResourceVersion = "999"
	metadataOnly.Labels = map[string]string{"chart": "new"}
	metadataOnly.Annotations = map[string]string{"unrelated": "metadata"}
	metadataDigest, err := managedAdmissionBindingSpecDigest(metadataOnly)
	if err != nil {
		t.Fatal(err)
	}
	if metadataDigest != bindingDigest {
		t.Fatalf("metadata-only binding update changed Spec digest: %q != %q", metadataDigest, bindingDigest)
	}
	specChanged := binding.DeepCopy()
	specChanged.Spec.PolicyName = "cvk-generated-worker-serviceaccount"
	specDigest, err := managedAdmissionBindingSpecDigest(specChanged)
	if err != nil {
		t.Fatal(err)
	}
	if specDigest == bindingDigest {
		t.Fatal("binding Spec change did not change compiled digest")
	}

	generations := validWorkerServiceAccountAdmissionGenerations()
	for i := range generations {
		if generations[i].suffix == "shared-worker-serviceaccount" {
			generations[i].policyGeneration = 2
			generations[i].bindingUID = binding.UID
			generations[i].bindingGeneration = binding.Generation
			generations[i].bindingSpecDigest = bindingDigest
		}
	}
	want, err := deriveWorkerServiceAccountPolicyEpoch(generations)
	if err != nil {
		t.Fatal(err)
	}
	reversed := make([]workerServiceAccountAdmissionGeneration, len(generations))
	for i := range generations {
		reversed[len(generations)-1-i] = generations[i]
	}
	got, err := deriveWorkerServiceAccountPolicyEpoch(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("epoch depends on policy iteration order: %q != %q", got, want)
	}
	metadataGenerations := append([]workerServiceAccountAdmissionGeneration(nil), generations...)
	metadataGenerations[0].bindingSpecDigest = metadataDigest
	got, err = deriveWorkerServiceAccountPolicyEpoch(metadataGenerations)
	if err != nil || got != want {
		t.Fatalf("metadata-only update rotated epoch: got=%q want=%q err=%v", got, want, err)
	}
}

func TestDeriveWorkerServiceAccountPolicyEpochRotatesOnContractIdentityChange(t *testing.T) {
	base := validWorkerServiceAccountAdmissionGenerations()
	want, err := deriveWorkerServiceAccountPolicyEpoch(base)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*workerServiceAccountAdmissionGeneration)
	}{
		{"policy UID", func(g *workerServiceAccountAdmissionGeneration) { g.policyUID = "new-policy" }},
		{"policy generation", func(g *workerServiceAccountAdmissionGeneration) { g.policyGeneration++ }},
		{"policy Spec digest", func(g *workerServiceAccountAdmissionGeneration) { g.policySpecDigest = "sha256:new-policy" }},
		{"binding UID", func(g *workerServiceAccountAdmissionGeneration) { g.bindingUID = "new-binding" }},
		{"binding generation", func(g *workerServiceAccountAdmissionGeneration) { g.bindingGeneration++ }},
		{"binding Spec digest", func(g *workerServiceAccountAdmissionGeneration) { g.bindingSpecDigest = "sha256:new-binding" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := append([]workerServiceAccountAdmissionGeneration(nil), base...)
			test.mutate(&changed[0])
			got, err := deriveWorkerServiceAccountPolicyEpoch(changed)
			if err != nil {
				t.Fatal(err)
			}
			if got == want {
				t.Fatalf("%s did not rotate worker ServiceAccount epoch", test.name)
			}
		})
	}
	for _, suffix := range []string{"shared-worker-token", "shared-worker-token-secret", "shared-worker-deployment", "shared-worker-pod-update"} {
		t.Run(suffix+" contract", func(t *testing.T) {
			changed := append([]workerServiceAccountAdmissionGeneration(nil), base...)
			for i := range changed {
				if changed[i].suffix == suffix {
					changed[i].policySpecDigest = "sha256:changed-" + suffix
				}
			}
			got, err := deriveWorkerServiceAccountPolicyEpoch(changed)
			if err != nil {
				t.Fatal(err)
			}
			if got == want {
				t.Fatalf("%s policy contract change did not rotate worker ServiceAccount epoch", suffix)
			}
		})
	}
}

func TestDeriveWorkerServiceAccountPolicyEpochRejectsIncompleteIdentity(t *testing.T) {
	valid := validWorkerServiceAccountAdmissionGenerations()
	if _, err := deriveWorkerServiceAccountPolicyEpoch(valid[:len(valid)-1]); err == nil {
		t.Fatal("missing worker policy/binding generation was accepted")
	}
	duplicate := append([]workerServiceAccountAdmissionGeneration(nil), valid...)
	duplicate[1].suffix = duplicate[0].suffix
	if _, err := deriveWorkerServiceAccountPolicyEpoch(duplicate); err == nil {
		t.Fatal("duplicate policy/binding generation was accepted")
	}
	tests := []struct {
		name   string
		mutate func(*workerServiceAccountAdmissionGeneration)
	}{
		{"policy UID", func(g *workerServiceAccountAdmissionGeneration) { g.policyUID = "" }},
		{"policy generation", func(g *workerServiceAccountAdmissionGeneration) { g.policyGeneration = 0 }},
		{"policy digest", func(g *workerServiceAccountAdmissionGeneration) { g.policySpecDigest = "" }},
		{"binding UID", func(g *workerServiceAccountAdmissionGeneration) { g.bindingUID = "" }},
		{"binding generation", func(g *workerServiceAccountAdmissionGeneration) { g.bindingGeneration = 0 }},
		{"binding digest", func(g *workerServiceAccountAdmissionGeneration) { g.bindingSpecDigest = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			broken := append([]workerServiceAccountAdmissionGeneration(nil), valid...)
			test.mutate(&broken[0])
			if _, err := deriveWorkerServiceAccountPolicyEpoch(broken); err == nil {
				t.Fatalf("empty/zero %s was accepted", test.name)
			}
		})
	}
	if _, err := managedAdmissionBindingSpecDigest(nil); err == nil {
		t.Fatal("nil admission binding Spec was accepted")
	}
}

func validManagedAdmissionPolicy(expected admissionContractExpectation) *admissionv1.ValidatingAdmissionPolicy {
	failurePolicy := admissionv1.Fail
	matchPolicy := admissionv1.Equivalent
	scope := expected.scope
	policy := &admissionv1.ValidatingAdmissionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Generation:  7,
			Annotations: map[string]string{managedprotocol.AnnotationAdmissionContractVersion: managedprotocol.AdmissionContractVersion},
		},
		Spec: admissionv1.ValidatingAdmissionPolicySpec{
			FailurePolicy: &failurePolicy,
			MatchConstraints: &admissionv1.MatchResources{
				MatchPolicy: &matchPolicy,
				ResourceRules: []admissionv1.NamedRuleWithOperations{{RuleWithOperations: admissionv1.RuleWithOperations{
					Operations: expected.operations,
					Rule:       admissionv1.Rule{APIGroups: expected.apiGroups, APIVersions: expected.apiVersions, Resources: expected.resources, Scope: &scope},
				}}},
			},
		},
		Status: admissionv1.ValidatingAdmissionPolicyStatus{ObservedGeneration: 7, TypeChecking: &admissionv1.TypeChecking{}},
	}
	for _, name := range expected.matchConditions {
		policy.Spec.MatchConditions = append(policy.Spec.MatchConditions, admissionv1.MatchCondition{Name: name, Expression: "request.operation != 'CONNECT'"})
	}
	for _, name := range expected.variables {
		policy.Spec.Variables = append(policy.Spec.Variables, admissionv1.Variable{Name: name, Expression: "request.operation != 'CONNECT'"})
	}
	for i := 0; i < expected.validations; i++ {
		expression := "object != null"
		if i == 0 {
			expression += " /* " + strings.Join(expected.requiredFragments, " ") + " */"
		}
		policy.Spec.Validations = append(policy.Spec.Validations, admissionv1.Validation{Expression: expression})
	}
	return policy
}
