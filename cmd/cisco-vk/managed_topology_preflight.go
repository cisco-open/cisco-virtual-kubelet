// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

var managedTopologyCRDs = []schema.GroupVersionResource{
	opsv1alpha1.GroupVersion.WithResource("iosxesoftwarerollouts"),
}

var managedAdmissionPolicySuffixes = []string{
	"managed-node",
	"managed-pod-status",
	"managed-device",
	"managed-rollout",
	"managed-upgrade-leaf",
	"topology-policy",
	"topology-ledger",
	"managed-maintenance-lease",
}

type admissionContractExpectation struct {
	apiGroups         []string
	apiVersions       []string
	resources         []string
	operations        []admissionv1.OperationType
	scope             admissionv1.ScopeType
	matchConditions   []string
	variables         []string
	validations       int
	requiredFragments []string
	coreTyped         bool
	digest            string
}

var managedAdmissionExpectations = map[string]admissionContractExpectation{
	"managed-node": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"nodes", "nodes/status"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.ClusterScope,
		matchConditions: []string{"managed-node"}, variables: []string{"manager", "oldManaged", "managerLegacyHandoff", "legacyHandoffMarkerPreserved"}, validations: 3, coreTyped: true,
		requiredFragments: []string{"worker-username", "request.subResource == 'status'", "object.spec == oldObject.spec", "node-uid", "device-uid", "worker-protocol", "worker-observed-revision", "last-applied-node-status", "managerLegacyHandoff", "legacy-handoff", "projected-keys", "managed-taints"},
		digest:            "sha256:8e24dbd6f8dc833eba95e87096cf1daee03b41ff6180d591eaca526236ec7a19",
	},
	"managed-pod-status": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"pods/status"},
		operations: []admissionv1.OperationType{admissionv1.Update}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"generated-worker"}, variables: []string{"usernameParts", "workerServiceAccount"}, validations: 3, coreTyped: true,
		requiredFragments: []string{"cisco-vk-managed-", "cisco-vk-legacy-", "oldObject.spec.nodeName", "object.spec == oldObject.spec", "workerServiceAccount", "request.userInfo.username"},
		digest:            "sha256:0a2d27b4e3eb3c6051b1068173448a1b81920b97fc8a4f723b4d0ec01af9bea9",
	},
	"managed-device": {
		apiGroups: []string{"cisco.vk"}, apiVersions: []string{"v1alpha1"}, resources: []string{"ciscodevices", "ciscodevices/status"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		variables:         []string{"manager", "newProtectedLabels", "oldProtectedLabels", "newProtectedAnnotations", "oldProtectedAnnotations"},
		validations:       11,
		requiredFragments: []string{"check('topology')", "nodeIdentity", "topologyProjection", "topologyLock", "maintenanceSession", "distribution.cisco.vk/", "request-legacy-handoff", "isolated-legacy-worker", "legacyHandoff", "healthObservation", "workerRevision", "request.subResource", "object.spec == oldObject.spec", "object.spec.labels == oldObject.spec.labels", "object.spec.taints == oldObject.spec.taints", "object.spec.maxPods", "object.spec.maxPods <= 110", "ownerReferences", "finalizers", "oldObject.status.legacyHandoff.phase == 'Complete'"},
		digest:            "sha256:8918af1b52e891bf67c5d23dd53accbc3e4bf477e29921447c93d227d4a01511",
	},
	"managed-rollout": {
		apiGroups: []string{"ops.cisco.vk"}, apiVersions: []string{"v1alpha1"}, resources: []string{"iosxesoftwarerollouts", "iosxesoftwarerollouts/status"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		variables:         []string{"manager"},
		validations:       6,
		requiredFragments: []string{"requestedBy", "check('approve')", "planHash", "check('control')", "request.subResource != 'status'", "spec.control.revision == 0"},
		digest:            "sha256:d9dd48735236cddeb73385993cafc67da0cb5177cd9e2482c056b4472665e0f4",
	},
	"managed-upgrade-leaf": {
		apiGroups: []string{"ops.cisco.vk"}, apiVersions: []string{"v1alpha1"}, resources: []string{"iosxesoftwareupgrades", "iosxesoftwareupgrades/status"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions:   []string{"managed-leaf"},
		variables:         []string{"manager", "oldClaims", "newClaims"},
		validations:       7,
		requiredFragments: []string{"worker-username", "iosxesoftwareupgrade-cleanup", "managerAdmission", "managerControl", "managedMutationClaims", "primarySupervisorInstallRequested", "reservationID", "policyEpoch", "topologyLockID", "observedWorkerConfigRevision"},
		digest:            "sha256:3339c1f7cf33800047dcfe1d759cae5c99ea8b152f69466aae36c72af8254b6d",
	},
	"topology-policy": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"configmaps"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"chart-policy"}, variables: []string{"manager", "policyEditor"}, validations: 3, coreTyped: true,
		requiredFragments: []string{"managed-policy", "admission-policy-prefix", "check('topology')", "ledger-uid", "request.namespace", "request.name"},
		digest:            "sha256:c457d5c27b8839b1636a545b5348488eada7316bc1c95d0f2479101f91354c1e",
	},
	"topology-ledger": {
		apiGroups: []string{""}, apiVersions: []string{"v1"}, resources: []string{"configmaps"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"chart-ledger"}, variables: []string{"manager", "breakglass"}, validations: 3, coreTyped: true,
		requiredFragments: []string{"managed-ledger", "ledger.json", "check('manage-ledger')", "request.namespace", "request.name"},
		digest:            "sha256:3f585012df3c122804d1f300f242eaa365073d30fb7cbd0c5444474ba59faaaf",
	},
	"managed-maintenance-lease": {
		apiGroups: []string{"coordination.k8s.io"}, apiVersions: []string{"v1"}, resources: []string{"leases"},
		operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete}, scope: admissionv1.NamespacedScope,
		matchConditions: []string{"managed-maintenance-request"},
		variables:       []string{"manager", "oldRequest", "newRequest", "oldHeld", "newHeld", "oldTransitions", "managerCreate", "managerAdopt", "boundWorker", "holderChanged"},
		validations:     8, coreTyped: true,
		requiredFragments: []string{"maintenance-request-version", "maintenance-session-token", "maintenance-operation-uid", "maintenance-control-revision", "worker-username", "holderIdentity", "device-uid"},
		digest:            "sha256:3a33ace5e0020e020d28947d70b0221e95459aa286f72110675e4406cd99f864",
	},
}

