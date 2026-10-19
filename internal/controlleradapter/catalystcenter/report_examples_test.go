// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	corev1 "k8s.io/api/core/v1"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

func TestReportExamplesCompile(t *testing.T) {
	files, err := filepath.Glob("../../../examples/configs/catalyst-center/*-report.yaml")
	if err != nil || len(files) == 0 {
		t.Fatal("report examples unavailable")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var config configapi.NetworkControllerConfig
			if err := yaml.UnmarshalStrict(raw, &config); err != nil {
				t.Fatal(err)
			}
			if !supportedConfigReport(&config) || config.Spec.Source.Inline == nil {
				t.Fatal("example violates reporting contract")
			}
			plans, err := (&client{}).compileConfigReports(config.Spec.Source.Inline.Raw, config.Spec.ManagedSections)
			if err != nil || len(plans) != len(config.Spec.ManagedSections) {
				t.Fatalf("example does not compile: %v", err)
			}
		})
	}
}

func TestApplyExamplesCompile(t *testing.T) {
	files, err := filepath.Glob("../../../examples/configs/catalyst-center/*-apply.yaml")
	if err != nil || len(files) == 0 {
		t.Fatal("apply examples unavailable")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			compileApplyExample(t, raw)
		})
	}
}

// Share the public example compiler with staged example qualification; the
// product runtime's initial NoAuth selector stays authoritative for routing.
func compileApplyExample(t *testing.T, raw []byte) {
	t.Helper()
	var err error
	var config configapi.NetworkControllerConfig
	if err = yaml.UnmarshalStrict(raw, &config); err != nil {
		t.Fatal(err)
	}
	if discoveryConfigSpecShape(&config) {
		if supportedConfigurationApply(&config) {
			t.Fatal("Delete example bypassed enrollment")
		}
		a, _ := journalFixture(t)
		nc, err := a.currentController(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		nc.Name, nc.Namespace = config.Spec.ControllerRef.Name, config.Namespace
		config.UID, config.Generation = "example", 1
		if !discoveryDeletionEligible(nc, &config) {
			t.Fatal("Delete example is not manager eligible")
		}
		config.Finalizers = append(config.Finalizers, discoveryConfigFinalizer)
	}
	if tagDeletionShape(&config) && config.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete && tagCleanupFinalizer(&config) == tagConfigFinalizer {
		if supportedConfigurationApply(&config) {
			t.Fatal("Tag Delete example bypassed enrollment")
		}
		a, _ := journalFixture(t)
		nc, err := a.currentController(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		nc.Name, nc.Namespace = config.Spec.ControllerRef.Name, config.Namespace
		config.UID, config.Generation = "example", 1
		if !tagDeletionEligible(nc, &config) {
			t.Fatal("Tag Delete example is not manager eligible")
		}
		config.Finalizers = append(config.Finalizers, tagConfigFinalizer)
	}
	if !supportedConfigurationApply(&config) || config.Spec.Source.Inline == nil {
		t.Fatal("invalid apply example contract")
	}
	var plan configurationApplyPlan
	if len(config.Spec.SecretRefs) == 0 && len(config.Spec.ManagedSections) == 1 && config.Spec.ManagedSections[0] == "inventory" {
		a := &adapter{client: &client{}, workerPodName: "example-worker", workerPodUID: "example-pod"}
		if err := a.ConfigureConfigurationReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
			t.Fatal(err)
		}
		config.UID, config.Generation = "example", 1
		source := &configSourceSnapshot{raw: config.Spec.Source.Inline.Raw}
		var handled bool
		nc := &ciskov1.NetworkController{}
		if discoveryNoAuthInitialSource(nc, &config, source) {
			nc.Name, nc.Namespace, nc.UID, nc.Generation = config.Spec.ControllerRef.Name, config.Namespace, "example-controller", 1
			nc.Spec.Type, nc.Spec.Endpoint = TypeName, "https://controller.example.test"
			nc.Spec.CredentialSecretRef.Name = "controller-api"
			a.key, a.uid, a.generation = ctrlclient.ObjectKeyFromObject(nc), nc.UID, nc.Generation
			plan, err = a.compileDiscoveryCredentialApply(nc, &config, source)
			handled = true
		} else {
			plan, handled, err = a.compileOwnedDiscoveryApply(nc, &config, source)
		}
		if !handled {
			plan, err = a.compileAdmittedInventoryApply(&config, source)
		}
	} else if len(config.Spec.SecretRefs) == 0 && len(config.Spec.ManagedSections) == 1 && config.Spec.ManagedSections[0] == "network_settings" {
		config.UID, config.Generation = "example", 1
		plan, err = (&adapter{client: &client{}}).compileNetworkSettingsApply(&ciskov1.NetworkController{}, &config, &configSourceSnapshot{raw: config.Spec.Source.Inline.Raw})
	} else if len(config.Spec.SecretRefs) == 0 {
		plan, err = (&client{}).compileConfigurationApply(config.Spec.Source.Inline.Raw, config.Spec.ManagedSections, nil)
		if err == nil && len(config.Spec.ManagedSections) == 1 && config.Spec.ManagedSections[0] == "feature_templates" {
			config.UID, config.Generation = "example", 1
			a := &adapter{client: &client{}}
			nc := &ciskov1.NetworkController{}
			source := &configSourceSnapshot{raw: config.Spec.Source.Inline.Raw}
			if err = a.bindFeatureCreateOwnership(nc, &config, source, &plan); err == nil {
				err = a.bindFeaturePrune(nc, &config, source, &plan)
			}
		}
	} else {
		nc, _, root, _, _ := configurationSecretFixture(t)
		nc.Name, nc.Namespace = config.Spec.ControllerRef.Name, config.Namespace
		nc.Spec.IntentSecretSources = nil
		config.UID, config.Generation = "example", 1
		for _, ref := range config.Spec.SecretRefs {
			nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, ciskov1.NetworkControllerIntentSecretSource{Alias: ref.Source, Name: "fixture", Key: "credential"})
			relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: config.Name, Section: ref.Section, JSONPointer: ref.Path, SourceAlias: ref.Source, SecretName: "fixture", SecretKey: "credential"})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, relative)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			value := versionedSecretFixture(t, 1, "fixture-value")
			if discoveryCredentialApplyShape(&config) && (strings.HasSuffix(ref.Path, "/password_list") || strings.HasSuffix(ref.Path, "/enable_password_list")) {
				value = versionedSecretFixture(t, 1, `["fixture-value"]`)
			}
			if ref.Use == "PrivateComparisonKey" {
				value = []byte(strings.Repeat("a1", 32))
			}
			if err := os.WriteFile(path, value, 0600); err != nil {
				t.Fatal(err)
			}
		}
		a := &adapter{intentSecretPath: root, client: &client{}}
		if wirelessTagComparisonConfig(&config) {
			plan, err = a.compileWirelessTagComparison(nc, &config, &configSourceSnapshot{raw: config.Spec.Source.Inline.Raw})
		} else if discoveryCredentialApplyShape(&config) {
			nc.Spec.Type, nc.Spec.Endpoint = TypeName, "https://controller.example.test"
			nc.Spec.CredentialSecretRef.Name = "controller-api"
			a.key, a.uid, a.generation = ctrlclient.ObjectKeyFromObject(nc), nc.UID, nc.Generation
			plan, err = a.compileDiscoveryCredentialApply(nc, &config, &configSourceSnapshot{raw: config.Spec.Source.Inline.Raw})
		} else {
			plan, err = a.compileCredentialConfigurationApply(nc, &config, &configSourceSnapshot{raw: config.Spec.Source.Inline.Raw})
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if plan.executor.count() == 0 {
		// Prune-all intentionally has no desired allocations; retained
		// owned receipts supply deletion targets at reconciliation.
		ownedPrune := tagPruneSource(config.Spec.Source.Inline.Raw, config.Spec.ManagedSections) && config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete
		switch e := plan.executor.(type) {
		case *ipamConfigurationExecutor:
			ownedPrune = e.prune
		case *ownedDiscoveryExecutor:
			ownedPrune = e.prune
		case *featurePruneExecutor:
			ownedPrune = true
		}
		if !ownedPrune || config.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyDelete {
			t.Fatal("empty example without explicit owned prune semantics")
		}
	}
}

