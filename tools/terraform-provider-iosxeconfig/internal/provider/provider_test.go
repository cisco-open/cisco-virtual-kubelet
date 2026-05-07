// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProviderEnvKubeconfigWithContext(t *testing.T) {
	dir := t.TempDir()
	kubeconfig := filepath.Join(dir, "config")
	body := []byte(`
apiVersion: v1
kind: Config
clusters:
- name: default-cluster
  cluster:
    server: https://default-cluster.example.invalid
- name: non-default-cluster
  cluster:
    server: https://non-default.example.invalid
contexts:
- name: default
  context:
    cluster: default-cluster
    user: test-user
- name: non-default
  context:
    cluster: non-default-cluster
    user: test-user
current-context: default
users:
- name: test-user
  user:
    token: test-token
`)
	if err := os.WriteFile(kubeconfig, body, 0o600); err != nil {
		t.Fatalf("write kubeconfig fixture: %v", err)
	}
	t.Setenv("KUBECONFIG", kubeconfig)

	cfg, err := loadRESTConfig("", "non-default")
	if err != nil {
		t.Fatalf("loadRESTConfig: %v", err)
	}
	if got, want := cfg.Host, "https://non-default.example.invalid"; got != want {
		t.Fatalf("REST host=%q, want %q", got, want)
	}
}

func TestProviderEnvKubeconfigWithoutContext(t *testing.T) {
	dir := t.TempDir()
	kubeconfig := filepath.Join(dir, "config")
	body := []byte(`
apiVersion: v1
kind: Config
clusters:
- name: default-cluster
  cluster:
    server: https://default-cluster.example.invalid
- name: non-default-cluster
  cluster:
    server: https://non-default.example.invalid
contexts:
- name: default
  context:
    cluster: default-cluster
    user: test-user
- name: non-default
  context:
    cluster: non-default-cluster
    user: test-user
current-context: default
users:
- name: test-user
  user:
    token: test-token
`)
	if err := os.WriteFile(kubeconfig, body, 0o600); err != nil {
		t.Fatalf("write kubeconfig fixture: %v", err)
	}
	t.Setenv("KUBECONFIG", kubeconfig)

	cfg, err := loadRESTConfig("", "")
	if err != nil {
		t.Fatalf("loadRESTConfig: %v", err)
	}
	if got, want := cfg.Host, "https://default-cluster.example.invalid"; got != want {
		t.Fatalf("REST host=%q, want %q", got, want)
	}
}