// verifyManagedAdmissionContract refuses to start a managed-topology manager
// unless every native policy is compiled for its current generation and every
// exact binding enforces Deny. A missing policy must never degrade this
// privileged mode to ordinary RBAC alone.
func verifyManagedAdmissionContract(
	ctx context.Context,
	cfg *rest.Config,
	prefix string,
	policyKey types.NamespacedName,
	ledgerName string,
) error {
	if prefix == "" {
		return fmt.Errorf("managed admission policy prefix is empty")
	}
	if policyKey.Namespace == "" || policyKey.Name == "" || ledgerName == "" {
		return fmt.Errorf("managed admission policy bindings are incomplete")
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("create admission preflight client: %w", err)
	}
	self, err := clientset.AuthenticationV1().SelfSubjectReviews().Create(
		ctx,
		&authenticationv1.SelfSubjectReview{},
		metav1.CreateOptions{},
	)
	if err != nil {
		return fmt.Errorf("resolve authenticated manager identity: %w", err)
	}
	contractBindings := admissionContractBindings{
		AdmissionPrefix: prefix,
		ManagerUsername: self.Status.UserInfo.Username,
		PolicyNamespace: policyKey.Namespace,
		PolicyName:      policyKey.Name,
		LedgerName:      ledgerName,
	}
	if err := contractBindings.validate(); err != nil {
		return err
	}
	policies := clientset.AdmissionregistrationV1().ValidatingAdmissionPolicies()
	policyBindings := clientset.AdmissionregistrationV1().ValidatingAdmissionPolicyBindings()
	for _, suffix := range managedAdmissionPolicySuffixes {
		name := prefix + "-" + suffix
		policy, err := policies.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get required ValidatingAdmissionPolicy %q: %w", name, err)
		}
		expectation, ok := managedAdmissionExpectations[suffix]
		if !ok {
			return fmt.Errorf("no compiled admission expectation for %q", suffix)
		}
		if err := validateManagedAdmissionPolicy(policy, expectation); err != nil {
			return fmt.Errorf("ValidatingAdmissionPolicy %q: %w", name, err)
		}
		if err := validateManagedAdmissionPolicyDigest(policy, expectation, contractBindings); err != nil {
			return fmt.Errorf("ValidatingAdmissionPolicy %q: %w", name, err)
		}

		binding, err := policyBindings.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get required ValidatingAdmissionPolicyBinding %q: %w", name, err)
		}
		if err := validateManagedAdmissionBinding(binding, name); err != nil {
			return fmt.Errorf("ValidatingAdmissionPolicyBinding %q: %w", name, err)
		}
	}
	return nil
}

