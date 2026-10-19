// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	admission "k8s.io/api/admissionregistration/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const bootstrapTestManager = "system:serviceaccount:manager-system:cisco-virtual-kubelet-controller"

// The fixture is the rendered chart policy/binding, not a hand-written mock of
// its CEL. The real-API test independently renders the chart and checks its hash.
func bootstrapProtectionFixture(t *testing.T) (*admission.ValidatingAdmissionPolicy, *admission.ValidatingAdmissionPolicyBinding, *core.Namespace) {
	t.Helper()
	raw, err := os.ReadFile("testdata/bootstrap-fence-admission.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) != 2 {
		t.Fatal("bad rendered policy fixture", err)
	}
	p := &admission.ValidatingAdmissionPolicy{}
	b := &admission.ValidatingAdmissionPolicyBinding{}
	if err := json.Unmarshal(rows[0], p); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rows[1], b); err != nil {
		t.Fatal(err)
	}
	p.UID = "policy"
	p.Generation = 1
	p.ResourceVersion = "1"
	p.Status = admission.ValidatingAdmissionPolicyStatus{ObservedGeneration: 1, TypeChecking: &admission.TypeChecking{}}
	b.UID = "binding"
	b.Generation = 1
	b.ResourceVersion = "1"
	n := &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "bootstrap-leases", UID: "namespace", ResourceVersion: "1"}, Status: core.NamespaceStatus{Phase: core.NamespaceActive}}
	return p, b, n
}

type bootstrapProtectionRaceReader struct {
	client.Reader
	mode  string
	reads map[string]int
}

func (r *bootstrapProtectionRaceReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	kind := ""
	switch obj.(type) {
	case *admission.ValidatingAdmissionPolicy:
		kind = "policy"
	case *admission.ValidatingAdmissionPolicyBinding:
		kind = "binding"
	case *core.Namespace:
		kind = "namespace"
	}
	r.reads[kind]++
	if r.mode == "late-"+kind && r.reads[kind] > 1 {
		obj.SetUID(types.UID("replacement"))
	}
	return nil
}

func TestBootstrapFenceProtection(t *testing.T) {
	for _, mode := range []string{"valid", "default-selectors", "missing-policy", "missing-binding", "missing-namespace", "unobserved", "uncompiled", "warnings", "modified-CEL", "condition", "failure-policy", "rules", "selector", "audit-action", "binding-selector", "wrong-policy", "params", "future-protocol", "deleting-policy", "deleting-binding", "terminating-namespace", "wrong-manager", "wrong-namespace", "late-policy", "late-binding", "late-namespace"} {
		t.Run(mode, func(t *testing.T) {
			p, b, n := bootstrapProtectionFixture(t)
			manager, ns := bootstrapTestManager, n.Name
			switch mode {
			case "default-selectors":
				p.Spec.MatchConstraints.NamespaceSelector = &metav1.LabelSelector{}
				p.Spec.MatchConstraints.ObjectSelector = &metav1.LabelSelector{}
				equivalent := admission.Equivalent
				b.Spec.MatchResources = &admission.MatchResources{MatchPolicy: &equivalent, NamespaceSelector: &metav1.LabelSelector{}, ObjectSelector: &metav1.LabelSelector{}}
			case "unobserved":
				p.Status.ObservedGeneration = 0
			case "uncompiled":
				p.Status.TypeChecking = nil
			case "warnings":
				p.Status.TypeChecking.ExpressionWarnings = []admission.ExpressionWarning{{FieldRef: "spec.validations[0].expression", Warning: "bad expression"}}
			case "modified-CEL":
				p.Spec.Validations[0].Expression = "true"
			case "condition":
				p.Spec.MatchConditions[0].Expression = "false"
			case "failure-policy":
				ignore := admission.Ignore
				p.Spec.FailurePolicy = &ignore
			case "rules":
				p.Spec.MatchConstraints.ResourceRules[0].Operations = []admission.OperationType{admission.Create}
			case "selector":
				p.Spec.MatchConstraints.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"skip": "true"}}
			case "audit-action":
				b.Spec.ValidationActions = []admission.ValidationAction{admission.Audit}
			case "binding-selector":
				b.Spec.MatchResources = &admission.MatchResources{ObjectSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"skip": "true"}}}
			case "wrong-policy":
				b.Spec.PolicyName = "other"
			case "params":
				b.Spec.ParamRef = &admission.ParamRef{Name: "bypass"}
			case "future-protocol":
				p.Annotations[bootstrapProtectionAnnotation] = "v9"
			case "deleting-policy":
				now := metav1.Now()
				p.DeletionTimestamp = &now
				p.Finalizers = []string{"test-retain"}
			case "deleting-binding":
				now := metav1.Now()
				b.DeletionTimestamp = &now
				b.Finalizers = []string{"test-retain"}
			case "terminating-namespace":
				n.Status.Phase = core.NamespaceTerminating
			case "wrong-manager":
				manager = "system:serviceaccount:manager-system:other"
			case "wrong-namespace":
				ns = "other"
			}
			scheme := runtime.NewScheme()
			_ = admission.AddToScheme(scheme)
			_ = core.AddToScheme(scheme)
			objects := []client.Object{}
			if mode != "missing-policy" {
				objects = append(objects, p)
			}
			if mode != "missing-binding" {
				objects = append(objects, b)
			}
			if mode != "missing-namespace" {
				objects = append(objects, n)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			reader := &bootstrapProtectionRaceReader{Reader: c, mode: mode, reads: map[string]int{}}
			observation, err := ReadBootstrapFenceProtection(context.Background(), reader, p.Name, manager, ns)
			valid := mode == "valid" || mode == "default-selectors"
			if (err == nil) != valid {
				digest, _ := bootstrapProtectionDigest(p, manager, ns)
				t.Fatal("protection observation outcome", err, "actual digest", digest)
			}
			if !valid {
				if observation != nil {
					t.Fatal("invalid protection returned")
				}
				return
			}
			if observation.PolicyUID != p.UID || observation.BindingUID != b.UID || observation.NamespaceUID != n.UID || observation.PolicyDigest != bootstrapProtectionSpecDigest {
				t.Fatal("incomplete installation observation")
			}
		})
	}
}

