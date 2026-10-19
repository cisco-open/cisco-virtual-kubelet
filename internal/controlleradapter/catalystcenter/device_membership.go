// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Device memberships are a resource executor, not a provisioning workflow.
// The inventory planner must establish site/state dependencies before exposing
// this executor to a manifest. Direct qualification uses an isolated lab tag.
type desiredDeviceMembership struct {
	device configurationDeviceReference
	tags   []string
}

type deviceMembershipExecutor struct {
	client   *client
	devices  []desiredDeviceMembership
	declared []desiredTag
}

func (e *deviceMembershipExecutor) count() int {
	n := 0
	for _, d := range e.devices {
		n += len(d.tags)
	}
	return n
}

func (e *deviceMembershipExecutor) observe(ctx context.Context) ([]tagMembership, error) {
	tags, err := e.client.listTags(ctx)
	if err != nil {
		return nil, err
	}
	devices, err := e.client.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	declared := map[string]bool{}
	for _, tag := range e.declared {
		declared[tag.name] = true
	}
	members := map[string]map[string]bool{}
	seen := map[string]bool{}
	result := []tagMembership{}
	for _, desired := range e.devices {
		if desired.device.Name == "" {
			return nil, errors.New("device membership requires a model name")
		}
		device, err := resolveConfigurationDevice(desired.device, devices)
		if err != nil {
			return nil, err
		}
		for _, name := range desired.tags {
			if !validTemplateTagName(name) || seen[device.ID+"/"+name] {
				return nil, errors.New("invalid or duplicate device tag association")
			}
			seen[device.ID+"/"+name] = true
			tag, exists := tags[name]
			if exists {
				if _, read := members[tag.ID]; !read {
					members[tag.ID], err = e.client.tagExplicitDeviceMembers(ctx, tag.ID)
					if err != nil {
						return nil, err
					}
				}
			}
			result = append(result, tagMembership{
				resource: tagMembershipIdentity(desired.device.Name, name),
				tagID:    tag.ID, memberID: device.ID, declared: declared[name],
				present:   exists && members[tag.ID][device.ID],
				protected: exists && (tag.SystemTag == nil || *tag.SystemTag),
			})
		}
	}
	return result, nil
}

func (e *deviceMembershipExecutor) next(ctx context.Context) (*configurationWrite, error) {
	frozen := &deviceMembershipExecutor{client: e.client, devices: make([]desiredDeviceMembership, len(e.devices)), declared: make([]desiredTag, len(e.declared))}
	for i, desired := range e.devices {
		frozen.devices[i] = desiredDeviceMembership{device: desired.device, tags: append([]string(nil), desired.tags...)}
	}
	for i, tag := range e.declared {
		frozen.declared[i].name = tag.name
	}
	observed, err := frozen.observe(ctx)
	if err != nil {
		return nil, err
	}
	return nextTagMembership(frozen.client, "networkdevice", observed, frozen.observe)
}

func (e *deviceMembershipExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	observed, err := e.observe(ctx)
	if err != nil {
		return false, err
	}
	return verifyTagMembership(observed, op)
}