type admissionContractBindings struct {
	AdmissionPrefix string
	ManagerUsername string
	PolicyNamespace string
	PolicyName      string
	LedgerName      string
}

func (b admissionContractBindings) validate() error {
	for name, value := range map[string]string{
		"admission policy prefix":        b.AdmissionPrefix,
		"authenticated manager username": b.ManagerUsername,
		"policy namespace":               b.PolicyNamespace,
		"policy name":                    b.PolicyName,
		"ledger name":                    b.LedgerName,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("managed admission %s is empty", name)
		}
	}
	return nil
}

const (
	contractAdmissionPrefixToken = "${CVK_ADMISSION_PREFIX}"
	contractManagerToken         = "${CVK_MANAGER_USERNAME}"
	contractPolicyNamespaceToken = "${CVK_POLICY_NAMESPACE}"
	contractPolicyNameToken      = "${CVK_POLICY_NAME}"
	contractLedgerNameToken      = "${CVK_LEDGER_NAME}"
)

func validateManagedAdmissionPolicyDigest(
	policy *admissionv1.ValidatingAdmissionPolicy,
	expected admissionContractExpectation,
	bindings admissionContractBindings,
) error {
	if expected.digest == "" {
		return fmt.Errorf("compiled contract digest is absent")
	}
	actual, err := managedAdmissionPolicyDigest(policy, bindings)
	if err != nil {
		return err
	}
	if actual != expected.digest {
		return fmt.Errorf("compiled contract digest is %s, want %s", actual, expected.digest)
	}
	return nil
}

