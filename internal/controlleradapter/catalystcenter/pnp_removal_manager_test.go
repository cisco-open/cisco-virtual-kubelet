// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"strings"
	"testing"

	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
)

var pnpManagerBindingModes = []string{"valid", "wrong-uid", "later-target", "wrong-creator", "noncanonical-baseline", "foreign-owner", "wrong-source", "wrong-inline", "submitted", "legacy-protocol", "physical-target", "no-prune", "missing-creator"}

func TestPnPRemovalManagerBinding(t *testing.T) {
	for _, mode := range pnpManagerBindingModes {
		t.Run(mode, func(t *testing.T) { exercisePnPManagerBinding(t, mode, nil) })
	}
}
func exercisePnPManagerBinding(t *testing.T, mode string, install func(*adapter, *config.NetworkControllerConfig)) {
	exercisePnPRemovalCohort(t, "cycle", install, func(a *adapter, c *config.NetworkControllerConfig, s *configSourceSnapshot, w *configurationWrite) {
		ctx := context.Background()
		if _, err := a.prepareConfiguration(ctx, c, s, "inventory", w.resource, w.action, w.identity); err != nil {
			t.Fatal(err)
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		nc = nc.DeepCopy()
		c = c.DeepCopy()
		source := controlleradapter.ConfigurationMetadataSource{Raw: append([]byte(nil), s.raw...), UID: string(s.uid), Version: s.version}
		op := nc.Status.Configuration.Operation
		switch mode {
		case "wrong-uid":
			op.ConfigUID = "other"
		case "later-target":
			op.TargetID = "zzz-id"
			op.Resource = pnpRemovalJournalPrefix + op.TargetID + ":" + featureIdentityDigest(nc.Status.Configuration.ActionApplications[1])
		case "wrong-creator":
			op.Resource = pnpRemovalJournalPrefix + op.TargetID + ":" + strings.Repeat("f", 64)
		case "noncanonical-baseline":
			nc.Status.Configuration.ActionApplications[1].Resource = pnpRecordPrefix + "zzz-id"
		case "foreign-owner":
			nc.Status.Configuration.Owners[0].ConfigUID = "other"
		case "wrong-source":
			source.UID = "foreign"
			source.Version = "1"
		case "wrong-inline":
			source.Raw = append(source.Raw, ' ')
		case "submitted":
			op.Phase = "Submitting"
		case "legacy-protocol":
			op.Resource = "pnp-remove:v1:aaa-id:" + strings.Repeat("f", 64)
		case "physical-target":
			op.TargetSerial = "foc12345678"
		case "no-prune":
			c.Spec.PrunePolicy = config.NetworkControllerRetentionPolicyRetain
		case "missing-creator":
			nc.Status.Configuration.ActionApplications = nc.Status.Configuration.ActionApplications[1:]
		}
		ncBefore, cBefore := nc.DeepCopy(), c.DeepCopy()
		rawBefore := append([]byte(nil), source.Raw...)
		scope := controlleradapter.ConfigurationMetadataScopeFor(TypeName)
		if scope == nil || !scope.Selected(nc) {
			t.Fatal("PnP removal scope not registered/selected")
		}
		serials, err := scope.PhysicalIdentities(nc, c, source)
		if (err == nil) != (mode == "valid") {
			t.Fatal("incorrect manager projection", err, serials)
		}
		if mode == "valid" && !reflect.DeepEqual(serials, []string{"foc12345678", "foc87654321"}) {
			t.Fatal("did not bind entire remaining creator cohort", serials)
		}
		if !reflect.DeepEqual(nc, ncBefore) || !reflect.DeepEqual(c, cBefore) || !reflect.DeepEqual(source.Raw, rawBefore) {
			t.Fatal("pure manager projection changed caller data")
		}
		other := nc.DeepCopy()
		other.Spec.Type = "nexus-dashboard"
		if scope.Selected(other) {
			t.Fatal("ND selected by Catalyst Center callback")
		}
		var fresh cvk.NetworkController
		if err := a.statusReader.Get(ctx, a.key, &fresh); err != nil {
			t.Fatal(err)
		}
		if fresh.Status.Configuration.Operation.Phase != "Prepared" || fresh.Status.Configuration.Operation.TargetSerial != "" {
			t.Fatal("projection granted dispatch or manufactured physical target")
		}
	})
}