func TestBootstrapProtectionDigestIdentityNormalization(t *testing.T) {
	p, _, _ := bootstrapProtectionFixture(t)
	first, err := bootstrapProtectionDigest(p, bootstrapTestManager, "bootstrap-leases")
	if err != nil {
		t.Fatal(err)
	}
	other := p.DeepCopy()
	replace := strings.NewReplacer(bootstrapTestManager, "system:serviceaccount:other:manager", "bootstrap-leases", "other-leases")
	other.Spec.Validations[0].Expression = replace.Replace(other.Spec.Validations[0].Expression)
	for i := range other.Spec.Variables {
		other.Spec.Variables[i].Expression = replace.Replace(other.Spec.Variables[i].Expression)
	}
	second, err := bootstrapProtectionDigest(other, "system:serviceaccount:other:manager", "other-leases")
	if err != nil || first != second {
		t.Fatal("installation literals changed the semantic contract", err)
	}
	other.Spec.Validations[0].Expression += " || true"
	third, err := bootstrapProtectionDigest(other, "system:serviceaccount:other:manager", "other-leases")
	if err != nil || first == third {
		t.Fatal("identity normalization accepted an extra bypass", err)
	}
}

func TestBootstrapFenceCannotTransferProtection(t *testing.T) {
	for _, kind := range []string{"policy", "binding", "namespace"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			c, nc, _ := bootstrapFenceFixture(t)
			store := &bootstrapFenceStore{Client: c}
			installation := bootstrapFenceTestInstallation()
			authorize := BootstrapFenceRuntimeCheck(func(context.Context, string, *cvk.NetworkController, BootstrapIdentityObservation) error { return nil })
			first, err := ReserveBootstrapFence(ctx, store, store, nc, installation, authorize)
			if err != nil {
				t.Fatal(err)
			}
			var obj client.Object
			switch kind {
			case "policy":
				obj = &admission.ValidatingAdmissionPolicy{}
			case "binding":
				obj = &admission.ValidatingAdmissionPolicyBinding{}
			case "namespace":
				obj = &core.Namespace{}
			}
			name := installation.PolicyName
			if kind == "namespace" {
				name = installation.LeaseNamespace
			}
			if err := c.Get(ctx, client.ObjectKey{Name: name}, obj); err != nil {
				t.Fatal(err)
			}
			if err := c.Delete(ctx, obj); err != nil {
				t.Fatal(err)
			}
			obj.SetUID("replacement")
			obj.SetResourceVersion("")
			if err := c.Create(ctx, obj); err != nil {
				t.Fatal(err)
			}
			// The replacement independently satisfies the compiled protection;
			// it still cannot inherit the original reservation's installation.
			if _, err := ReadBootstrapFenceProtection(ctx, c, installation.PolicyName, installation.ManagerUsername, installation.LeaseNamespace); err != nil {
				t.Fatal("replacement is not otherwise valid", err)
			}
			second, err := ReserveBootstrapFence(ctx, store, store, nc, installation, authorize)
			if err == nil || second != nil || store.creates.Load() != 1 {
				t.Fatal("protection replacement adopted old fence", err)
			}
			retained, err := ReadBootstrapFence(ctx, c, installation.LeaseNamespace, "bootstrap123")
			if err != nil || !reflect.DeepEqual(first, retained) {
				t.Fatal("retained protection evidence changed", err)
			}
		})
	}
}
