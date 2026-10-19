// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func networkSettingIdentity(path, kind string) string { return path + "#" + kind }

// Build only explicit attributes in the resolved profile. AAA credentials and
// IPAM have separate contracts and cannot silently fall through to PUT.
func networkSettingPayload(setting networkSetting) ([]byte, error) {
	var payload any
	switch setting.kind {
	case "ntp", "dhcp":
		// Empty model lists explicitly reset the group. The pinned provider
		// emits null; the appliance rejects [] for NTP and returns {} after
		// either setting's reset. Manifest nulls never reach this executor.
		var servers []string
		if len(setting.servers) > 0 {
			servers = append([]string{}, setting.servers...)
		}
		payload = map[string]any{setting.kind: map[string]any{"servers": servers}}
	case "dns":
		dns := map[string]any{"domainName": setting.domain}
		if len(setting.servers) != 0 {
			dns["dnsServers"] = append([]string{}, setting.servers...)
		}
		payload = map[string]any{"dns": dns}
	case "timezone":
		payload = map[string]any{"timeZone": map[string]any{"identifier": setting.timezone}}
	case "banner":
		payload = map[string]any{"banner": map[string]any{"type": setting.bannerType, "message": setting.banner}}
	case "telemetry":
		groups := map[string]any{}
		for _, group := range telemetryGroups {
			values, ok := setting.telemetry[group]
			if !ok || len(values) == 0 {
				continue
			}
			copy := map[string]any{}
			for k, v := range values {
				copy[k] = v
			}
			if group == "applicationVisibility" {
				collector := map[string]any{}
				for _, key := range []string{"collectorType", "address", "port"} {
					if value, ok := copy[key]; ok {
						collector[key] = value
						delete(copy, key)
					}
				}
				if len(collector) > 0 {
					copy["collector"] = collector
				}
			}
			groups[group] = copy
		}
		payload = groups
	default:
		return nil, errors.New("network setting execution is not implemented")
	}
	return json.Marshal(payload)
}

type networkConfigurationExecutor struct {
	comparison       *templateInputComparison
	client           *client
	bindings         []networkBinding
	applicationPlan  func(context.Context, networkSubmissionObservation) (string, error)
	applicationProof func(context.Context, string) error
}

func (e *networkConfigurationExecutor) count() int {
	count := 0
	for _, b := range e.bindings {
		count += len(b.settings)
	}
	return count
}

func (e *networkConfigurationExecutor) sites(ctx context.Context) (map[string]Site, error) {
	sites, err := e.client.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	actual := map[string]Site{}
	for _, site := range sites {
		actual[site.NameHierarchy] = site
	}
	for _, binding := range e.bindings {
		if len(binding.settings) == 0 {
			continue
		}
		site, ok := actual[binding.path]
		if !ok || site.Type != binding.kind || !validAPIID(site.ID) {
			return nil, errors.New("network setting site identity is missing or incompatible")
		}
	}
	return actual, nil
}

