// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"testing"

	admission "k8s.io/api/admissionregistration/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func metadataProtectionFixture(t *testing.T) (*admission.ValidatingAdmissionPolicy, *admission.ValidatingAdmissionPolicyBinding, *core.Namespace) {
	t.Helper()
	raw, err := exec.Command("helm", "template", "cvk", "../../charts/cisco-virtual-kubelet", "--namespace", "manager-system", "--kube-version", "1.35.0", "--set", "topology.enabled=true", "--set", "controller.leaderElect=true", "--set", "rbac.profile=strict", "--set", "config.leaseNamespace=metadata-leases", "--show-only", "templates/metadata-handoff-admission.yaml").CombinedOutput()
	if err != nil {
		t.Fatalf("helm: %v %s", err, raw)
	}
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	policy := &admission.ValidatingAdmissionPolicy{}
	binding := &admission.ValidatingAdmissionPolicyBinding{}
	for {
		var obj unstructured.Unstructured
		err := decoder.Decode(&obj.Object)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch obj.GetKind() {
		case "ValidatingAdmissionPolicy":
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, policy); err != nil {
				t.Fatal(err)
			}
		case "ValidatingAdmissionPolicyBinding":
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, binding); err != nil {
				t.Fatal(err)
			}
		}
	}
	if policy.Name == "" || binding.Name == "" {
		t.Fatal("missing rendered metadata policy")
	}
	policy.UID = "policy"
	policy.ResourceVersion = "1"
	policy.Generation = 1
	policy.Status = admission.ValidatingAdmissionPolicyStatus{ObservedGeneration: 1, TypeChecking: &admission.TypeChecking{}}
	binding.UID = "binding"
	binding.ResourceVersion = "1"
	binding.Generation = 1
	ns := &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "metadata-leases", UID: "namespace", ResourceVersion: "1"}, Status: core.NamespaceStatus{Phase: core.NamespaceActive}}
	return policy, binding, ns
}

func TestMetadataPolicyContractFingerprint(t *testing.T) {
	policy, _, _ := metadataProtectionFixture(t)
	digest, err := bootstrapProtectionDigest(policy, bootstrapTestManager, "metadata-leases")
	if err != nil {
		t.Fatal(err)
	}
	if digest != metadataProtectionSpecDigest {
		t.Fatal("compiled metadata policy digest differs from rendered chart", digest)
	}
}

func TestMetadataHandoffProtection(t *testing.T) {
	policy, binding, namespace := metadataProtectionFixture(t)
	for _, mode := range []string{"valid", "unobserved", "uncompiled", "warnings", "altered-policy", "audit-binding", "wrong-protocol", "missing-namespace", "late-policy", "late-binding", "late-namespace"} {
		t.Run(mode, func(t *testing.T) {
			p, b, ns := policy.DeepCopy(), binding.DeepCopy(), namespace.DeepCopy()
			switch mode {
			case "unobserved":
				p.Status.ObservedGeneration = 0
			case "uncompiled":
				p.Status.TypeChecking = nil
			case "warnings":
				p.Status.TypeChecking.ExpressionWarnings = []admission.ExpressionWarning{{FieldRef: "spec", Warning: "unqualified"}}
			case "altered-policy":
				p.Spec.Validations[0].Expression = "true"
			case "audit-binding":
				b.Spec.ValidationActions = []admission.ValidationAction{admission.Audit}
			case "wrong-protocol":
				p.Annotations[metadataProtectionAnnotation] = "future"
			}
			scheme := runtime.NewScheme()
			_ = admission.AddToScheme(scheme)
			_ = core.AddToScheme(scheme)
			objects := []client.Object{p, b}
			if mode != "missing-namespace" {
				objects = append(objects, ns)
			}
			store := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			reader := &bootstrapProtectionRaceReader{Reader: store, mode: mode, reads: map[string]int{}}
			evidence, err := ReadMetadataHandoffProtection(context.Background(), reader, p.Name, bootstrapTestManager, "metadata-leases")
			if (err == nil) != (mode == "valid") {
				t.Fatal("incorrect runtime protection observation", err)
			}
			if mode == "valid" && (evidence.PolicyUID != "policy" || evidence.BindingUID != "binding" || evidence.NamespaceUID != "namespace" || evidence.PolicyDigest != metadataProtectionSpecDigest) {
				t.Fatal("missing protection incarnation", evidence)
			}
			if mode != "valid" && evidence != nil {
				t.Fatal("unproven policy returned")
			}
		})
	}
}