func TestTemplateContentExamplesCompile(t *testing.T) {
	for _, name := range []string{"template-content.yaml", "template-content-execution.yaml", "template-private-baseline-execution.yaml", "template-composite-execution.yaml", "template-tag-execution.yaml", "template-software-version-execution.yaml", "template-variable-metadata-execution.yaml", "template-parameter-types-execution.yaml", "template-binding-execution.yaml"} {
		raw, err := os.ReadFile("../../../examples/configs/catalyst-center/" + name)
		if err != nil {
			t.Fatal(err)
		}
		documents := bytes.Split(raw, []byte("\n---\n"))
		if len(documents) != 2 {
			t.Fatal("expected ConfigMap and config")
		}
		var cm corev1.ConfigMap
		var config configapi.NetworkControllerConfig
		if err = yaml.UnmarshalStrict(documents[0], &cm); err != nil {
			t.Fatal(err)
		}
		if err = yaml.UnmarshalStrict(documents[1], &config); err != nil {
			t.Fatal(err)
		}
		if config.Spec.Source.ConfigMapRef == nil || config.Spec.Source.ConfigMapRef.Name != cm.Name || config.Namespace != cm.Namespace {
			t.Fatal("example source mismatch")
		}
		model := []byte(cm.Data[config.Spec.Source.ConfigMapRef.Key])
		files, err := nacmodel.ParseTemplateAssets([]byte(cm.Data[nacmodel.TemplateAssetsKey]))
		if err != nil {
			t.Fatal(err)
		}
		if config.Spec.Mode == configapi.NetworkControllerApplyModeApply {
			if !supportedConfigurationApply(&config) {
				t.Fatal("unsupported apply example")
			}
			executor, err := (&client{}).compileConfigurationExecutor(model, "templates", files)
			expected := 3
			if name == "template-composite-execution.yaml" || name == "template-tag-execution.yaml" || name == "template-software-version-execution.yaml" {
				expected = 5
			}
			if err != nil || executor.count() != expected {
				t.Fatal("invalid template execution example", err)
			}
		} else {
			if _, err := (&client{}).compileConfigReports(model, []string{"templates"}, files); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestFloorImageExampleCompiles(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/floor-image-execution.yaml")
	if err != nil {
		t.Fatal(err)
	}
	documents := bytes.Split(raw, []byte("\n---\n"))
	if len(documents) != 2 {
		t.Fatal("expected asset ConfigMap and config")
	}
	var cm corev1.ConfigMap
	var config configapi.NetworkControllerConfig
	if err = yaml.UnmarshalStrict(documents[0], &cm); err != nil {
		t.Fatal(err)
	}
	if err = yaml.UnmarshalStrict(documents[1], &config); err != nil {
		t.Fatal(err)
	}
	if !supportedConfigurationApply(&config) || config.Spec.Source.ConfigMapRef == nil || config.Spec.Source.ConfigMapRef.Name != cm.Name || config.Namespace != cm.Namespace {
		t.Fatal("invalid source contract")
	}
	assets, err := loadFloorImageAssets(&cm, config.Spec.Source.ConfigMapRef.Key)
	if err != nil {
		t.Fatal(err)
	}
	config.UID = "example"
	a := &adapter{client: &client{}}
	plan, err := a.compileSiteImageApply(&ciskov1.NetworkController{}, &config, &configSourceSnapshot{raw: []byte(cm.Data[config.Spec.Source.ConfigMapRef.Key]), floorImages: assets})
	if err != nil || plan.executor.count() != 2 {
		t.Fatal("image example failed to compile", err)
	}
}