// managedAdmissionPolicyDigest canonicalizes only the chart-dependent literal
// identities, then hashes the entire typed policy Spec. Counts and substrings
// are useful diagnostics, but this digest is the fail-closed proof that a CEL
// expression, validation message, selector, rule, or other policy field was
// not narrowed or otherwise changed after the matching binary was built.
func managedAdmissionPolicyDigest(
	policy *admissionv1.ValidatingAdmissionPolicy,
	bindings admissionContractBindings,
) (string, error) {
	if policy == nil {
		return "", fmt.Errorf("compiled admission policy is nil")
	}
	if err := bindings.validate(); err != nil {
		return "", err
	}
	spec := policy.DeepCopy().Spec
	// API-server defaulting materializes omitted selectors as empty objects.
	// Empty and absent both match all resources; canonicalize only that semantic
	// no-op so the same chart contract hashes identically before and after a
	// real API round trip. Non-empty selectors remain part of the digest and are
	// rejected below as narrowing.
	if spec.MatchConstraints != nil {
		if labelSelectorEmpty(spec.MatchConstraints.NamespaceSelector) {
			spec.MatchConstraints.NamespaceSelector = nil
		}
		if labelSelectorEmpty(spec.MatchConstraints.ObjectSelector) {
			spec.MatchConstraints.ObjectSelector = nil
		}
	}
	replacements := []struct{ from, to string }{
		{bindings.AdmissionPrefix, contractAdmissionPrefixToken},
		{bindings.ManagerUsername, contractManagerToken},
		{bindings.PolicyNamespace, contractPolicyNamespaceToken},
		{bindings.PolicyName, contractPolicyNameToken},
		{bindings.LedgerName, contractLedgerNameToken},
	}
	sort.SliceStable(replacements, func(i, j int) bool {
		return len(replacements[i].from) > len(replacements[j].from)
	})
	normalize := func(expression string) string {
		for _, replacement := range replacements {
			expression = strings.ReplaceAll(expression, replacement.from, replacement.to)
		}
		return expression
	}
	for i := range spec.MatchConditions {
		spec.MatchConditions[i].Expression = normalize(spec.MatchConditions[i].Expression)
	}
	for i := range spec.Variables {
		spec.Variables[i].Expression = normalize(spec.Variables[i].Expression)
	}
	for i := range spec.Validations {
		spec.Validations[i].Expression = normalize(spec.Validations[i].Expression)
		spec.Validations[i].Message = normalize(spec.Validations[i].Message)
		spec.Validations[i].MessageExpression = normalize(spec.Validations[i].MessageExpression)
	}
	for i := range spec.AuditAnnotations {
		spec.AuditAnnotations[i].ValueExpression = normalize(spec.AuditAnnotations[i].ValueExpression)
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("encode compiled admission contract: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validateManagedAdmissionPolicy(policy *admissionv1.ValidatingAdmissionPolicy, expected admissionContractExpectation) error {
	if policy.Annotations[managedprotocol.AnnotationAdmissionContractVersion] != managedprotocol.AdmissionContractVersion {
		return fmt.Errorf("missing compiled admission contract version %q", managedprotocol.AdmissionContractVersion)
	}
	if policy.Spec.FailurePolicy == nil || *policy.Spec.FailurePolicy != admissionv1.Fail {
		return fmt.Errorf("failurePolicy must be Fail")
	}
	if policy.Spec.ParamKind != nil {
		return fmt.Errorf("paramKind must be absent")
	}
	if policy.Spec.MatchConstraints == nil || policy.Spec.MatchConstraints.MatchPolicy == nil ||
		*policy.Spec.MatchConstraints.MatchPolicy != admissionv1.Equivalent ||
		!labelSelectorEmpty(policy.Spec.MatchConstraints.NamespaceSelector) ||
		!labelSelectorEmpty(policy.Spec.MatchConstraints.ObjectSelector) ||
		len(policy.Spec.MatchConstraints.ExcludeResourceRules) != 0 || len(policy.Spec.MatchConstraints.ResourceRules) != 1 {
		return fmt.Errorf("match constraints differ from the compiled fail-closed contract")
	}
	rule := policy.Spec.MatchConstraints.ResourceRules[0]
	if len(rule.ResourceNames) != 0 || !sameStrings(rule.Rule.APIGroups, expected.apiGroups) || !sameStrings(rule.Rule.Resources, expected.resources) ||
		!sameOperations(rule.RuleWithOperations.Operations, expected.operations) || rule.Rule.Scope == nil || *rule.Rule.Scope != expected.scope ||
		!sameStrings(rule.Rule.APIVersions, expected.apiVersions) {
		return fmt.Errorf("resource rule differs from the compiled contract")
	}
	if !sameStrings(matchConditionNames(policy.Spec.MatchConditions), expected.matchConditions) {
		return fmt.Errorf("match conditions differ from the compiled contract")
	}
	if !sameStrings(variableNames(policy.Spec.Variables), expected.variables) {
		return fmt.Errorf("variables differ from the compiled contract")
	}
	if len(policy.Spec.Validations) != expected.validations {
		return fmt.Errorf("has %d validations, want exactly %d", len(policy.Spec.Validations), expected.validations)
	}
	var expressions strings.Builder
	for _, condition := range policy.Spec.MatchConditions {
		if expressionIsUnconditional(condition.Expression) {
			return fmt.Errorf("contains an unconditional match condition")
		}
		expressions.WriteString(condition.Expression)
		expressions.WriteByte('\n')
	}
	for _, variable := range policy.Spec.Variables {
		expressions.WriteString(variable.Expression)
		expressions.WriteByte('\n')
	}
	for _, validation := range policy.Spec.Validations {
		if expressionIsUnconditional(validation.Expression) {
			return fmt.Errorf("contains an unconditional validation")
		}
		expressions.WriteString(validation.Expression)
		expressions.WriteByte('\n')
	}
	contract := expressions.String()
	for _, fragment := range expected.requiredFragments {
		if !strings.Contains(contract, fragment) {
			return fmt.Errorf("compiled contract fragment %q is absent", fragment)
		}
	}
	if policy.Status.ObservedGeneration != policy.Generation {
		return fmt.Errorf("generation %d is not observed", policy.Generation)
	}
	// Kubernetes cannot type-check expressions against arbitrary CRD schemas.
	// Require warning-free completion only for built-in resources and cover the
	// CRD policies with real-apiserver denial tests.
	if expected.coreTyped {
		if policy.Status.TypeChecking == nil {
			return fmt.Errorf("type checking has not completed")
		}
		if len(policy.Status.TypeChecking.ExpressionWarnings) != 0 {
			return fmt.Errorf("has %d expression warning(s)", len(policy.Status.TypeChecking.ExpressionWarnings))
		}
	}
	return nil
}

func matchConditionNames(conditions []admissionv1.MatchCondition) []string {
	names := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		names = append(names, condition.Name)
	}
	return names
}

func variableNames(variables []admissionv1.Variable) []string {
	names := make([]string, 0, len(variables))
	for _, variable := range variables {
		names = append(names, variable.Name)
	}
	return names
}

func expressionIsUnconditional(expression string) bool {
	expression = strings.TrimSpace(strings.Trim(strings.TrimSpace(expression), "()"))
	return expression == "true" || expression == "false"
}

func validateManagedAdmissionBinding(binding *admissionv1.ValidatingAdmissionPolicyBinding, policyName string) error {
	if binding.Annotations[managedprotocol.AnnotationAdmissionContractVersion] != managedprotocol.AdmissionContractVersion {
		return fmt.Errorf("missing compiled admission contract version %q", managedprotocol.AdmissionContractVersion)
	}
	if binding.Spec.PolicyName != policyName || !reflect.DeepEqual(binding.Spec.ValidationActions, []admissionv1.ValidationAction{admissionv1.Deny}) ||
		binding.Spec.ParamRef != nil {
		return fmt.Errorf("must enforce only Deny for its exact unparameterized policy")
	}
	if resources := binding.Spec.MatchResources; resources != nil &&
		((resources.MatchPolicy != nil && *resources.MatchPolicy != admissionv1.Equivalent) ||
			!labelSelectorEmpty(resources.NamespaceSelector) || !labelSelectorEmpty(resources.ObjectSelector) ||
			len(resources.ResourceRules) != 0 || len(resources.ExcludeResourceRules) != 0) {
		return fmt.Errorf("must not narrow the policy match set")
	}
	return nil
}

func labelSelectorEmpty(selector *metav1.LabelSelector) bool {
	return selector == nil ||
		(len(selector.MatchLabels) == 0 && len(selector.MatchExpressions) == 0)
}

func sameStrings(left, right []string) bool {
	leftCopy, rightCopy := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	return reflect.DeepEqual(leftCopy, rightCopy)
}

func sameOperations(left, right []admissionv1.OperationType) bool {
	leftCopy, rightCopy := append([]admissionv1.OperationType(nil), left...), append([]admissionv1.OperationType(nil), right...)
	sort.Slice(leftCopy, func(i, j int) bool { return leftCopy[i] < leftCopy[j] })
	sort.Slice(rightCopy, func(i, j int) bool { return rightCopy[i] < rightCopy[j] })
	return reflect.DeepEqual(leftCopy, rightCopy)
}
