// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var credentialNoDispatchKinds = []string{"create", "metadata-create", "metadata-retain", "replacement", "prune", "config-cleanup"}
var credentialNoDispatchModes = []string{"refuse", "read-error", "conflict", "conflict-exhausted", "lost-clear-ack", "changed-owner", "changed-operation", "recorded-task", "recorded-credential", "ordinary-error", "ambiguous", "accepted", "foreign-proof"}

func TestDiscoveryCredentialNoDispatchProof(t *testing.T) {
	for _, kind := range credentialNoDispatchKinds {
		for _, mode := range credentialNoDispatchModes {
			t.Run(kind+"/"+mode, func(t *testing.T) { exerciseCredentialNoDispatchProof(t, kind, mode, nil) })
		}
	}
}

func exerciseCredentialNoDispatchProof(t *testing.T, kind, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	fields := map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1}
	values := map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`}
	if strings.HasPrefix(kind, "metadata-") {
		fields = map[string]any{"snmp_version": "v3", "snmp_mode": "NoAuthNoPriv", "snmp_user_name": "operator"}
		values = nil
	}
	raw := discoveryCredentialSource(t, fields, false)
	a, _, c, _, _ := discoveryProjectionFixture(t, raw, values)
	if install != nil {
		install(a, c)
	}
	if kind == "metadata-create" {
		protectCredentialFixtureController(t, a)
		posts := 0
		enrollPublicCredentialFixture(t, a, c, "inline", &posts)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, c)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := a.prepareDiscoveryCredentialProjection(nc, c, source)
	if err != nil {
		t.Fatal(err)
	}
	request, input, err := projection.bindRequest(nc, c, source, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	var creator *discoveryCredentialCreatorProof
	if kind != "create" && !strings.HasPrefix(kind, "metadata-") {
		f := newDiscoveryLineageFixture()
		f.create(t, request.versions, "")
		f.state.Owners[0].ConfigName, f.state.Owners[0].ConfigUID = c.Name, string(c.UID)
		r := &f.state.ActionApplications[0].Operation
		r.ConfigName, r.ConfigUID, r.ConfigGeneration, r.ControllerGeneration = c.Name, string(c.UID), c.Generation, nc.Generation
		f.state.CredentialApplications[0].ConfigUID = string(c.UID)
		nc.Status.Configuration = f.state
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
		history, err := discoveryCredentialLineages(f.state, string(c.UID))
		if err != nil {
			t.Fatal(err)
		}
		creator = history.latest[discoveryPublicDigest("fixture")]
	}
	baseline := nc.Status.Configuration.DeepCopy()
	if baseline == nil {
		baseline = &cvk.NetworkControllerConfigurationStatus{}
	}
	base, _, _ := discoveryCredentialInputParts(input)
	prune := discoveryCredentialPrunePrefix + strings.Repeat("b", 32) + ":" + strings.Repeat("c", 64) + ":" + featureIdentityDigest(raw)
	if creator != nil {
		prune = discoveryCredentialPrunePrefix + creator.receipt.Operation.ID + ":" + creator.digest() + ":" + featureIdentityDigest(raw)
	}
	identity := configurationResourceIdentity{credentials: request.versions}
	resource, action := input, "Create"
	switch kind {
	case "replacement":
		resource = discoveryCredentialRetirePrefix + creator.receipt.Operation.ID + ":" + creator.digest() + ":" + base
		action = "Delete"
		identity.target = creator.identity.id
	case "prune":
		resource = prune
		action = "Delete"
		identity = configurationResourceIdentity{target: creator.identity.id}
	case "config-cleanup":
		resource = "discovery-finalize:v2:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64) + ":" + strings.Repeat("c", 64) + ":" + discoveryCredentialCleanupSourceCommitment("", "", featureIdentityDigest(raw)) + ":" + prune
		action = "Delete"
		identity = configurationResourceIdentity{target: creator.identity.id}
	}
	admin := a.statusWriter
	if mode == "conflict" || mode == "conflict-exhausted" {
		a.statusWriter = &noDispatchConflictClient{Client: admin, always: mode == "conflict-exhausted"}
	}
	if mode == "lost-clear-ack" {
		a.statusWriter = &credentialNoDispatchClearAckClient{Client: admin}
	}
	var before *cvk.NetworkControllerConfigurationOperation
	var proof *configurationPreSubmitRefusal
	calls := 0
	write := &configurationWrite{resource: resource, action: action, identity: identity, submit: func(context.Context) (Task, error) {
		calls++
		switch mode {
		case "accepted":
			return Task{ID: "native-task"}, nil
		case "ambiguous":
			return Task{}, nil
		case "foreign-proof":
			foreign := &configurationWrite{submit: func(context.Context) (Task, error) { t.Fatal("foreign preflight reached mutation"); return Task{}, nil }}
			guardConfigurationPreflight(foreign, func(context.Context) error { return errConfigurationOwned })
			_, err := foreign.submit(ctx)
			return Task{}, err
		}
		return Task{}, errConfigurationOwned
	}}
	guardConfigurationPreflight(write, func(context.Context) error {
		now, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		before = now.Status.Configuration.Operation.DeepCopy()
		switch mode {
		case "ordinary-error", "ambiguous", "accepted", "foreign-proof":
			return nil
		case "changed-owner":
			now.Status.Configuration.Owners[0].ConfigUID = "foreign"
		case "changed-operation":
			now.Status.Configuration.Operation.SourceVersion = "changed"
		case "recorded-task":
			now.Status.Configuration.Operation.TaskID = "native-task"
		case "recorded-credential":
			versions := request.versions
			if strings.HasPrefix(kind, "metadata-") {
				versions = discoveryLineageVersions(1, 0)
			}
			now.Status.Configuration.CredentialApplications = append(now.Status.Configuration.CredentialApplications, cvk.NetworkControllerConfigurationCredentialApplication{Section: "inventory", ConfigUID: string(c.UID), OperationID: before.ID, Resource: input, TargetID: "owned-job", Versions: versions})
		case "read-error":
			return errInvalidResponse
		default:
			return errConfigurationOwned
		}
		if err := admin.Status().Update(ctx, now); err != nil {
			return err
		}
		return errConfigurationOwned
	})
	claim := write.noDispatch
	write.noDispatch = func(err error) *configurationPreSubmitRefusal { proof = claim(err); return proof }
	if !noDispatchWriteSupported("inventory", write) {
		t.Fatal("typed write did not enter the shared proof gate", kind)
	}
	err = a.submitConfigurationWrite(ctx, c, source, "inventory", write)
	a.statusWriter = admin
	after := journalState(t, a)
	retired := mode == "refuse" || mode == "read-error" || mode == "conflict" || mode == "lost-clear-ack"
	if before == nil {
		t.Fatal("test did not reach post-permit preflight", err)
	}
	if retired {
		if mode != "lost-clear-ack" && !errors.Is(err, errConfigurationNotDispatched) {
			t.Fatal("missing proven refusal", err)
		}
		if after.Operation != nil || calls != 0 || !reflect.DeepEqual(after.ActionApplications, baseline.ActionApplications) || !reflect.DeepEqual(after.CredentialApplications, baseline.CredentialApplications) || len(after.Owners) != 1 || after.Owners[0].ConfigUID != string(c.UID) || proof == nil {
			t.Fatal("retirement changed ownership, invented delivery or entered mutation", after, calls)
		}
		baseline := after.DeepCopy()
		if a.retireProvenNoDispatch(ctx, before, proof) == nil {
			t.Fatal("spent proof replayed")
		}
		if _, err := write.submit(ctx); err == nil {
			t.Fatal("consumed write replayed")
		}
		if !reflect.DeepEqual(baseline, journalState(t, a)) {
			t.Fatal("replayed proof changed retained state")
		}
		return
	}
	if after.Operation == nil || errors.Is(err, errConfigurationNotDispatched) {
		t.Fatal("unproven or attempted request was retired", mode, err)
	}
	if mode == "accepted" {
		if err != nil || after.Operation.Phase != "Accepted" || after.Operation.TaskID != "native-task" || calls != 1 {
			t.Fatal("accepted task lost", err)
		}
	} else if mode == "ordinary-error" || mode == "ambiguous" || mode == "foreign-proof" {
		if after.Operation.Phase != "Unknown" || after.Operation.TaskID != "" || calls != 1 || proof != nil {
			t.Fatal("request outcome acquired a foreign or inferred no-dispatch proof")
		}
	} else if calls != 0 {
		t.Fatal("refused preflight entered native mutation")
	}
}

type credentialNoDispatchClearAckClient struct {
	ctrlclient.Client
	hits int
}

func (c *credentialNoDispatchClearAckClient) Status() ctrlclient.SubResourceWriter {
	return &credentialNoDispatchClearAckWriter{SubResourceWriter: c.Client.Status(), owner: c}
}

type credentialNoDispatchClearAckWriter struct {
	ctrlclient.SubResourceWriter
	owner *credentialNoDispatchClearAckClient
}

func (w *credentialNoDispatchClearAckWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if nc, ok := obj.(*cvk.NetworkController); ok && nc.Status.Configuration != nil && nc.Status.Configuration.Operation == nil && w.owner.hits == 0 {
		w.owner.hits++
		return errors.New("lost no-dispatch slot-clear acknowledgement")
	}
	return nil
}
