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

package softwareupgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

// ResolvedImage carries the materialised image bytes plus metadata.
//
// Reader streams the image contents; Size is the byte count when known
// in advance (zero means "unknown — let the device decide"); Cleanup
// is invoked exactly once after the upload completes or fails and is
// responsible for releasing any temp-file resources; Local=true means
// the image is already on the device flash and Reader is nil.
type ResolvedImage struct {
	Reader  io.Reader
	Size    int64
	Cleanup func() error
	Local   bool
}

// ImageResolver materialises an UpgradeImageSource. Injected so tests
// can substitute deterministic readers.
type ImageResolver interface {
	Resolve(ctx context.Context, namespace string, src opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error)
}

// DefaultImageResolver dispatches on the populated field. URL → HTTP
// GET into a temp file with SHA256 verification; ConfigMapRef → read
// binaryData["image"]; LocalPath → no resolution, the reconciler
// jumps to Activating.
type DefaultImageResolver struct {
	HTTPClient *http.Client
	K8sClient  client.Client
}

// NewDefaultImageResolver constructs a resolver with sensible
// defaults. K8s is mandatory (for ConfigMap reads); httpClient may be
// nil to use http.DefaultClient.
func NewDefaultImageResolver(k8s client.Client, httpClient *http.Client) *DefaultImageResolver {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &DefaultImageResolver{HTTPClient: httpClient, K8sClient: k8s}
}

func (r *DefaultImageResolver) Resolve(ctx context.Context, namespace string, src opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error) {
	switch {
	case src.LocalPath != "":
		return &ResolvedImage{Local: true}, nil
	case src.URL != "":
		return r.resolveURL(ctx, src)
	case src.ConfigMapRef != nil:
		return r.resolveConfigMap(ctx, namespace, src.ConfigMapRef.Name)
	default:
		return nil, errors.New("image source: one of url, configMapRef, or localPath is required")
	}
}

func (r *DefaultImageResolver) resolveURL(ctx context.Context, src opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error) {
	if src.SHA256 == "" {
		return nil, errors.New("image source URL requires SHA256 verification")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("image source URL: %w", err)
	}
	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("image source URL get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("image source URL %s returned status %d", src.URL, resp.StatusCode)
	}
	tmp, err := os.CreateTemp("", "cvk-upgrade-*.bin")
	if err != nil {
		return nil, fmt.Errorf("image source URL: temp file: %w", err)
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), resp.Body)
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, fmt.Errorf("image source URL: stream into temp file: %w", err)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != src.SHA256 {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, fmt.Errorf("image source URL: SHA256 mismatch: got %s want %s", got, src.SHA256)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, fmt.Errorf("image source URL: temp rewind: %w", err)
	}
	cleanup := func() error {
		_ = tmp.Close()
		return os.Remove(tmp.Name())
	}
	return &ResolvedImage{Reader: tmp, Size: n, Cleanup: cleanup}, nil
}

func (r *DefaultImageResolver) resolveConfigMap(ctx context.Context, namespace, name string) (*ResolvedImage, error) {
	if r.K8sClient == nil {
		return nil, errors.New("image source configMapRef: K8sClient not configured on resolver")
	}
	var cm corev1.ConfigMap
	if err := r.K8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &cm); err != nil {
		return nil, fmt.Errorf("image source configMapRef get: %w", err)
	}
	data, ok := cm.BinaryData["image"]
	if !ok {
		return nil, fmt.Errorf("image source configMapRef: ConfigMap %s/%s has no binaryData[\"image\"]", namespace, name)
	}
	// ConfigMaps cap at ~1 MiB total; the image must fit. No SHA check
	// here — the operator already controls the ConfigMap and we treat
	// it as the source of truth.
	rd := &readerCloser{r: byteReader(data)}
	return &ResolvedImage{Reader: rd, Size: int64(len(data)), Cleanup: func() error { return nil }}, nil
}

type readerCloser struct{ r io.Reader }

func (r *readerCloser) Read(p []byte) (int, error) { return r.r.Read(p) }
func (r *readerCloser) Close() error               { return nil }

func byteReader(b []byte) io.Reader {
	return &bytesReader{b: b}
}

type bytesReader struct {
	b   []byte
	pos int
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.pos:])
	r.pos += n
	return n, nil
}
