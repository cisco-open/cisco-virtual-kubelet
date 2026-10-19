// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var credentialCustodyCancellationModes = append(append([]string{}, credentialUnsubmittedModes...), "drain-prepared-returned-archive", "drain-prepared-missing-seal", "drain-prepared-archive-loss", "drain-prepared-secret-replan", "drain-prepared-retained-native-return")

func TestDiscoveryCredentialCustodyCancellation(t *testing.T) {
	for _, mode := range credentialCustodyCancellationModes {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialCleanupObservationScenario(t, "handoff-observe/custody-execute-cancel-"+mode, nil)
		})
	}
}

func exerciseDiscoveryCredentialCustodyCancellation(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, mode string, cm *core.ConfigMap, admin ctrlclient.Client, realAPI bool) {
	t.Helper()
	ctx := context.Background()
	before := journalState(t, a).DeepCopy()
	key := ctrlclient.ObjectKeyFromObject(c)
	if before.Operation == nil || before.Operation.Phase != "Prepared" || before.Operation.SubmittedAt != nil || before.Operation.TaskID != "" {
		t.Fatal("no exact never-submitted successor")
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, ref := range c.Spec.SecretRefs {
		for _, src := range nc.Spec.IntentSecretSources {
			if ref.Source != src.Alias {
				continue
			}
			relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: c.Name, Section: ref.Section, JSONPointer: ref.Path, SourceAlias: ref.Source, SecretName: src.Name, SecretKey: src.Key})
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(ref.Path, "/password_list") {
				paths["passwordList"] = filepath.Join(a.intentSecretPath, relative)
			}
		}
	}
	if err := admin.Delete(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := admin.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	posts, deletes, gets := 0, 0, 0
	// Reuse all original cancellation/fault/manager assertions with a nonempty
	// archive. Stop before its optional next-owner exercise to check the seal.
	baseMode := mode
	if mode == "drain-prepared-handoff" {
		baseMode = "drain-prepared"
	}
	exerciseUnsubmittedCredentialDeletion(t, a, c, baseMode, cm, paths, &posts, &deletes, &gets, realAPI)
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if nc.Status.Configuration.Operation != nil || hasDiscoveryConfigFinalizer(c) {
		return
	}
	record, history, err := discoveryCredentialRetiredHandoffRecord(nc)
	if err != nil || record.CredentialHistoryDigest != discoveryCredentialHistorySealPrefix+discoveryCredentialAuditDigest(nc.Status.Configuration, "") || len(history.creators) != 0 || len(history.archives) != 1 || !reflect.DeepEqual(record.UnsubmittedOperation, before.Operation) || !reflect.DeepEqual(nc.Status.Configuration.ActionApplications, before.ActionApplications) || !reflect.DeepEqual(nc.Status.Configuration.CredentialApplications, before.CredentialApplications) {
		t.Fatal("sealed cancellation lost its witness/archive or invented delivery", err)
	}
	for _, damage := range []string{"seal", "archive", "witness", "scope", "evidence"} {
		copy := nc.DeepCopy()
		cond := meta.FindStatusCondition(copy.Status.Conditions, discoveryRetirementCondition)
		var r discoveryOwnershipRetirement
		json.Unmarshal([]byte(cond.Message), &r)
		switch damage {
		case "seal":
			r.CredentialHistoryDigest = ""
		case "archive":
			copy.Status.Configuration.ActionApplications = nil
			copy.Status.Configuration.CredentialApplications = nil
		case "witness":
			r.UnsubmittedOperation.TaskID = "unproven"
		case "scope":
			r.Completion.ScopeDigest = strings.Repeat("a", 64)
		case "evidence":
			r.Completion.EvidenceDigest = strings.Repeat("a", 64)
		}
		raw, _ := json.Marshal(r)
		cond.Message = string(raw)
		if _, _, err := discoveryCredentialRetiredHandoffRecord(copy); err == nil {
			t.Fatal("damaged no-submission proof authorized handoff", damage)
		}
	}
	if mode == "drain-prepared-handoff" {
		a.statusWriter = admin
		exerciseDiscoveryCredentialHandoffObservation(t, a, c, "custody-public-cleanup-inline", realAPI)
		state := journalState(t, a)
		if len(state.CredentialApplications) != 2 || state.CredentialApplications[1].Versions[0].Version != 2 || len(state.ActionApplications) != 4 {
			t.Fatal("cancelled queued version was treated as delivered or lost lineage")
		}
	}
}
