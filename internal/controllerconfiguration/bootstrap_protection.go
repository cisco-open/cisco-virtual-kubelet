// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"

	admission "k8s.io/api/admissionregistration/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const bootstrapProtectionAnnotation = "ops.cisco.vk/bootstrap-fence-protocol"

// Hash of the entire typed chart policy Spec, normalizing only its two quoted
// installation identity literals and API-defaulted empty selectors. Tests
// compare both rendered and real-API-server Specs with this compiled value.
const bootstrapProtectionSpecDigest = "9b328495a0b9197a7c13fe1e63228644261fe6024049249ae8c9701afa02b5b9"

// Historical record reconstruction only. Live policy inspection still requires
// the current exact compiled Spec before any new reservation is written.
const bootstrapLegacyProtectionSpecDigest = "d7239261d66c95e289aa623e5fe0b9ee1516ec7d90f26d2eb9078af2eedc11a4"

// BootstrapFenceInstallation selects the manager-owned shared protection
// contract. A tenant namespace is never used as an implicit Lease fallback.
type BootstrapFenceInstallation struct {
	PolicyName      string `json:"policyName"`
	ManagerUsername string `json:"managerUsername"`
	LeaseNamespace  string `json:"leaseNamespace"`
}

// AdmissionProtection contains observed installation identities, not
// permission to create a reservation or claim a device. Enrollment/rollout
// exclusion and actor authority must also be established by the runtime check.
type AdmissionProtection struct {
	PolicyUID, BindingUID, NamespaceUID types.UID
	PolicyGeneration, BindingGeneration int64
	PolicyDigest                        string
}

// BootstrapFenceProtection preserves the existing read-only bootstrap evidence
// contract while sharing the policy observation implementation.
type BootstrapFenceProtection = AdmissionProtection

// ReadBootstrapFenceProtection requires the manager's uncached reader. It
// verifies the complete policy and binding, compiler observation and a live
// shared namespace, then re-reads all three objects to reject replacements or
// changes during observation. It performs no writes and is not a runtime grant.
func ReadBootstrapFenceProtection(ctx context.Context, reader client.Reader, policyName, managerUsername, leaseNamespace string) (*BootstrapFenceProtection, error) {
	return readRetainedAdmissionProtection(ctx, reader, policyName, managerUsername, leaseNamespace, bootstrapProtectionAnnotation, BootstrapFenceProtocol, bootstrapProtectionSpecDigest)
}

func readRetainedAdmissionProtection(ctx context.Context, reader client.Reader, policyName, managerUsername, leaseNamespace, annotation, protocol, expectedDigest string) (*AdmissionProtection, error) {
	reject := errors.New("retained admission protection is unavailable or changed")
	if reader == nil || len(validation.IsDNS1123Subdomain(policyName)) != 0 || !validBootstrapProtectionIdentities(managerUsername, leaseNamespace) {
		return nil, reject
	}
	var policy admission.ValidatingAdmissionPolicy
	var binding admission.ValidatingAdmissionPolicyBinding
	var namespace core.Namespace
	if err := reader.Get(ctx, client.ObjectKey{Name: policyName}, &policy); err != nil {
		return nil, err
	}
	if err := reader.Get(ctx, client.ObjectKey{Name: policyName}, &binding); err != nil {
		return nil, err
	}
	if err := reader.Get(ctx, client.ObjectKey{Name: leaseNamespace}, &namespace); err != nil {
		return nil, err
	}
	if !bootstrapProtectionObjectCurrent(&policy) || !bootstrapProtectionObjectCurrent(&binding) || namespace.UID == "" || namespace.ResourceVersion == "" || namespace.DeletionTimestamp != nil || namespace.Status.Phase != core.NamespaceActive ||
		policy.Annotations[annotation] != protocol || binding.Annotations[annotation] != protocol ||
		policy.Status.ObservedGeneration != policy.Generation || policy.Status.TypeChecking == nil || len(policy.Status.TypeChecking.ExpressionWarnings) != 0 {
		return nil, reject
	}
	digest, err := bootstrapProtectionDigest(&policy, managerUsername, leaseNamespace)
	if err != nil {
		return nil, err
	}
	if digest != expectedDigest {
		return nil, errors.New("admission policy differs from compiled protection contract")
	}
	expectedBinding := admission.ValidatingAdmissionPolicyBindingSpec{PolicyName: policyName, ValidationActions: []admission.ValidationAction{admission.Deny}}
	actualBinding := binding.DeepCopy().Spec
	if actualBinding.MatchResources != nil {
		resources := actualBinding.MatchResources
		if resources.MatchPolicy != nil && *resources.MatchPolicy == admission.Equivalent {
			resources.MatchPolicy = nil
		}
		if emptyBootstrapProtectionSelector(resources.NamespaceSelector) {
			resources.NamespaceSelector = nil
		}
		if emptyBootstrapProtectionSelector(resources.ObjectSelector) {
			resources.ObjectSelector = nil
		}
		if reflect.DeepEqual(*resources, admission.MatchResources{}) {
			actualBinding.MatchResources = nil
		}
	}
	if !reflect.DeepEqual(actualBinding, expectedBinding) {
		return nil, reject
	}
	for _, original := range []client.Object{&policy, &binding, &namespace} {
		fresh := original.DeepCopyObject().(client.Object)
		if err := reader.Get(ctx, client.ObjectKeyFromObject(original), fresh); err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(original, fresh) {
			return nil, reject
		}
	}
	return &BootstrapFenceProtection{PolicyUID: policy.UID, BindingUID: binding.UID, NamespaceUID: namespace.UID, PolicyGeneration: policy.Generation, BindingGeneration: binding.Generation, PolicyDigest: digest}, nil
}

