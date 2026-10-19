//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestEnvtestDiscoveryCredentialProjectionBoundary(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, test := range []struct {
		name      string
		fields    map[string]any
		values    map[string]string
		configMap bool
	}{
		{"cli", map[string]any{"user_name_list": []any{"operator"}, "password_list": nil, "password_list_version": 1}, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`}, false},
		{"cli-configmap", map[string]any{"user_name_list": []any{"operator"}, "password_list": nil, "password_list_version": 1}, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`}, true},
		{"http", map[string]any{"http_read_credential": nil, "http_read_credential_version": 1}, map[string]string{"httpReadCredential": discoveryCredentialMarker}, false},
		{"snmp", map[string]any{"snmp_version": "v3", "snmp_user_name": "operator", "snmp_mode": "AuthPriv", "snmp_auth_protocol": "SHA", "snmp_priv_protocol": "AES128", "snmp_auth_passphrase_version": 1, "snmp_priv_passphrase_version": 1}, map[string]string{"snmpAuthPassphrase": discoveryCredentialMarker + "_AUTH", "snmpPrivPassphrase": discoveryCredentialMarker + "_PRIV"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			raw := discoveryCredentialSource(t, test.fields, false)
			a, _, c, source, _ := discoveryProjectionFixture(t, raw, test.values)
			// Projection alone cannot enroll the manager-owned deletion finalizer.
			c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
			installWirelessFeatureQualificationAPI(t, api, "discovery-credential-"+test.name)(a, c)
			if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
				t.Fatal(err)
			}
			if test.configMap {
				cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: "discovery-model"}, Data: map[string]string{"model": string(raw)}}
				if err := api.Create(ctx, cm); err != nil {
					t.Fatal(err)
				}
				c.Spec.Source.Inline = nil
				c.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}
				if err := api.Update(ctx, c); err != nil {
					t.Fatal(err)
				}
				var err error
				source, err = loadControllerSource(ctx, api, c)
				if err != nil {
					t.Fatal(err)
				}
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			p, err := a.prepareDiscoveryCredentialProjection(nc, c, source)
			if err != nil {
				t.Fatal(err)
			}
			request, key, err := p.bindRequest(nc, c, source, 0, nil)
			if err != nil || len(request.versions) != len(c.Spec.SecretRefs) || key == "" {
				t.Fatal("persisted projection failed", err)
			}
			stored, err := json.Marshal(c)
			if err != nil || strings.Contains(string(stored), discoveryCredentialMarker) {
				t.Fatal("credential material entered the Kubernetes source")
			}
			calls := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) })
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
				t.Fatal(err)
			}
			if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
				t.Fatal(err)
			}
			nc, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			ready := meta.FindStatusCondition(c.Status.Conditions, configapi.NetworkControllerConfigConditionReady)
			if ready == nil || ready.Reason != "CleanupEnrollmentPending" {
				t.Fatal("unenrolled Delete policy did not retain the exact enrollment fence")
			}

			if calls != 0 || c.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting || hasDiscoveryConfigFinalizer(c) || nc.Status.Configuration != nil {
				t.Fatal("input projection acquired public execution authority")
			}
			if _, err := p.requestFields(nc, c, source, 0); err == nil {
				t.Fatal("stale config status version reused projection")
			}
		})
	}
}

func TestEnvtestDiscoveryCredentialCreateObservation(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"complete", "mixed", "journal-changed", "projection-changed"} {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialCreateObservationScenario(t, mode, installWirelessFeatureQualificationAPI(t, api, "discovery-observer-"+mode))
		})
	}
}

func TestEnvtestDiscoveryCredentialAtomicCompletion(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"complete", "lost-ack", "conflict", "pruned-credentials", "pruned-action", "pruned-operation-versions", "pruned-action-versions"} {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialAtomicCompletionScenario(t, mode, installWirelessFeatureQualificationAPI(t, api, "discovery-receipt-"+mode))
		})
	}
}

func TestEnvtestDiscoveryCredentialPublicCycle(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"complete", "configmap", "mixed", "known-unknown", "lost-accepted-ack", "lost-permit-ack", "ambiguous-create", "unowned-existing", "pruned-pair", "wrong-native-id", "metadata-drift", "rotation", "controller-alias-addition", "preflight-replan"} {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialExecutorScenario(t, mode, installWirelessFeatureQualificationAPI(t, api, "discovery-public-"+mode), true)
		})
	}
}

func TestEnvtestDiscoveryCredentialPublicMultipleJobs(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"complete", "late-unowned", "composition", "composition-configmap", "composition-unknown", "composition-late-unowned", "composition-reclassify", "composition-replace"} {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialMultipleScenario(t, mode, installWirelessFeatureQualificationAPI(t, api, "discovery-multiple-"+mode))
		})
	}
}

func TestEnvtestDiscoveryCredentialLegacyEffectPromotion(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"complete", "lost-ack", "conflict", "pruned", "native-drift", "foreign-uuid", "active", "missing-versions", "unknown-version", "changed-mask", "changed-digest", "forged-public", "busy", "capacity"} {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialEffectPromotionScenario(t, mode, installWirelessFeatureQualificationAPI(t, api, "discovery-effect-"+mode))
		})
	}
}
