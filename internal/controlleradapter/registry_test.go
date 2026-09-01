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

package controlleradapter

// Tests in this file replace the package-global registry and therefore must
// not call t.Parallel.

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
)

type testAdapter struct{}

func (*testAdapter) SetupWithManager(ctrl.Manager) error { return nil }

func resetRegistry(t *testing.T) func() {
	t.Helper()
	registryMu.Lock()
	saved := registry
	registry = map[string]Registration{}
	registryMu.Unlock()
	return func() {
		registryMu.Lock()
		registry = saved
		registryMu.Unlock()
	}
}

func validRegistration(typeName string) Registration {
	return Registration{
		Descriptor: Descriptor{
			Type:        typeName,
			DisplayName: "Test Controller " + typeName,
			NetAsCode: ciskov1.NetworkControllerNetAsCodeStatus{
				Format:        "netascode-" + typeName,
				Stripe:        strings.ReplaceAll(typeName, "-", "_"),
				ModelVersions: []string{"1.0.0"},
				Sections:      []string{"inventory", "sites"},
			},
			Capabilities:      []string{"config", "inventory"},
			WorkerClusterRole: DefaultWorkerClusterRole,
		},
		Factory: func(Options) (Adapter, error) { return &testAdapter{}, nil },
	}
}

func TestRegisterLookupAndNewAdapter(t *testing.T) {
	defer resetRegistry(t)()

	var received Options
	reg := validRegistration("campus-controller")
	reg.Factory = func(opts Options) (Adapter, error) {
		received = opts
		return &testAdapter{}, nil
	}
	Register(reg)

	controller := &ciskov1.NetworkController{}
	controller.Name = "campus"
	controller.Spec.Type = "campus-controller"
	controller.Spec.Endpoint = "https://controller.example.test"
	controller.Spec.CredentialSecretRef.Name = "credentials"
	adapter, err := NewAdapter("campus-controller", Options{
		Controller:       controller,
		CredentialPath:   DefaultCredentialPath,
		CAPath:           DefaultCAPath,
		IntentSecretPath: DefaultIntentSecretPath,
		MaterialRotation: testMaterialRotationPolicy(),
	})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	if adapter == nil {
		t.Fatal("NewAdapter returned nil")
	}
	if received.Controller == controller {
		t.Fatal("factory received the caller's NetworkController pointer")
	}
	if received.Controller.Name != "campus" || received.Controller.Spec.Endpoint != controller.Spec.Endpoint {
		t.Fatalf("factory snapshot=%+v, want source values", received.Controller)
	}
	received.Controller.Spec.Endpoint = "https://adapter-mutated.example.test"
	if controller.Spec.Endpoint != "https://controller.example.test" {
		t.Fatalf("factory snapshot mutation escaped to caller: %q", controller.Spec.Endpoint)
	}
	if received.CredentialPath != DefaultCredentialPath || received.CAPath != DefaultCAPath || received.IntentSecretPath != DefaultIntentSecretPath {
		t.Fatalf("factory paths=%+v", received)
	}
	if received.MaterialRotation.Changes == nil || received.MaterialRotation.MaxSessionLifetime != DefaultMaxSessionLifetime {
		t.Fatalf("factory material rotation policy=%+v", received.MaterialRotation)
	}
}

func TestLookupAndRegisterUseDefensiveCopies(t *testing.T) {
	defer resetRegistry(t)()

	reg := validRegistration("copy-test")
	Register(reg)
	reg.Descriptor.NetAsCode.ModelVersions[0] = "mutated-after-register"
	reg.Descriptor.NetAsCode.Sections[0] = "mutated-after-register"
	reg.Descriptor.Capabilities[0] = "mutated-after-register"

	first, ok := Lookup("copy-test")
	if !ok {
		t.Fatal("Lookup(copy-test) missing")
	}
	if first.Descriptor.NetAsCode.ModelVersions[0] == "mutated-after-register" || first.Descriptor.NetAsCode.Sections[0] == "mutated-after-register" || first.Descriptor.Capabilities[0] == "mutated-after-register" {
		t.Fatalf("registration retained caller-owned slices: %+v", first.Descriptor)
	}
	first.Descriptor.NetAsCode.ModelVersions[0] = "mutated-after-lookup"
	first.Descriptor.NetAsCode.Sections[0] = "mutated-after-lookup"
	first.Descriptor.Capabilities[0] = "mutated-after-lookup"

	second, ok := Lookup("copy-test")
	if !ok {
		t.Fatal("Lookup(copy-test) missing on second read")
	}
	if second.Descriptor.NetAsCode.ModelVersions[0] == "mutated-after-lookup" || second.Descriptor.NetAsCode.Sections[0] == "mutated-after-lookup" || second.Descriptor.Capabilities[0] == "mutated-after-lookup" {
		t.Fatalf("Lookup returned registry-owned slices: %+v", second.Descriptor)
	}
}

