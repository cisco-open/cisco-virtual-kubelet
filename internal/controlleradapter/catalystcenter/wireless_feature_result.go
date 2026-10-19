// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Nonsecret native receipt reference. This is not durable ownership and grants
// neither submission nor deletion authority.
type wirelessFeatureTaskReference struct {
	ID        string    `json:"taskID"`
	Name      string    `json:"name"`
	Action    string    `json:"action"`
	Target    string    `json:"targetID,omitempty"`
	Submitted time.Time `json:"submittedAt"`
}

func canonicalWirelessFeatureUUID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}
func validWirelessFeatureTaskReference(ref wirelessFeatureTaskReference, now time.Time) bool {
	if !validAPIID(ref.ID) || !validProfileName(ref.Name) || len(ref.Name) > 64 || strings.ContainsAny(ref.Name, "%&<>[]'/") || ref.Submitted.IsZero() || now.Before(ref.Submitted) {
		return false
	}
	return ref.Action == "Create" && ref.Target == "" || (ref.Action == "Update" || ref.Action == "Delete") && canonicalWirelessFeatureUUID(ref.Target)
}
func decodeWirelessFeatureTaskResult(raw []byte, ref wirelessFeatureTaskReference, now time.Time) (string, bool, error) {
	if !validWirelessFeatureTaskReference(ref, now) {
		return "", false, errConfigurationTransition
	}
	done, err := decodeConfigurationTaskComplete(raw, ref.ID, ref.Submitted, now)
	if err != nil || !done {
		return "", false, err
	}
	var body struct {
		Response struct {
			Service  string `json:"serviceType"`
			Progress string `json:"progress"`
			Data     string `json:"data"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Response.Service != "NCNC" || !canonicalWirelessFeatureUUID(body.Response.Data) {
		return "", false, errInvalidResponse
	}
	action := map[string]string{"Create": "created", "Update": "updated", "Delete": "deleted"}[ref.Action]
	progress := "Successfully " + action + " model config design with name " + ref.Name
	if ref.Action == "Delete" {
		progress = "Successfully deleted model config design with id " + ref.Target
	}
	if body.Response.Progress != progress || ref.Target != "" && ref.Target != body.Response.Data {
		return "", false, errInvalidResponse
	}
	return body.Response.Data, true, nil
}
func (c *client) wirelessFeatureTaskResult(ctx context.Context, ref wirelessFeatureTaskReference, now time.Time) (string, bool, error) {
	if !validWirelessFeatureTaskReference(ref, now) {
		return "", false, errConfigurationTransition
	}
	raw, err := c.get(ctx, taskPath+url.PathEscape(ref.ID), nil, http.StatusOK)
	if err != nil {
		return "", false, err
	}
	return decodeWirelessFeatureTaskResult(raw, ref, now)
}

// Require task-returned identity, selected native kind/name and desired fields
// together. Matching metadata or a same-name replacement cannot become a result.
// Callers must separately bind/persist owner/source/operation evidence before
// using a result as ownership; this read-only verifier never publishes a receipt.
func verifyWirelessFeatureCreatedResult[T any](ctx context.Context, c *client, kind string, ref wirelessFeatureTaskReference, want T, read func(context.Context, wirelessFeatureInstance) (T, error), matches func(T, T) bool, now time.Time) (string, bool, error) {
	if ref.Action != "Create" {
		return "", false, errConfigurationTransition
	}
	return verifyWirelessFeatureAppliedResult(ctx, c, kind, ref, want, read, matches, now)
}

func verifyWirelessFeatureAppliedResult[T any](ctx context.Context, c *client, kind string, ref wirelessFeatureTaskReference, want T, read func(context.Context, wirelessFeatureInstance) (T, error), matches func(T, T) bool, now time.Time) (string, bool, error) {
	if c == nil || read == nil || matches == nil || ref.Action != "Create" && ref.Action != "Update" || kind != wirelessFRAKind && kind != wirelessCleanAirKind {
		return "", false, errConfigurationTransition
	}
	id, done, err := c.wirelessFeatureTaskResult(ctx, ref, now)
	if err != nil || !done {
		return "", false, err
	}
	instances, err := c.wirelessFeatureInstances(ctx, kind)
	if err != nil {
		return "", false, err
	}
	instance, exists := instances[ref.Name]
	if !exists {
		return "", false, nil
	}
	if instance.ID != id || *instance.System {
		return "", false, errConfigurationOwned
	}
	got, err := read(ctx, instance)
	if err != nil || !matches(want, got) {
		return "", false, err
	}
	return id, true, nil
}

// Capture the complete native fingerprint from the same detail response whose
// typed attributes satisfy the retained source effect. A second GET could bind
// later unrelated edits to an earlier successful readback.
func verifyWirelessFeatureObservedResult[T any](ctx context.Context, c *client, kind string, ref wirelessFeatureTaskReference, want T, read func(context.Context, wirelessFeatureInstance) (T, string, error), matches func(T, T) bool, now time.Time) (string, string, bool, error) {
	if read == nil {
		return "", "", false, errConfigurationTransition
	}
	fingerprint := ""
	observed := func(ctx context.Context, instance wirelessFeatureInstance) (T, error) {
		value, digest, err := read(ctx, instance)
		fingerprint = digest
		return value, err
	}
	id, done, err := verifyWirelessFeatureAppliedResult(ctx, c, kind, ref, want, observed, matches, now)
	if err != nil || !done {
		return "", "", false, err
	}
	if !validWirelessFeatureFingerprint(fingerprint) {
		return "", "", false, errInvalidResponse
	}
	return id, fingerprint, true, nil
}

func (c *client) wirelessFeatureMutationTask(ctx context.Context, method, path string, body []byte) (Task, error) {
	raw, err := c.mutate(ctx, method, path, body)
	if err != nil {
		return Task{}, err
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		return Task{}, errInvalidResponse
	}
	return Task{ID: receipt.Response.ID}, nil
}
