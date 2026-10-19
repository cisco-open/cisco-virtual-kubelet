// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"slices"
	"strings"
)

type configurationCredentialIntent struct {
	path, field, key string
	version          int64
}

func resolveVersionedConfigurationCredentials(snapshot *configurationSecretSnapshot, owner, section string, leaves []configurationCredentialIntent) (map[string]string, []ciskov1.NetworkControllerConfigurationCredentialVersion, error) {
	if snapshot == nil || owner == "" || snapshot.uid != owner {
		return nil, nil, errConfigurationSecret
	}
	payload := map[string]string{}
	versions := []ciskov1.NetworkControllerConfigurationCredentialVersion{}
	for _, leaf := range leaves {
		found := false
		for _, value := range snapshot.values {
			if value.ref.Section != section || value.ref.Path != leaf.path {
				continue
			}
			if found {
				return nil, nil, errConfigurationSecret
			}
			secret, err := decodeConfigurationVersionedSecret(value.value, leaf.version)
			if err != nil {
				return nil, nil, err
			}
			found = true
			payload[leaf.field] = secret
			versions = append(versions, ciskov1.NetworkControllerConfigurationCredentialVersion{Key: leaf.key, Path: leaf.path, Version: leaf.version, SourceAlias: value.ref.Source, SecretName: value.source.Name, SecretKey: value.source.Key})
		}
		if !found {
			return nil, nil, errConfigurationSecret
		}
	}
	slices.SortFunc(versions, func(a, b ciskov1.NetworkControllerConfigurationCredentialVersion) int {
		return strings.Compare(a.Path, b.Path)
	})
	return payload, versions, nil
}

func configurationCredentialRotation(prior, desired []ciskov1.NetworkControllerConfigurationCredentialVersion, identify func(string) string) (bool, error) {
	old := map[string]ciskov1.NetworkControllerConfigurationCredentialVersion{}
	for _, v := range prior {
		kind := identify(v.Path)
		if kind == "" || v.Version < 1 || v.SourceAlias == "" || v.SecretName == "" || v.SecretKey == "" {
			return false, errConfigurationSecret
		}
		if _, exists := old[kind]; exists {
			return false, errConfigurationSecret
		}
		old[kind] = v
	}
	rotate := false
	seen := map[string]bool{}
	for _, v := range desired {
		kind := identify(v.Path)
		previous, exists := old[kind]
		if kind == "" || seen[kind] || v.Version < 1 || v.SourceAlias == "" || v.SecretName == "" || v.SecretKey == "" {
			return false, errConfigurationSecret
		}
		seen[kind] = true
		if !exists {
			rotate = true
			continue
		}
		delete(old, kind)
		if v.Version < previous.Version {
			return false, errConfigurationSecret
		}
		sameSource := v.SourceAlias == previous.SourceAlias && v.SecretName == previous.SecretName && v.SecretKey == previous.SecretKey
		if !sameSource && v.Version == previous.Version {
			return false, errConfigurationSecret
		}
		rotate = rotate || !sameSource || v.Version != previous.Version
	}
	if len(old) != 0 {
		return false, errConfigurationSecret
	}
	return rotate, nil
}
