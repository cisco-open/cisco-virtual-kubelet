// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

var errConfigurationSecret = errors.New("controller configuration credential projection is unavailable, changed, or invalid")

// Destinations are supplied by a product parser, never inferred from the user
// refs. Authorizing a Secret source does not authorize overwriting arbitrary
// model fields. This resolver is not yet an execution capability: manifest
// writes remain gated until durable write-only credential receipts are wired.
type configurationSecretDestination struct{ section, pointer string }

type configurationSecretValue struct {
	ref      configapi.NetworkControllerSecretRef
	source   ciskov1.NetworkControllerIntentSecretSource
	relative string
	value    []byte
}

// An ephemeral snapshot, not an applied-credential receipt. Values and their
// unkeyed digests must never be stored in Kubernetes, logs, events, or evidence
// files. Explicit private-comparison use may emit domain-separated MACs only.
// Reconstructing a snapshot after a restart does not prove which credential an
// earlier request submitted. The journal must retain that uncertainty.
type configurationSecretSnapshot struct {
	root                 string
	controller, config   string
	controllerUID, uid   string
	namespace            string
	controllerGeneration int64
	generation           int64
	values               []configurationSecretValue
	use                  string
}

func (*configurationSecretSnapshot) String() string   { return "<redacted configuration credentials>" }
func (*configurationSecretSnapshot) GoString() string { return "<redacted configuration credentials>" }

const maxConfigurationSecretBytes = 1 << 20

// resolveConfigurationSecrets reads only the controller-authorized, per-key
// projections already mounted by the common worker. It never reads Secret API
// objects or creates another credential store. Call after ValidateConfigContract.
func resolveConfigurationSecrets(nc *ciskov1.NetworkController, config *configapi.NetworkControllerConfig, root string, destinations []configurationSecretDestination) (*configurationSecretSnapshot, error) {
	return resolveConfigurationSecretsForUse(nc, config, root, destinations, "Value")
}

func resolveConfigurationSecretsForUse(nc *ciskov1.NetworkController, config *configapi.NetworkControllerConfig, root string, destinations []configurationSecretDestination, use string) (*configurationSecretSnapshot, error) {
	if use == "" {
		use = "Value"
	}
	if use != "Value" && use != "PrivateComparisonKey" {
		return nil, errConfigurationSecret
	}
	if nc == nil || config == nil || nc.Spec.Paused || nc.Namespace != config.Namespace || config.Spec.ControllerRef.Name != nc.Name ||
		nc.UID == "" || config.UID == "" || len(config.Spec.SecretRefs) == 0 || len(config.Spec.SecretRefs) > 128 ||
		!filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, errConfigurationSecret
	}
	authorized := map[string]ciskov1.NetworkControllerIntentSecretSource{}
	for _, source := range nc.Spec.IntentSecretSources {
		if _, duplicate := authorized[source.Alias]; duplicate {
			return nil, errConfigurationSecret
		}
		authorized[source.Alias] = source
	}
	allowed := map[configurationSecretDestination]bool{}
	for _, destination := range destinations {
		if _, err := configurationSecretPointer(destination.pointer); err != nil || !slices.Contains(config.Spec.ManagedSections, destination.section) || allowed[destination] {
			return nil, errConfigurationSecret
		}
		allowed[destination] = true
	}
	snapshot := &configurationSecretSnapshot{
		root: root, controller: nc.Name, controllerUID: string(nc.UID), controllerGeneration: nc.Generation,
		config: config.Name, uid: string(config.UID), namespace: config.Namespace, generation: config.Generation,
		use: use,
	}
	seen := map[configurationSecretDestination]bool{}
	for _, ref := range config.Spec.SecretRefs {
		actualUse := ref.Use
		if actualUse == "" {
			actualUse = "Value"
		}
		if actualUse != use {
			return nil, errConfigurationSecret
		}
		destination := configurationSecretDestination{ref.Section, ref.Path}
		source, ok := authorized[ref.Source]
		if !ok || !allowed[destination] || seen[destination] {
			return nil, errConfigurationSecret
		}
		seen[destination] = true
		relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{
			ConfigName: config.Name, Section: ref.Section, JSONPointer: ref.Path,
			SourceAlias: ref.Source, SecretName: source.Name, SecretKey: source.Key,
		})
		if err != nil {
			return nil, errConfigurationSecret
		}
		snapshot.values = append(snapshot.values, configurationSecretValue{ref: ref, source: source, relative: relative})
	}
	// OpenRoot follows Kubernetes atomic-writer symlinks inside the mount, while
	// refusing traversal outside it (including symlink replacements during open).
	projection, err := os.OpenRoot(root)
	if err != nil {
		return nil, errConfigurationSecret
	}
	defer projection.Close()
	total := 0
	for i := range snapshot.values {
		value, err := readConfigurationSecret(projection, snapshot.values[i].relative)
		if err != nil {
			return nil, err
		}
		total += len(value)
		if total > maxConfigurationSecretBytes {
			return nil, errConfigurationSecret
		}
		snapshot.values[i].value = value
	}
	return snapshot, nil
}

