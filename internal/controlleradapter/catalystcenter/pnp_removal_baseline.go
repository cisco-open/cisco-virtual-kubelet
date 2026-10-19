// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"reflect"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

const pnpRecordPrefix = "pnp-record:"

type pnpRemovalBaseline struct {
	ID, Serial, CanonicalDigest, NativeDigest string
}

// Legacy records remain valid registrations, but cannot supply omitted-source
// cleanup identity. Enrichment appends canonical facts to the same UUID result;
// the original Create operation and VerifiedAt are never rewritten.
func decodePnPRecord(resource string) (pnpRemovalBaseline, error) {
	var out pnpRemovalBaseline
	if !strings.HasPrefix(resource, pnpRecordPrefix) || len(resource) > 512 {
		return out, errConfigurationOwned
	}
	parts := strings.Split(strings.TrimPrefix(resource, pnpRecordPrefix), ":")
	if len(parts) != 1 && len(parts) != 6 || !validAPIID(parts[0]) {
		return out, errConfigurationOwned
	}
	out.ID = parts[0]
	if len(parts) == 1 {
		return out, nil
	}
	if parts[1] != "removal" || parts[2] != "v1" || !validTemplateComparisonDigest(parts[4]) || !validTemplateComparisonDigest(parts[5]) {
		return out, errConfigurationOwned
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != parts[3] {
		return out, errConfigurationOwned
	}
	serial, err := topologyidentity.CanonicalPhysicalIdentity(string(raw))
	if err != nil || serial != string(raw) {
		return out, errConfigurationOwned
	}
	out.Serial, out.CanonicalDigest, out.NativeDigest = serial, parts[4], parts[5]
	return out, nil
}

func (b pnpRemovalBaseline) resource() (string, error) {
	raw := pnpRecordPrefix + b.ID + ":removal:v1:" + base64.RawURLEncoding.EncodeToString([]byte(b.Serial)) + ":" + b.CanonicalDigest + ":" + b.NativeDigest
	parsed, err := decodePnPRecord(raw)
	if err != nil || parsed != b {
		return "", errConfigurationOwned
	}
	return raw, nil
}

func pnpCanonicalRegistrationDigest(want pnpDeviceIntent) (string, string, error) {
	serials, err := validatePnPDeviceIntent(want)
	if err != nil || len(serials) != 1 {
		return "", "", errConfigurationTransition
	}
	want.Serial = serials[0]
	body, err := pnpRegistrationPayload(want)
	if err != nil {
		return "", "", err
	}
	return serials[0], fmt.Sprintf("%x", sha256.Sum256(body)), nil
}

// Observe an original, source-bound owned registration before retaining any
// source-independent cleanup identity. This is a read/status-only stage, not a
// Delete permit. No source or native-identity reconstruction is attempted.
func (a *adapter) retainOwnedPnPRemovalBaseline(ctx context.Context, c *configapi.NetworkControllerConfig, s *configSourceSnapshot, want pnpDeviceIntent) (bool, error) {
	if a == nil || !supportedPnPRegistrationConfig(c) {
		return false, errConfigurationTransition
	}
	c = c.DeepCopy()
	if s != nil {
		copy := *s
		copy.raw = append([]byte(nil), s.raw...)
		s = &copy
	}
	if err := declaredPnPRegistration(c, s, want); err != nil {
		return false, err
	}
	serial, canonical, err := pnpCanonicalRegistrationDigest(want)
	if err != nil {
		return false, err
	}
	if err := a.checkConfigurationSnapshot(ctx, c, s); err != nil {
		return false, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return false, err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil {
		return false, errConfigurationBusy
	}
	if err := configurationOwnersMatch(nc.Status.Configuration, c); err != nil {
		return false, err
	}
	original, err := ownedPnPRegistrationReceipt(nc, c, want)
	if err != nil {
		return false, err
	}
	if original == nil {
		return false, errConfigurationOwned
	}
	index := -1
	for i, r := range nc.Status.Configuration.ActionApplications {
		if r.Operation.ID == original.ID {
			if index != -1 {
				return false, errConfigurationOwned
			}
			index = i
		}
	}
	if index < 0 {
		return false, errConfigurationOwned
	}
	previous := nc.Status.Configuration.ActionApplications[index].DeepCopy()
	retained, err := decodePnPRecord(previous.Resource)
	if err != nil {
		return false, err
	}
	authority := func(ctx context.Context) error {
		if err := a.checkConfigurationSnapshot(ctx, c, s); err != nil {
			return err
		}
		fresh, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if fresh.UID != nc.UID || fresh.Generation != nc.Generation || fresh.ResourceVersion != nc.ResourceVersion || !reflect.DeepEqual(fresh.Status.Configuration, nc.Status.Configuration) {
			return errConfigurationStale
		}
		return nil
	}
	plan, err := a.client.planPnPRegistrationRemoval(ctx, want, original, authority)
	if err != nil {
		return false, err
	}
	if err := plan.observeUnchanged(ctx); err != nil {
		return false, err
	}
	baseline := pnpRemovalBaseline{ID: original.CreatedResourceID, Serial: serial, CanonicalDigest: canonical, NativeDigest: plan.snapshotDigest}
	if retained.Serial != "" {
		if retained != baseline {
			return false, errConfigurationOwned
		}
		return false, nil
	}
	resource, err := baseline.resource()
	if err != nil {
		return false, err
	}
	updated := nc.DeepCopy()
	updated.Status.Configuration.ActionApplications[index].Resource = resource
	if err := pnpRegistrationReceiptBytesFit(updated.Status.Configuration.ActionApplications); err != nil {
		return false, err
	}
	if !pnpRegistrationReceiptPresent(updated.Status.Configuration, original) {
		return false, errConfigurationOwned
	}
	// Same existing controller status CAS. Unrelated claims/receipts and the
	// original creator operation/time remain unchanged; no second row is added.
	if err := a.statusWriter.Status().Update(ctx, updated); err != nil {
		return false, err
	}
	stored, err := a.currentController(ctx)
	if err != nil {
		return false, err
	}
	if !reflect.DeepEqual(stored.Status.Configuration, updated.Status.Configuration) {
		return false, errConfigurationStale
	}
	return true, nil
}
