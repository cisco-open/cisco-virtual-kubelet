// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controlleradapter

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	// DefaultWorkerClusterRole is the install-time-audited base role shipped by
	// the Helm chart. Adapters that need only the shared APIs should select it
	// in their Descriptor.
	DefaultWorkerClusterRole = "cisco-virtual-kubelet-controller-worker"

	// DefaultCredentialPath is the read-only mount point offered to controller
	// adapters for controller authentication material. The foundation passes a
	// path, never Secret bytes; each adapter defines and validates its own key
	// layout beneath the mount.
	DefaultCredentialPath = "/var/run/secrets/cisco-vk/controller/credentials"

	// DefaultCADirectory is the directory mount for an optional controller CA.
	// Directory projection (rather than a subPath file mount) lets kubelet
	// publish ConfigMap CA rotations without restarting the worker.
	DefaultCADirectory = "/var/run/secrets/cisco-vk/controller-ca"

	// DefaultCAPath is the conventional CA bundle file within that directory.
	// An empty Options.CAPath means that the system trust store applies.
	DefaultCAPath = DefaultCADirectory + "/ca.crt"

	// DefaultIntentSecretPath is the root of explicitly projected Secret keys
	// referenced by NetworkControllerConfig.spec.secretRefs. Workers never use
	// the Kubernetes Secret API to resolve these values.
	DefaultIntentSecretPath = "/var/run/secrets/cisco-vk/controller-intent"
)

// Options is the product-neutral input to a controller adapter Factory.
//
// NewAdapter always supplies Factory with a deep copy of Controller. This
// gives the adapter an immutable startup snapshot without sharing informer or
// reconciler cache state. CredentialPath, CAPath, and IntentSecretPath identify
// kubelet-mounted, read-only files; credentials are deliberately not
// represented as strings or byte slices in this contract. MaterialRotation is
// the manager-owned signal and age limit adapters use to invalidate cached
// authentication and TLS sessions after projected-volume rotation.
type Options struct {
	Controller       *ciskov1.NetworkController
	CredentialPath   string
	CAPath           string
	IntentSecretPath string
	MaterialRotation MaterialRotationPolicy
}

// cloned returns an ownership-independent copy suitable for handing to an
// adapter. NetworkController is a Kubernetes API object, so a JSON round trip
// preserves the API representation while this foundational package remains
// independent of generated deepcopy implementation details.
func (o Options) cloned() (Options, error) {
	out := o
	if o.Controller == nil {
		return out, nil
	}
	raw, err := json.Marshal(o.Controller)
	if err != nil {
		return Options{}, fmt.Errorf("copy NetworkController: marshal: %w", err)
	}
	out.Controller = new(ciskov1.NetworkController)
	if err := json.Unmarshal(raw, out.Controller); err != nil {
		return Options{}, fmt.Errorf("copy NetworkController: unmarshal: %w", err)
	}
	return out, nil
}

// Adapter installs one controller product's reconcilers and manager runnables.
// SetupWithManager must not start unmanaged goroutines: controller-runtime owns
// lifecycle, cancellation, leader election, metrics, and health for the worker.
type Adapter interface {
	SetupWithManager(ctrl.Manager) error
}

// Factory constructs one adapter for one NetworkController worker. Factories
// should validate their controller-specific contract but defer network dials
// and long-running work to manager-owned reconcilers or runnables.
type Factory func(Options) (Adapter, error)

// DescriptorDigest returns a stable fingerprint for the adapter contract that
// must agree between the central manager image and an isolated worker image.
// Set-like slices are normalized so registration order has no effect.
func DescriptorDigest(descriptor Descriptor) string {
	normalized := descriptor
	normalized.Capabilities = append([]string(nil), descriptor.Capabilities...)
	normalized.NetAsCode.ModelVersions = append([]string(nil), descriptor.NetAsCode.ModelVersions...)
	normalized.NetAsCode.Sections = append([]string(nil), descriptor.NetAsCode.Sections...)
	sort.Strings(normalized.Capabilities)
	sort.Strings(normalized.NetAsCode.ModelVersions)
	sort.Strings(normalized.NetAsCode.Sections)
	payload, err := json.Marshal(normalized)
	if err != nil {
		// Descriptor is composed only of JSON-native exported fields. Keep a
		// deterministic fallback so an unexpected encoder change fails closed.
		payload = []byte(fmt.Sprintf("%#v", normalized))
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("%x", digest[:])
}

// IntentSecretPathInput contains only identities needed to bind a projected
// file to one exact config leaf and one administrator-authorized Secret key.
// SecretName and SecretKey are references, never Secret values.
type IntentSecretPathInput struct {
	ConfigName  string
	Section     string
	JSONPointer string
	SourceAlias string
	SecretName  string
	SecretKey   string
}

// IntentSecretRelativePath returns the deterministic, non-sensitive file path
// used by generic orchestration for one NetworkControllerConfig SecretRef. Its
// digest binds both the destination and authorization mapping, so an old Pod
// cannot mistake a stale projected value for an updated source alias/key.
func IntentSecretRelativePath(input IntentSecretPathInput) (string, error) {
	if problems := utilvalidation.IsDNS1123Subdomain(input.ConfigName); len(problems) > 0 {
		return "", fmt.Errorf("invalid NetworkControllerConfig name %q", input.ConfigName)
	}
	if !validNetAsCodeIdentifier(input.Section) {
		return "", fmt.Errorf("invalid Network as Code section %q", input.Section)
	}
	if input.JSONPointer == "" || input.JSONPointer[0] != '/' {
		return "", fmt.Errorf("invalid empty or relative JSON pointer")
	}
	if problems := utilvalidation.IsDNS1123Label(input.SourceAlias); len(problems) > 0 {
		return "", fmt.Errorf("invalid intent Secret source alias %q", input.SourceAlias)
	}
	if problems := utilvalidation.IsDNS1123Subdomain(input.SecretName); len(problems) > 0 {
		return "", fmt.Errorf("invalid authorized Secret name")
	}
	if problems := utilvalidation.IsConfigMapKey(input.SecretKey); len(problems) > 0 {
		return "", fmt.Errorf("invalid authorized Secret key")
	}
	digest := sha256.Sum256([]byte(input.JSONPointer + "\x00" + input.SourceAlias + "\x00" + input.SecretName + "\x00" + input.SecretKey))
	return filepath.Join(input.ConfigName, input.Section, fmt.Sprintf("%x", digest[:])), nil
}