func readConfigurationSecret(root *os.Root, relative string) ([]byte, error) {
	file, err := root.Open(relative)
	if err != nil {
		return nil, errConfigurationSecret
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxConfigurationSecretBytes {
		return nil, errConfigurationSecret
	}
	value, err := io.ReadAll(io.LimitReader(file, maxConfigurationSecretBytes+1))
	if err != nil || len(value) == 0 || len(value) > maxConfigurationSecretBytes || !utf8.Valid(value) || slices.Contains(value, byte(0)) {
		return nil, errConfigurationSecret
	}
	return value, nil
}

// unchanged detects remapping, revocation and material changes before a permit
// and immediately before submission. It does not replace durable versioning or
// offer atomicity with Kubernetes Secret updates. Submission uses captured bytes.
func (s *configurationSecretSnapshot) unchanged(nc *ciskov1.NetworkController, config *configapi.NetworkControllerConfig) error {
	if s == nil || nc == nil || config == nil || nc.Name != s.controller || string(nc.UID) != s.controllerUID || nc.Generation != s.controllerGeneration ||
		config.Name != s.config || string(config.UID) != s.uid || config.Generation != s.generation || config.Namespace != s.namespace ||
		len(config.Spec.SecretRefs) != len(s.values) {
		return errConfigurationSecret
	}
	destinations := make([]configurationSecretDestination, 0, len(s.values))
	for _, value := range s.values {
		if !slices.Contains(config.Spec.SecretRefs, value.ref) || !slices.Contains(nc.Spec.IntentSecretSources, value.source) {
			return errConfigurationSecret
		}
		destinations = append(destinations, configurationSecretDestination{value.ref.Section, value.ref.Path})
	}
	fresh, err := resolveConfigurationSecretsForUse(nc, config, s.root, destinations, s.use)
	if err != nil {
		return err
	}
	for _, value := range s.values {
		for _, current := range fresh.values {
			if current.ref == value.ref && subtle.ConstantTimeCompare(current.value, value.value) != 1 {
				return errConfigurationSecret
			}
		}
	}
	return nil
}

func configurationSecretPointer(pointer string) ([]string, error) {
	if len(pointer) == 0 || len(pointer) > 1024 || pointer[0] != '/' {
		return nil, errConfigurationSecret
	}
	parts := strings.Split(pointer[1:], "/")
	for i, part := range parts {
		var decoded strings.Builder
		for n := 0; n < len(part); n++ {
			if part[n] != '~' {
				decoded.WriteByte(part[n])
				continue
			}
			n++
			if n >= len(part) || (part[n] != '0' && part[n] != '1') {
				return nil, errConfigurationSecret
			}
			if part[n] == '0' {
				decoded.WriteByte('~')
			} else {
				decoded.WriteByte('/')
			}
		}
		parts[i] = decoded.String()
	}
	return parts, nil
}

// inject works on a private copy. Every parent and array element must already
// exist; only absent/null object leaves may be populated. A ref cannot silently
// overwrite inline intent, append an array element, or replace an object.
func (s *configurationSecretSnapshot) inject(raw []byte) ([]byte, error) {
	if s == nil || (s.use != "" && s.use != "Value") || len(s.values) == 0 {
		return nil, errConfigurationSecret
	}
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, errConfigurationSecret
	}
	var stripe map[string]any
	if node.Decode(&stripe) != nil {
		return nil, errConfigurationSecret
	}
	for _, value := range s.values {
		parts, err := configurationSecretPointer(value.ref.Path)
		if err != nil {
			return nil, err
		}
		var current any = stripe[value.ref.Section]
		for _, part := range parts[:len(parts)-1] {
			switch parent := current.(type) {
			case map[string]any:
				current = parent[part]
			case []any:
				index, err := strconv.Atoi(part)
				if err != nil || index < 0 || index >= len(parent) || strconv.Itoa(index) != part {
					return nil, errConfigurationSecret
				}
				current = parent[index]
			default:
				return nil, errConfigurationSecret
			}
		}
		parent, ok := current.(map[string]any)
		if !ok || parent[parts[len(parts)-1]] != nil {
			return nil, errConfigurationSecret
		}
		parent[parts[len(parts)-1]] = string(value.value)
	}
	out, err := json.Marshal(map[string]any{nacmodel.Stripe: stripe})
	if err != nil || len(out) > 1<<20 {
		return nil, errConfigurationSecret
	}
	return out, nil
}
