// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var credentialCustodyModes = []string{"complete", "configmap", "same-name", "lost-ack", "rejected-write", "source-race", "config-race", "projection-lost", "secret-bytes-change", "native-return", "journal-race", "legacy-seal", "forged-observation", "controller-change", "unresolved-snapshot"}

func TestDiscoveryCredentialCustodyPublication(t *testing.T) {
	for _, mode := range credentialCustodyModes {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialCleanupObservationScenario(t, "handoff-observe/custody-"+mode, nil)
		})
	}
}

type credentialCustodyFaultClient struct {
	ctrlclient.Client
	mode   string
	writes *int
}
type credentialCustodyFaultWriter struct {
	ctrlclient.SubResourceWriter
	owner credentialCustodyFaultClient
}

func (c credentialCustodyFaultClient) Status() ctrlclient.SubResourceWriter {
	return credentialCustodyFaultWriter{c.Client.Status(), c}
}
func (w credentialCustodyFaultWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	nc, ok := obj.(*cvk.NetworkController)
	if !ok {
		return errors.New("custody writer can update controller status only")
	}
	cond := meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition)
	var record discoveryOwnershipRetirement
	if cond == nil || json.Unmarshal([]byte(cond.Message), &record) != nil || record.CredentialCustody == nil {
		return errors.New("unexpected custody status write")
	}
	*w.owner.writes++
	if w.owner.mode == "rejected-write" && *w.owner.writes == 1 {
		return errors.New("rejected private custody CAS")
	}
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if w.owner.mode == "lost-ack" {
		return errors.New("lost custody CAS acknowledgement")
	}
	return nil
}