func (e *networkConfigurationExecutor) next(ctx context.Context) (*configurationWrite, error) {
	frozen := freezeNetworkExecutor(e)
	// Validate the complete selected payload set before collecting native state.
	for _, binding := range frozen.bindings {
		for _, setting := range binding.settings {
			if _, err := networkSettingPayload(setting); err != nil {
				return nil, err
			}
		}
	}
	observed, err := frozen.observeSubmission(ctx)
	if err != nil {
		return nil, err
	}
	predecessor := ""
	if frozen.applicationPlan != nil {
		predecessor, err = frozen.applicationPlan(ctx, observed)
		if err != nil {
			return nil, err
		}
	}
	for _, binding := range frozen.bindings {
		for _, setting := range binding.settings {
			identity := networkSettingIdentity(binding.path, setting.kind)
			if len(networkSettingDrift(binding.path, setting, observed.effective[identity])) == 0 {
				continue
			}
			// A domain-only API PUT clears resolvers on the appliance. Preserve
			// unspecified resolvers from the same frozen, twice-revalidated
			// observation; explicit [] deliberately clears via omission.
			submission := setting
			if setting.kind == "dns" && setting.servers == nil {
				submission.servers = observed.effective[identity].servers
			}
			if setting.kind == "telemetry" {
				submission, err = resolveTelemetrySubmission(setting, observed.effective[identity])
				if err != nil {
					return nil, err
				}
			}
			body, err := networkSettingPayload(submission)
			if err != nil {
				return nil, err
			}
			site := observed.sites[binding.path]
			apiKind := setting.kind
			if apiKind == "timezone" {
				apiKind = "timeZone"
			}
			endpoint := sitesPath + "/" + site.id + "/" + apiKind + "Settings"
			resource := identity
			if frozen.applicationPlan != nil {
				resource = networkSettingUpdatePrefix + networkSettingLogicalKey(identity) + ":" + predecessor
			}
			if setting.kind == "telemetry" && frozen.comparison != nil {
				resource, err = networkPrivateResource(ctx, frozen.comparison, binding.path, predecessor, observed, submission)
				if err != nil {
					return nil, err
				}
			}
			write := &configurationWrite{resource: resource, action: "Update", identity: configurationResourceIdentity{target: site.id}, submit: func(ctx context.Context) (Task, error) {
				raw, err := frozen.client.mutate(ctx, http.MethodPut, endpoint, body)
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
			}}
			var preflightContext context.Context
			guardConfigurationPreflight(write, func(ctx context.Context) error {
				preflightContext = ctx
				for range 2 {
					fresh, err := frozen.observeSubmission(ctx)
					if err != nil {
						return err
					}
					if frozen.applicationPlan != nil {
						history, err := frozen.applicationPlan(ctx, fresh)
						if err != nil {
							return err
						}
						if history != predecessor {
							return errNetworkApplicationHistory
						}
					}
					if !reflect.DeepEqual(observed, fresh) {
						return errConfigurationStale
					}
				}
				if frozen.comparison != nil {
					return frozen.comparison.check(ctx)
				}
				return nil
			})
			if frozen.applicationPlan != nil {
				prior := write.noDispatch
				write.noDispatch = func(err error) *configurationPreSubmitRefusal {
					// Retain the predecessor fence if bookkeeping itself was
					// lost or altered; no-dispatch cannot restore old custody.
					if errors.Is(err, errNetworkApplicationHistory) {
						return nil
					}
					proof := prior(err)
					if proof == nil {
						return nil
					}
					if preflightContext == nil || frozen.applicationProof == nil {
						return nil
					}
					proofCtx, cancel := context.WithTimeout(context.WithoutCancel(preflightContext), 5*time.Second)
					defer cancel()
					// A failed native read cannot hide simultaneous custody
					// loss and erase the only retained predecessor fence.
					if err := frozen.applicationProof(proofCtx, predecessor); err != nil {
						return nil
					}
					return proof
				}
			}
			return write, nil
		}
	}
	return nil, nil
}

func (e *networkConfigurationExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op.Action != "Update" || !validAPIID(op.TargetID) || op.ParentID != "" {
		return false, errConfigurationTransition
	}
	if _, private := networkPrivateParts(op.Resource); private {
		if !networkSettingApplicationEnvelope(op) {
			return false, errConfigurationTransition
		}
		for _, binding := range e.bindings {
			for _, setting := range binding.settings {
				if setting.kind == "telemetry" && networkSettingResourceMatches(op.Resource, networkSettingIdentity(binding.path, setting.kind)) {
					selected := &networkConfigurationExecutor{client: e.client, comparison: e.comparison, bindings: []networkBinding{{path: binding.path, kind: binding.kind, settings: []networkSetting{setting}}}}
					observed, err := selected.observeSubmission(ctx)
					if err != nil {
						return false, err
					}
					if observed.sites[binding.path].id != op.TargetID {
						return false, errConfigurationStale
					}
					return networkMatchPrivate(ctx, e.comparison, op.Resource, binding.path, observed)
				}
			}
		}
		return false, errNetworkNativeBaseline
	}
	sites, err := e.sites(ctx)
	if err != nil {
		return false, err
	}
	for _, binding := range e.bindings {
		for _, setting := range binding.settings {
			if !networkSettingResourceMatches(op.Resource, networkSettingIdentity(binding.path, setting.kind)) {
				continue
			}
			if sites[binding.path].ID != op.TargetID {
				return false, errors.New("network setting site was replaced")
			}
			got, err := e.client.effectiveNetworkSetting(ctx, sites, binding.path, setting.kind)
			if err != nil {
				return false, err
			}
			if setting.kind == "telemetry" && e.comparison != nil {
				return false, errNetworkNativeBaseline
			}
			return len(networkSettingDrift(binding.path, setting, got)) == 0, nil
		}
	}
	return false, errConfigurationTransition
}
