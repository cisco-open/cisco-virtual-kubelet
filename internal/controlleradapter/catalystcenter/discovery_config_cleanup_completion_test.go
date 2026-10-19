// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var discoveryCompletionModes = []string{"closed", "nil-state", "empty-state", "owner-empty", "unowned-name", "foreign-owner", "configmap", "create-left", "reappeared", "malformed", "duplicate", "foreign-alias", "active-operation", "foreign-section", "missing-source", "mixed-source", "evidence-race", "native-race", "spec-race", "source-race"}

func TestDiscoveryConfigCleanupCompletion(t *testing.T) {
	for _, mode := range discoveryCompletionModes {
		t.Run(mode, func(t *testing.T) { exerciseDiscoveryCompletion(t, mode, nil) })
	}
}

func exerciseDiscoveryCompletion(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, c, world := discoveryPruneFixture(t)
	if install != nil {
		install(a, c)
	}
	c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	c.Finalizers = []string{discoveryConfigFinalizer, "test.cisco.vk/completion-keeper"}
	var cm *core.ConfigMap
	if mode == "configmap" || mode == "source-race" {
		cm = &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "completion-source", Namespace: c.Namespace}, Data: map[string]string{"model": emptyDiscoveryModel}}
		if install == nil {
			cm.UID = "completion-source"
		}
		if err := a.statusWriter.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
		c.Spec.Source.Inline = nil
		c.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}
	}
	if mode == "unowned-name" {
		c.Spec.Source.Inline.Raw = discoveryModel()
	}
	if mode == "missing-source" {
		c.Spec.Source.Inline = nil
		c.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: "missing", Key: "model"}
	}
	if mode == "mixed-source" {
		c.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"discovery":[],"tags":[{"name":"foreign"}]}}}`)
	}
	if err := a.statusWriter.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := a.statusWriter.Delete(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := &nc.Status.Configuration.ActionApplications[0]
	r.Operation.Action = "Delete"
	r.Operation.TargetID = "42"
	r.Operation.Resource += discoveryPruneSuffix
	delete(world.jobs, "fixture")
	switch mode {
	case "nil-state":
		nc.Status.Configuration = nil
	case "empty-state":
		nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{}
	case "owner-empty":
		nc.Status.Configuration.ActionApplications = nil
	case "unowned-name", "foreign-owner":
		nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
		nc.Status.Configuration.Owners[0].ConfigName = "foreign"
		r.Operation.ConfigUID = "foreign"
		r.Operation.ConfigName = "foreign"
		r.Operation.Action = "Create"
		r.Operation.TargetID = ""
		// This record is an ordinary foreign Create, not a finalized tombstone.
		r.Operation.Resource = ownedDiscoveryResource("fixture", map[string]any{"name": "fixture", "discoveryType": "SINGLE", "ipAddressList": "192.0.2.1", "protocolOrder": "SSH", "globalCredentialIdList": []string{"credential-id"}})
		row := world.jobs["foreign"]
		row.Name = "fixture"
		row.ID = "42"
		world.jobs["fixture"] = row
		if mode == "unowned-name" {
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{}
		}
	case "create-left":
		r.Operation.Action = "Create"
		r.Operation.TargetID = ""
		r.Operation.Resource = r.Operation.Resource[:len(r.Operation.Resource)-len(discoveryPruneSuffix)]
	case "reappeared":
		row := world.jobs["foreign"]
		row.Name = "fixture"
		row.ID = "42"
		world.jobs["fixture"] = row
	case "malformed":
		r.Operation.Phase = "Unknown"
	case "duplicate":
		other := *r.DeepCopy()
		other.Resource = ownedDiscoveryReceiptKey("fixture", "other-id")
		other.Operation.TargetID = "other-id"
		nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, other)
	case "foreign-alias":
		other := *r.DeepCopy()
		other.Operation.ConfigUID = "foreign"
		other.Operation.ConfigName = "foreign"
		other.Resource = ownedDiscoveryReceiptKey("foreign", "42")
		other.Operation.Resource = strings.Replace(other.Operation.Resource, discoveryPublicDigest("fixture"), discoveryPublicDigest("foreign"), 1)
		nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, other)
	case "active-operation":
		nc.Status.Configuration.Operation = r.Operation.DeepCopy()
		nc.Status.Configuration.Operation.Phase = "Unknown"
	case "foreign-section":
		nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, cvk.NetworkControllerSectionOwner{Section: "sites", ConfigUID: string(c.UID), ConfigName: c.Name})
	}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	reads := 0
	rest, _, err := a.client.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	rest.HTTPClient.Transport = discoveryCleanupRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.Method == "GET" && req.URL.Path == discoveryPath+"/1/100" {
			reads++
			if reads == 2 {
				switch mode {
				case "evidence-race":
					current, _ := a.currentController(ctx)
					current.Status.Configuration.Owners[0].ConfigName = "foreign"
					if err := a.statusWriter.Status().Update(ctx, current); err != nil {
						t.Fatal(err)
					}
				case "native-race":
					row := world.jobs["foreign"]
					row.Name = "fixture"
					row.ID = "42"
					world.jobs["fixture"] = row
				case "spec-race":
					copy := c.DeepCopy()
					copy.Spec.ModelSource.Exporter = "changed"
					if err := a.statusWriter.Update(ctx, copy); err != nil {
						t.Fatal(err)
					}
				case "source-race":
					cm.Data["model"] += " "
					if err := a.statusWriter.Update(ctx, cm); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		return base.RoundTrip(req)
	})
	before := nc.Status.Configuration.DeepCopy()
	proof, err := a.observeDiscoveryConfigCompletion(ctx, c)
	valid := mode == "closed" || mode == "nil-state" || mode == "empty-state" || mode == "owner-empty" || mode == "unowned-name" || mode == "foreign-owner" || mode == "configmap"
	if (err == nil) != valid || (proof != nil) != valid {
		t.Fatal("incorrect completion observation", proof, err)
	}
	if len(world.writes) != 0 {
		t.Fatal("completion invoked native writer")
	}
	if !valid {
		return
	}
	if reads != 2 {
		t.Fatal("completion did not repeat native observation")
	}
	if !reflect.DeepEqual(before, journalState(t, a)) {
		t.Fatal("completion changed ownership/journal")
	}
	if discoveryDeletionCompleted(nc, c) {
		t.Fatal("metadata completion preceded worker proof")
	}
	if err := a.publishDiscoveryConfigCompletion(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !discoveryDeletionCompleted(nc, c) || len(c.Finalizers) != 2 || !reflect.DeepEqual(before, nc.Status.Configuration) || len(world.writes) != 0 {
		t.Fatal("bound status proof/finalizer isolation failed")
	}
	for _, mutation := range []string{"config-uid", "generation", "spec", "finalizer", "message", "controller-uid", "controller-generation", "evidence"} {
		t.Run(mutation, func(t *testing.T) {
			config, current := c.DeepCopy(), nc.DeepCopy()
			switch mutation {
			case "config-uid":
				config.UID = "foreign"
			case "generation":
				config.Generation++
			case "spec":
				config.Spec.ModelSource.Exporter = "changed"
			case "finalizer":
				config.Finalizers = nil
			case "message":
				config.Status.Conditions[0].Message += " "
			case "controller-uid":
				current.UID = "foreign"
			case "controller-generation":
				current.Generation++
			case "evidence":
				if current.Status.Configuration == nil {
					current.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{}
				} else {
					current.Status.Configuration.Owners = append(current.Status.Configuration.Owners, cvk.NetworkControllerSectionOwner{Section: "sites", ConfigUID: "other", ConfigName: "other"})
				}
			}
			if discoveryDeletionCompleted(current, config) {
				t.Fatal("changed completion proof accepted")
			}
		})
	}
}