func exerciseDiscoveryCredentialCustody(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, source *configSourceSnapshot, observed *discoveryCredentialHandoffObservation, mode string, cm *core.ConfigMap, projected string, returnNative func(), mutations *int, realAPI bool) {
	t.Helper()
	executionMode := ""
	if strings.HasPrefix(mode, "execute-") {
		executionMode = strings.TrimPrefix(mode, "execute-")
		mode = "complete"
	}
	ctx := context.Background()
	admin := a.statusWriter
	before, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	configBefore := c.DeepCopy()
	switch mode {
	case "forged-observation":
		copy := *observed
		copy.InputsDigest = strings.Repeat("a", 64)
		observed = &copy
	case "controller-change":
		copy := before.DeepCopy()
		copy.Generation++
		copy.Spec.Connection.MaxConcurrentRequests = 8
		if err := admin.Update(ctx, copy); err != nil {
			t.Fatal(err)
		}
		a.generation = copy.Generation
	case "unresolved-snapshot":
		source.secrets = nil
	case "source-race":
		cm.Data["model"] += " "
		if err := admin.Update(ctx, cm); err != nil {
			t.Fatal(err)
		}
	case "config-race":
		copy := c.DeepCopy()
		copy.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
		if err := admin.Update(ctx, copy); err != nil {
			t.Fatal(err)
		}
	case "projection-lost":
		if err := os.Remove(projected); err != nil {
			t.Fatal(err)
		}
	case "secret-bytes-change":
		if err := os.WriteFile(projected, versionedSecretFixture(t, 2, `["changed-under-same-version"]`), 0600); err != nil {
			t.Fatal(err)
		}
	case "native-return":
		returnNative()
	case "journal-race":
		copy := before.DeepCopy()
		copy.Status.Configuration.CredentialApplications = nil
		if err := admin.Status().Update(ctx, copy); err != nil {
			t.Fatal(err)
		}
	case "legacy-seal":
		copy := before.DeepCopy()
		cond := meta.FindStatusCondition(copy.Status.Conditions, discoveryRetirementCondition)
		var record discoveryOwnershipRetirement
		json.Unmarshal([]byte(cond.Message), &record)
		record.CredentialHistoryDigest = ""
		raw, _ := json.Marshal(record)
		cond.Message = string(raw)
		if err := admin.Status().Update(ctx, copy); err != nil {
			t.Fatal(err)
		}
		// Legacy closure remains observable, but absence of a seal must not
		// authorize the new custody publication even with a fresh observation.
		var err error
		observed, err = a.observeDiscoveryCredentialOwnershipHandoff(ctx, c, source)
		if err != nil {
			t.Fatal(err)
		}
	}
	writes := 0
	a.statusWriter = credentialCustodyFaultClient{statusOnlyFeatureClient{Client: admin}, mode, &writes}
	defer func() { a.statusWriter = admin }()
	err = a.publishDiscoveryCredentialCustody(ctx, c, source, observed)
	success := mode == "complete" || mode == "configmap" || mode == "same-name" || mode == "lost-ack" || mode == "rejected-write"
	if mode == "rejected-write" {
		if err == nil || writes != 1 {
			t.Fatal("rejected CAS not exercised")
		}
		after, readErr := a.currentController(ctx)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !reflect.DeepEqual(after.Status, before.Status) {
			t.Fatal("rejected CAS partially transferred ownership")
		}
		err = a.publishDiscoveryCredentialCustody(ctx, c, source, observed)
	}
	if mode == "lost-ack" {
		if err == nil || writes != 1 {
			t.Fatal("lost custody acknowledgement not exercised")
		}
	} else if (err == nil) != success {
		t.Fatal("unexpected custody result", mode, err)
	}
	after, readErr := a.currentController(ctx)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if *mutations != 0 {
		t.Fatal("custody publication attempted native mutation")
	}
	if !success {
		if writes != 0 || !reflect.DeepEqual(after.Status.Configuration.Owners, before.Status.Configuration.Owners) {
			t.Fatal("unverified custody changed ownership")
		}
		return
	}
	owners := map[string]bool{}
	for _, row := range before.Status.Configuration.ActionApplications {
		owners[row.Operation.ConfigUID] = true
	}
	record, cohort, err := discoveryCredentialCustodyRecord(after, c, source)
	if err != nil || cohort == nil || len(cohort.archived) != len(owners) || len(cohort.current.creators) != 0 || record.CredentialCustody.PreviousRetirementDigest != observed.RetirementDigest {
		t.Fatal("committed custody could not be independently read", err)
	}
	if !reflect.DeepEqual(after.Status.Configuration.ActionApplications, before.Status.Configuration.ActionApplications) || !reflect.DeepEqual(after.Status.Configuration.CredentialApplications, before.Status.Configuration.CredentialApplications) || after.Status.Configuration.Operation != nil {
		t.Fatal("custody rewrote receipt/version/task history")
	}
	if len(after.Status.Configuration.Owners) != 1 || after.Status.Configuration.Owners[0].ConfigUID != string(c.UID) || after.Status.Configuration.Owners[0].ConfigName != c.Name {
		t.Fatal("wrong successor reservation")
	}
	var unchanged configapi.NetworkControllerConfig
	if err := admin.Get(ctx, ctrlclient.ObjectKeyFromObject(c), &unchanged); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(unchanged.Spec, configBefore.Spec) || !reflect.DeepEqual(unchanged.Finalizers, configBefore.Finalizers) || unchanged.ResourceVersion != configBefore.ResourceVersion {
		t.Fatal("worker mutated config or finalizer")
	}
	count := writes
	if err := a.publishDiscoveryCredentialCustody(ctx, c, source, observed); err == nil || writes != count {
		t.Fatal("old observation replayed the ownership CAS")
	}
	for _, change := range []string{"version", "target", "generation", "spec", "source", "inputs", "previous-proof", "archive-seal", "old-pair", "owner", "controller", "condition"} {
		copy := after.DeepCopy()
		cond := meta.FindStatusCondition(copy.Status.Conditions, discoveryRetirementCondition)
		var r discoveryOwnershipRetirement
		json.Unmarshal([]byte(cond.Message), &r)
		switch change {
		case "version":
			r.CredentialCustody.Version = "future/v99"
		case "target":
			r.CredentialCustody.ConfigUID = "foreign"
		case "generation":
			r.CredentialCustody.ConfigGeneration++
		case "spec":
			r.CredentialCustody.SpecDigest = strings.Repeat("a", 64)
		case "source":
			r.CredentialCustody.SourceUID = "forged"
		case "inputs":
			r.CredentialCustody.InputsDigest = "invalid"
		case "previous-proof":
			r.CredentialCustody.PreviousRetirementDigest = strings.Repeat("a", 64)
		case "archive-seal":
			r.CredentialHistoryDigest = discoveryCredentialHistorySealPrefix + strings.Repeat("a", 64)
		case "old-pair":
			if len(copy.Status.Configuration.CredentialApplications) == 0 {
				copy.Status.Configuration.CredentialApplications = append(copy.Status.Configuration.CredentialApplications, cvk.NetworkControllerConfigurationCredentialApplication{ConfigUID: string(c.UID), Resource: "unproven"})
			} else {
				copy.Status.Configuration.CredentialApplications = nil
			}
		case "owner":
			copy.Status.Configuration.Owners[0].ConfigUID = "foreign"
		case "controller":
			copy.Generation++
		case "condition":
			cond.ObservedGeneration++
		}
		raw, _ := json.Marshal(r)
		cond.Message = string(raw)
		if _, _, err := discoveryCredentialCustodyRecord(copy, c, source); err == nil {
			t.Fatal("altered custody accepted", change)
		}
	}
	if _, err := decodeDiscoveryRetirement(after); err == nil {
		t.Fatal("ordinary handoff consumed credential custody")
	}
	if _, err := discoveryCredentialLineages(after.Status.Configuration, string(c.UID)); err == nil && len(cohort.archived) > 0 {
		t.Fatal("private custody enabled unqualified normal execution")
	}
	if executionMode != "" {
		a.statusWriter = admin
		exerciseDiscoveryCredentialCustodyExecution(t, a, c, executionMode, cm, admin, realAPI)
	}
}