func TestRegisteredTypesAreSorted(t *testing.T) {
	defer resetRegistry(t)()
	Register(validRegistration("zeta"))
	Register(validRegistration("alpha"))
	Register(validRegistration("middle"))

	want := []string{"alpha", "middle", "zeta"}
	if got := RegisteredTypes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("RegisteredTypes()=%v, want %v", got, want)
	}
	if !Registered("middle") || Registered("absent") {
		t.Fatalf("Registered results incorrect")
	}
	if descriptor, ok := DescriptorFor("alpha"); !ok || descriptor.Type != "alpha" {
		t.Fatalf("DescriptorFor(alpha)=(%+v,%v)", descriptor, ok)
	}
}

func TestRegisterRejectsInvalidContracts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Registration)
		want   string
	}{
		{name: "empty type", mutate: func(r *Registration) { r.Descriptor.Type = "" }, want: "empty controller type"},
		{name: "non DNS type", mutate: func(r *Registration) { r.Descriptor.Type = "Catalyst_Center" }, want: "DNS-1123"},
		{name: "numeric type prefix", mutate: func(r *Registration) { r.Descriptor.Type = "1controller" }, want: "lowercase letter"},
		{name: "empty display name", mutate: func(r *Registration) { r.Descriptor.DisplayName = " " }, want: "empty display name"},
		{name: "display name whitespace", mutate: func(r *Registration) { r.Descriptor.DisplayName = " Controller" }, want: "whitespace"},
		{name: "foreign model format", mutate: func(r *Registration) { r.Descriptor.NetAsCode.Format = "openconfig" }, want: "netascode-*"},
		{name: "empty model suffix", mutate: func(r *Registration) { r.Descriptor.NetAsCode.Format = "netascode-" }, want: "netascode-*"},
		{name: "invalid model format", mutate: func(r *Registration) { r.Descriptor.NetAsCode.Format = "netascode-BAD" }, want: "invalid"},
		{name: "empty stripe", mutate: func(r *Registration) { r.Descriptor.NetAsCode.Stripe = "" }, want: "empty Network as Code stripe"},
		{name: "invalid stripe", mutate: func(r *Registration) { r.Descriptor.NetAsCode.Stripe = "Bad-Stripe" }, want: "must match"},
		{name: "no model versions", mutate: func(r *Registration) { r.Descriptor.NetAsCode.ModelVersions = nil }, want: "no qualified"},
		{name: "duplicate model version", mutate: func(r *Registration) { r.Descriptor.NetAsCode.ModelVersions = []string{"1.0", "1.0"} }, want: "duplicate"},
		{name: "long model version", mutate: func(r *Registration) { r.Descriptor.NetAsCode.ModelVersions = []string{strings.Repeat("v", 129)} }, want: "exceeds 128"},
		{name: "no sections", mutate: func(r *Registration) { r.Descriptor.NetAsCode.Sections = nil }, want: "no Network as Code sections"},
		{name: "empty section", mutate: func(r *Registration) { r.Descriptor.NetAsCode.Sections = []string{""} }, want: "must not be empty"},
		{name: "duplicate section", mutate: func(r *Registration) { r.Descriptor.NetAsCode.Sections = []string{"sites", "sites"} }, want: "duplicate"},
		{name: "section whitespace", mutate: func(r *Registration) { r.Descriptor.NetAsCode.Sections = []string{" sites"} }, want: "whitespace"},
		{name: "invalid section", mutate: func(r *Registration) { r.Descriptor.NetAsCode.Sections = []string{"Site-Settings"} }, want: "must match"},
		{name: "empty capability", mutate: func(r *Registration) { r.Descriptor.Capabilities = []string{""} }, want: "must not be empty"},
		{name: "invalid capability", mutate: func(r *Registration) { r.Descriptor.Capabilities = []string{"Task_Polling"} }, want: "DNS-1123"},
		{name: "duplicate capability", mutate: func(r *Registration) { r.Descriptor.Capabilities = []string{"config", "config"} }, want: "duplicate"},
		{name: "empty role", mutate: func(r *Registration) { r.Descriptor.WorkerClusterRole = "" }, want: "empty worker ClusterRole"},
		{name: "dynamic role template", mutate: func(r *Registration) { r.Descriptor.WorkerClusterRole = "worker-{{name}}" }, want: "invalid"},
		{name: "unaudited role", mutate: func(r *Registration) { r.Descriptor.WorkerClusterRole = "cisco-virtual-kubelet-controller" }, want: "audited allow-list"},
		{name: "nil factory", mutate: func(r *Registration) { r.Factory = nil }, want: "nil factory"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer resetRegistry(t)()
			reg := validRegistration("validation-test")
			test.mutate(&reg)
			assertRegisterPanics(t, reg, test.want)
		})
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	defer resetRegistry(t)()
	Register(validRegistration("duplicate"))
	assertRegisterPanics(t, validRegistration("duplicate"), "duplicate registration")
}