func bootstrapProtectionObjectCurrent(obj client.Object) bool {
	return obj.GetUID() != "" && obj.GetResourceVersion() != "" && obj.GetGeneration() > 0 && obj.GetDeletionTimestamp() == nil && len(obj.GetOwnerReferences()) == 0
}

func emptyBootstrapProtectionSelector(selector *metav1.LabelSelector) bool {
	return selector == nil || len(selector.MatchLabels) == 0 && len(selector.MatchExpressions) == 0
}

func validBootstrapProtectionIdentities(manager, namespace string) bool {
	parts := strings.Split(manager, ":")
	return len(parts) == 4 && parts[0] == "system" && parts[1] == "serviceaccount" && len(validation.IsDNS1123Label(parts[2])) == 0 && len(validation.IsDNS1123Subdomain(parts[3])) == 0 && len(validation.IsDNS1123Label(namespace)) == 0
}

func bootstrapProtectionDigest(policy *admission.ValidatingAdmissionPolicy, manager, namespace string) (string, error) {
	if policy == nil || !validBootstrapProtectionIdentities(manager, namespace) {
		return "", errors.New("invalid bootstrap protection identity")
	}
	spec := policy.DeepCopy().Spec
	if spec.MatchConstraints != nil {
		if emptyBootstrapProtectionSelector(spec.MatchConstraints.NamespaceSelector) {
			spec.MatchConstraints.NamespaceSelector = nil
		}
		if emptyBootstrapProtectionSelector(spec.MatchConstraints.ObjectSelector) {
			spec.MatchConstraints.ObjectSelector = nil
		}
	}
	// Valid DNS/service-account identities contain no quotes or escapes. Replace
	// only whole double-quoted CEL literals; never substrings in identifiers or
	// namespaces inside a service-account value. No whitespace/CEL normalization
	// is accepted, so any other expression edit changes the compiled digest.
	managerLiteral, namespaceLiteral := strconv.Quote(manager), strconv.Quote(namespace)
	managerCount, namespaceCount := 0, 0
	invalidLiteral := false
	replace := strings.NewReplacer(strconv.Quote(manager), strconv.Quote("@bootstrap-manager@"), strconv.Quote(namespace), strconv.Quote("@bootstrap-namespace@"))
	normalize := func(expression string) string {
		managerCount += strings.Count(expression, managerLiteral)
		namespaceCount += strings.Count(expression, namespaceLiteral)
		if strings.Contains(expression, "@bootstrap-manager@") || strings.Contains(expression, "@bootstrap-namespace@") {
			invalidLiteral = true
		}
		return replace.Replace(expression)
	}
	for i := range spec.MatchConditions {
		spec.MatchConditions[i].Expression = normalize(spec.MatchConditions[i].Expression)
	}
	for i := range spec.Variables {
		spec.Variables[i].Expression = normalize(spec.Variables[i].Expression)
	}
	for i := range spec.Validations {
		spec.Validations[i].Expression = normalize(spec.Validations[i].Expression)
		if spec.Validations[i].MessageExpression != "" {
			spec.Validations[i].MessageExpression = normalize(spec.Validations[i].MessageExpression)
		}
	}
	for i := range spec.AuditAnnotations {
		spec.AuditAnnotations[i].ValueExpression = normalize(spec.AuditAnnotations[i].ValueExpression)
	}
	if invalidLiteral || managerCount != 1 || namespaceCount != 1 {
		return "", errors.New("bootstrap protection installation literals are absent or ambiguous")
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
