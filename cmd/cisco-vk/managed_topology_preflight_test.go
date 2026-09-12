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
		AdmissionPrefix: prefix,
		ManagerUsername: envOrDefault("CVK_ADMISSION_MANAGER_USERNAME", "system:serviceaccount:cisco-vk-system:cisco-virtual-kubelet-controller"),
		PolicyNamespace: envOrDefault("CVK_ADMISSION_POLICY_NAMESPACE", "cisco-vk-system"),
		PolicyName:      envOrDefault("CVK_ADMISSION_POLICY_NAME", prefix+"-topology-policy"),
		LedgerName:      envOrDefault("CVK_ADMISSION_LEDGER_NAME", prefix+"-topology-ledger"),
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
		AdmissionPrefix: "prefix",
		ManagerUsername: "system:serviceaccount:system:manager",
		PolicyNamespace: "system",
		PolicyName:      "policy",
		LedgerName:      "ledger",
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
		AdmissionPrefix: "prefix",
		ManagerUsername: "system:serviceaccount:system:manager",
		PolicyNamespace: "system",
		PolicyName:      "policy",
		LedgerName:      "ledger",
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
	policyA.Spec.Validations[0].Expression = "'prefix-a' == 'prefix-a'"
	bindingsA := admissionContractBindings{
		AdmissionPrefix: "prefix-a", ManagerUsername: "manager",
		PolicyNamespace: "namespace", PolicyName: "policy", LedgerName: "ledger",
	}
	digestA, err := managedAdmissionPolicyDigest(policyA, bindingsA)
	if err != nil {
		t.Fatal(err)
	}
	policyB := policyA.DeepCopy()
	policyB.Spec.Validations[0].Expression = "'prefix-b' == 'prefix-b'"
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

func TestAdmissionContractBindingsRequireEveryIdentity(t *testing.T) {
	valid := admissionContractBindings{
		AdmissionPrefix: "prefix",
		ManagerUsername: "manager",
		PolicyNamespace: "namespace",
		PolicyName:      "policy",
		LedgerName:      "ledger",
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid bindings rejected: %v", err)
	}
	for _, field := range []string{"prefix", "manager", "namespace", "policy", "ledger"} {
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
			}
			if err := candidate.validate(); err == nil {
				t.Fatal("incomplete bindings were accepted")
			}
		})
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