func TestUnknownTypeErrorsEnumerateRegisteredTypes(t *testing.T) {
	defer resetRegistry(t)()
	Register(validRegistration("zeta"))
	Register(validRegistration("alpha"))

	_, err := NewAdapter("missing", Options{Controller: &ciskov1.NetworkController{}})
	if err == nil || !strings.Contains(err.Error(), "alpha, zeta") {
		t.Fatalf("NewAdapter unknown error=%v, want sorted registrations", err)
	}
	if err := InstallScheme("missing", runtime.NewScheme()); err == nil || !strings.Contains(err.Error(), "alpha, zeta") {
		t.Fatalf("InstallScheme unknown error=%v, want sorted registrations", err)
	}
}

func TestNewAdapterRejectsNilMismatchedAndNilResult(t *testing.T) {
	defer resetRegistry(t)()
	reg := validRegistration("expected")
	Register(reg)

	if _, err := NewAdapter("expected", Options{}); err == nil || !strings.Contains(err.Error(), "nil NetworkController") {
		t.Fatalf("nil controller error=%v", err)
	}
	invalid := &ciskov1.NetworkController{}
	invalid.Spec.Type = "expected"
	invalid.Spec.Endpoint = "http://controller.example.test"
	invalid.Spec.CredentialSecretRef.Name = "credentials"
	if _, err := NewAdapter("expected", Options{Controller: invalid, CredentialPath: DefaultCredentialPath}); err == nil || !strings.Contains(err.Error(), "invalid NetworkController") {
		t.Fatalf("invalid controller error=%v", err)
	}
	invalid.Spec.Endpoint = "https://controller.example.test"
	if _, err := NewAdapter("expected", Options{Controller: invalid, CredentialPath: "/etc/credentials"}); err == nil || !strings.Contains(err.Error(), "must be below") {
		t.Fatalf("unsafe credential path error=%v", err)
	}
	if _, err := NewAdapter("expected", Options{Controller: invalid, CredentialPath: DefaultIntentSecretPath, IntentSecretPath: DefaultIntentSecretPath}); err == nil || !strings.Contains(err.Error(), "fixed runtime path") {
		t.Fatalf("swapped credential path error=%v", err)
	}
	if _, err := NewAdapter("expected", Options{Controller: invalid, CredentialPath: DefaultCredentialPath}); err == nil || !strings.Contains(err.Error(), "intentSecretPath") {
		t.Fatalf("missing intent secret path error=%v", err)
	}
	validPaths := Options{
		Controller:       invalid,
		CredentialPath:   DefaultCredentialPath,
		IntentSecretPath: DefaultIntentSecretPath,
	}
	if _, err := NewAdapter("expected", validPaths); err == nil || !strings.Contains(err.Error(), "rotation change channel") {
		t.Fatalf("missing material rotation channel error=%v", err)
	}
	validPaths.MaterialRotation = MaterialRotationPolicy{Changes: make(chan struct{})}
	if _, err := NewAdapter("expected", validPaths); err == nil || !strings.Contains(err.Error(), "max session lifetime") {
		t.Fatalf("missing max session lifetime error=%v", err)
	}
	mismatch := &ciskov1.NetworkController{}
	mismatch.Spec.Type = "different"
	mismatch.Spec.Endpoint = "https://controller.example.test"
	mismatch.Spec.CredentialSecretRef.Name = "credentials"
	if _, err := NewAdapter("expected", Options{Controller: mismatch, CredentialPath: DefaultCredentialPath}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched type error=%v", err)
	}

	defer resetRegistry(t)()
	nilReg := validRegistration("nil-result")
	nilReg.Factory = func(Options) (Adapter, error) { return nil, nil }
	Register(nilReg)
	nilResult := &ciskov1.NetworkController{}
	nilResult.Spec.Type = "nil-result"
	nilResult.Spec.Endpoint = "https://controller.example.test"
	nilResult.Spec.CredentialSecretRef.Name = "credentials"
	if _, err := NewAdapter("nil-result", Options{Controller: nilResult, CredentialPath: DefaultCredentialPath, IntentSecretPath: DefaultIntentSecretPath, MaterialRotation: testMaterialRotationPolicy()}); err == nil || !strings.Contains(err.Error(), "nil adapter") {
		t.Fatalf("nil adapter result error=%v", err)
	}
}

func TestNewAdapterWrapsFactoryError(t *testing.T) {
	defer resetRegistry(t)()
	reg := validRegistration("factory-error")
	reg.Factory = func(Options) (Adapter, error) { return nil, errors.New("adapter setup failed") }
	Register(reg)
	controller := &ciskov1.NetworkController{}
	controller.Spec.Type = "factory-error"
	controller.Spec.Endpoint = "https://controller.example.test"
	controller.Spec.CredentialSecretRef.Name = "credentials"
	_, err := NewAdapter("factory-error", Options{Controller: controller, CredentialPath: DefaultCredentialPath, IntentSecretPath: DefaultIntentSecretPath, MaterialRotation: testMaterialRotationPolicy()})
	if err == nil || !strings.Contains(err.Error(), `NewAdapter "factory-error"`) || !strings.Contains(err.Error(), "adapter setup failed") {
		t.Fatalf("factory error=%v", err)
	}
}

func testMaterialRotationPolicy() MaterialRotationPolicy {
	return MaterialRotationPolicy{
		Changes:            make(chan struct{}),
		MaxSessionLifetime: DefaultMaxSessionLifetime,
	}
}

func TestInstallSchemeOptionalAndRegistered(t *testing.T) {
	defer resetRegistry(t)()
	Register(validRegistration("no-scheme"))
	if err := InstallScheme("no-scheme", nil); err != nil {
		t.Fatalf("optional InstallScheme: %v", err)
	}

	called := false
	reg := validRegistration("with-scheme")
	reg.AddToScheme = func(s *runtime.Scheme) error {
		called = s != nil
		return nil
	}
	Register(reg)
	if err := InstallScheme("with-scheme", nil); err == nil || !strings.Contains(err.Error(), "nil scheme") {
		t.Fatalf("nil scheme error=%v", err)
	}
	if err := InstallScheme("with-scheme", runtime.NewScheme()); err != nil {
		t.Fatalf("InstallScheme: %v", err)
	}
	if !called {
		t.Fatal("AddToScheme was not invoked")
	}
}

func TestDescriptorDigestIsOrderIndependentAndContractSensitive(t *testing.T) {
	descriptor := Descriptor{
		Type:        "digest-controller",
		DisplayName: "Digest Controller",
		NetAsCode: ciskov1.NetworkControllerNetAsCodeStatus{
			Format:        "netascode-digest-controller",
			Stripe:        "digest_controller",
			ModelVersions: []string{"2.0", "1.0"},
			Sections:      []string{"sites", "inventory"},
		},
		Capabilities:      []string{"config", "inventory"},
		WorkerClusterRole: DefaultWorkerClusterRole,
	}
	reordered := descriptor
	reordered.NetAsCode.ModelVersions = []string{"1.0", "2.0"}
	reordered.NetAsCode.Sections = []string{"inventory", "sites"}
	reordered.Capabilities = []string{"inventory", "config"}
	if DescriptorDigest(descriptor) != DescriptorDigest(reordered) {
		t.Fatal("descriptor digest depends on set ordering")
	}
	reordered.WorkerClusterRole = "different-audited-role"
	if DescriptorDigest(descriptor) == DescriptorDigest(reordered) {
		t.Fatal("descriptor digest ignored worker RBAC contract")
	}
}

func TestRegistryConcurrentAccess(t *testing.T) {
	defer resetRegistry(t)()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			Register(validRegistration(fmt.Sprintf("controller-%02d", i)))
		}()
	}
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = Registered("controller-00")
			_, _ = Lookup("controller-01")
			_ = RegisteredTypes()
		}()
	}
	wg.Wait()
	if got := len(RegisteredTypes()); got != 16 {
		t.Fatalf("registered type count=%d, want 16", got)
	}
}

func assertRegisterPanics(t *testing.T, reg Registration, want string) {
	t.Helper()
	defer func() {
		value := recover()
		if value == nil {
			t.Fatal("Register did not panic")
		}
		if got := fmt.Sprint(value); !strings.Contains(got, want) {
			t.Fatalf("Register panic=%q, want substring %q", got, want)
		}
	}()
	Register(reg)
}
