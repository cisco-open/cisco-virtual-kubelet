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
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/pin/tftp/v3"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

// ResolvedImage carries the materialised image bytes plus metadata.
//
// Reader streams the image contents; Size is the verified positive byte count.
// Cleanup is invoked exactly once after the upload completes or fails and is
// responsible for releasing any temp-file resources. Digest is the
// algorithm-qualified SHA-256 content address of the materialised bytes and
// lets the reconciler pin content across retries before device-side install.
type ResolvedImage struct {
	Reader  io.Reader
	Size    int64
	Digest  string
	Cleanup func() error
}

// ImageResolver materialises an UpgradeImageSource. Injected so tests can
// substitute deterministic readers. Implementations may wrap transient errors
// with MarkRetryableResolveError; callers must check context cancellation
// first, then use IsRetryableResolveError when deciding whether to retry.
type ImageResolver interface {
	Resolve(ctx context.Context, namespace string, src opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error)
}

// DefaultImageResolver dispatches on the populated byte source. URL →
// remote fetch into a temp file with SHA256 verification; ConfigMapRef →
// read binaryData["image"] and compute its content digest. Preinstalled and
// device-resident sources are lifecycle intents, not byte sources, and are
// handled by the reconciler's platform lifecycle backend.
type DefaultImageResolver struct {
	HTTPClient    *http.Client
	K8sClient     client.Reader
	TFTPBlockSize int
	CacheDir      string
	// MaxImageBytes bounds both downloaded and cached image data. Zero uses the
	// production default. The CISCO_VK_UPGRADE_MAX_IMAGE_BYTES environment
	// variable, when set to a positive base-10 byte count, overrides this field.
	MaxImageBytes int64
	// ResolveTimeout bounds one remote materialization. Zero uses the production
	// default; a negative value is rejected.
	ResolveTimeout time.Duration

	cacheMu sync.Mutex
}

const (
	defaultTFTPBlockSize  = 8192
	defaultTFTPRetries    = 10
	defaultTFTPTimeout    = 10 * time.Second
	defaultMaxImageBytes  = int64(8 << 30)
	defaultResolveTimeout = 4 * time.Hour
	cacheTempPrefix       = ".cvk-upgrade-cache-"
	maxSCPControlLineSize = 64 << 10

	envAllowInsecureSSH = "CISCO_VK_UPGRADE_ALLOW_INSECURE_SSH"
	envMaxImageBytes    = "CISCO_VK_UPGRADE_MAX_IMAGE_BYTES"

	// URLSecretPurposeLabel makes use of a Secret for software image retrieval
	// an explicit Secret-owner decision. Endpoint binding prevents an upgrade CR
	// author from forwarding credentials to an arbitrary server.
	URLSecretPurposeLabel     = "cisco.vk/purpose"
	URLSecretPurposeValue     = "software-image-source"
	URLSecretAllowedSchemeKey = "allowedScheme"
	URLSecretAllowedHostKey   = "allowedHost"
	URLSecretAllowedPortKey   = "allowedPort"
)

var (
	errImageTooLarge         = errors.New("image exceeds configured size limit")
	errUnsafeCacheFile       = errors.New("unsafe image cache entry")
	errSCPControlLineTooLong = errors.New("SCP control line exceeds configured limit")
)

// retryableResolveError marks a failure that is safe to retry without changing
// the source specification while preserving its original cause.
type retryableResolveError struct {
	err error
}

func (err *retryableResolveError) Error() string { return err.err.Error() }
func (err *retryableResolveError) Unwrap() error { return err.err }

// IsRetryableResolveError reports whether an image-resolution error crosses a
// boundary classified as transient by the resolver.
func IsRetryableResolveError(err error) bool {
	var retryable *retryableResolveError
	return errors.As(err, &retryable)
}

// MarkRetryableResolveError marks err as retryable. Nil remains nil, and an
// already-marked error is returned unchanged.
func MarkRetryableResolveError(err error) error {
	if err == nil || IsRetryableResolveError(err) {
		return err
	}
	return &retryableResolveError{err: err}
}

// NewDefaultImageResolver constructs a resolver with sensible
// defaults. K8s is mandatory (for ConfigMap reads); httpClient may be
// nil to use http.DefaultClient.
func NewDefaultImageResolver(k8s client.Reader, httpClient *http.Client) *DefaultImageResolver {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &DefaultImageResolver{
		HTTPClient:    httpClient,
		K8sClient:     k8s,
		TFTPBlockSize: defaultTFTPBlockSize,
		MaxImageBytes: defaultMaxImageBytes,
	}
}

func (r *DefaultImageResolver) Resolve(ctx context.Context, namespace string, src opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error) {
	maxImageBytes, err := r.imageSizeLimit()
	if err != nil {
		return nil, err
	}
	resolveTimeout := r.ResolveTimeout
	if resolveTimeout == 0 {
		resolveTimeout = defaultResolveTimeout
	}
	if resolveTimeout < 0 {
		return nil, errors.New("image resolver ResolveTimeout must not be negative")
	}
	resolveCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	switch {
	case src.URL != "":
		if !validSHA256Hex(src.SHA256) {
			return nil, errors.New("image source URL SHA256 must be 64 lowercase hexadecimal characters")
		}
		u, err := url.Parse(src.URL)
		if err != nil {
			return nil, &redactedURLParseError{endpoint: redactRawURL(src.URL), cause: err}
		}
		if _, managed := managedImageSourcePolicyFromContext(resolveCtx); managed {
			if err := validateManagedImageSource(u, src); err != nil {
				return nil, err
			}
		}
		if err := r.authorizeURLSecret(resolveCtx, namespace, u, src.URLSecretRef); err != nil {
			return nil, err
		}
		return r.resolveCachedURL(resolveCtx, namespace, src, maxImageBytes)
	case src.ConfigMapRef != nil:
		if _, managed := managedImageSourcePolicyFromContext(resolveCtx); managed {
			return nil, errors.New("managed image source must use an HTTPS or SFTP URL")
		}
		return r.resolveConfigMap(resolveCtx, namespace, src.ConfigMapRef.Name, maxImageBytes)
	default:
		return nil, errors.New("image source is not a resolvable byte source")
	}
}

func (r *DefaultImageResolver) imageSizeLimit() (int64, error) {
	limit := r.MaxImageBytes
	if limit == 0 {
		limit = defaultMaxImageBytes
	}
	if limit < 0 {
		return 0, errors.New("image resolver MaxImageBytes must be positive")
	}
	if raw := strings.TrimSpace(os.Getenv(envMaxImageBytes)); raw != "" {
		override, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || override <= 0 {
			return 0, fmt.Errorf("image resolver %s must be a positive base-10 byte count", envMaxImageBytes)
		}
		limit = override
	}
	return limit, nil
}

func (r *DefaultImageResolver) resolveCachedURL(ctx context.Context, namespace string, src opsv1alpha1.UpgradeImageSource, maxImageBytes int64) (*ResolvedImage, error) {
	cacheDir := r.CacheDir
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "cvk-upgrade-cache")
	}
	if err := ensurePrivateCacheDir(cacheDir); err != nil {
		return nil, fmt.Errorf("image source cache: prepare %s: %w", cacheDir, err)
	}
	// A per-device resolver only needs the requested content address. Serialize
	// cache maintenance and evict older managed digests before downloading so a
	// sequence of upgrade CRs cannot retain an unbounded number of large images.
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	if err := retainOnlyCacheDigest(cacheDir, src.SHA256); err != nil {
		return nil, fmt.Errorf("image source cache: enforce aggregate bound: %w", err)
	}
	cachePath := filepath.Join(cacheDir, src.SHA256+".bin")
	if cached, err := openCachedImage(cachePath, src.SHA256, maxImageBytes); err == nil {
		return cached, nil
	} else if errors.Is(err, errUnsafeCacheFile) || errors.Is(err, errImageTooLarge) {
		return nil, fmt.Errorf("image source cache: %w", err)
	} else if !os.IsNotExist(err) {
		// A corrupt or incomplete entry must not coexist with the replacement
		// download at full size. It is safe to unlink because openCachedImage has
		// already rejected its content and the directory is process-private.
		if removeErr := os.Remove(cachePath); removeErr != nil && !os.IsNotExist(removeErr) {
			return nil, fmt.Errorf("image source cache: remove rejected entry: %w", removeErr)
		}
	}

	materialized, err := r.resolveURL(ctx, namespace, src, cacheDir, maxImageBytes)
	if err != nil {
		return nil, err
	}
	if materialized == nil {
		return nil, errors.New("image source URL materialized no image")
	}
	return publishMaterializedImage(materialized, cachePath, src.SHA256, maxImageBytes)
}

func retainOnlyCacheDigest(cacheDir, keepDigest string) error {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return err
	}
	keepName := keepDigest + ".bin"
	for _, entry := range entries {
		name := entry.Name()
		if name == keepName {
			continue
		}
		if strings.HasPrefix(name, cacheTempPrefix) {
			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("inspect orphan temp %s: %w", name, err)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%w: orphan temp %s is not a regular file", errUnsafeCacheFile, name)
			}
		} else if !managedCacheName(name) {
			continue
		}
		if err := os.Remove(filepath.Join(cacheDir, name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("evict %s: %w", name, err)
		}
	}
	return nil
}

func managedCacheName(name string) bool {
	if !strings.HasSuffix(name, ".bin") {
		return false
	}
	return validSHA256Hex(strings.TrimSuffix(name, ".bin"))
}

func (r *DefaultImageResolver) resolveURL(ctx context.Context, namespace string, src opsv1alpha1.UpgradeImageSource, cacheDir string, maxImageBytes int64) (*materializedImage, error) {
	if src.SHA256 == "" {
		return nil, errors.New("image source URL requires SHA256 verification")
	}
	u, err := url.Parse(src.URL)
	if err != nil {
		return nil, &redactedURLParseError{endpoint: redactRawURL(src.URL), cause: err}
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return r.resolveHTTPURL(ctx, u, src.SHA256, cacheDir, maxImageBytes)
	case "tftp":
		return r.resolveTFTPURL(ctx, u, src.SHA256, cacheDir, maxImageBytes)
	case "ftp":
		return r.resolveFTPURL(ctx, namespace, u, src, cacheDir, maxImageBytes)
	case "scp":
		return r.resolveSCPURL(ctx, namespace, u, src, cacheDir, maxImageBytes)
	case "sftp":
		return r.resolveSFTPURL(ctx, namespace, u, src, cacheDir, maxImageBytes)
	default:
		return nil, fmt.Errorf("image source URL: unsupported scheme %q", u.Scheme)
	}
}

func (r *DefaultImageResolver) resolveHTTPURL(ctx context.Context, u *url.URL, sha256Hex, cacheDir string, maxImageBytes int64) (*materializedImage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("image source HTTP %s: invalid request", redactURL(u))
	}
	httpClient := r.HTTPClient
	if _, managed := managedImageSourcePolicyFromContext(ctx); managed {
		httpClient, err = managedHTTPClient(ctx, httpClient, u)
		if err != nil {
			return nil, err
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		cause := unwrapURLError(err)
		requestErr := &redactedHTTPError{operation: "get", endpoint: redactURL(u), cause: cause}
		if retryableConnectionError(cause) {
			return nil, MarkRetryableResolveError(requestErr)
		}
		return nil, requestErr
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		statusErr := fmt.Errorf("image source HTTP %s returned status %d", redactURL(u), resp.StatusCode)
		if retryableHTTPStatus(resp.StatusCode) {
			return nil, MarkRetryableResolveError(statusErr)
		}
		return nil, statusErr
	}
	if err := validateImageSize("image source HTTP Content-Length", resp.ContentLength, maxImageBytes); err != nil {
		return nil, err
	}
	return materializeRemoteImage("image source HTTP", sha256Hex, cacheDir, maxImageBytes, func(w io.Writer) (int64, error) {
		body := &retryableHTTPBodyReader{reader: resp.Body, endpoint: redactURL(u)}
		return io.Copy(w, body)
	})
}

func retryableHTTPStatus(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusTooEarly ||
		status == http.StatusTooManyRequests ||
		status/100 == 5
}

func retryableConnectionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}

func classifyConnectionFailure(err error) error {
	if retryableConnectionError(err) {
		return MarkRetryableResolveError(err)
	}
	return err
}

func retryableFTPError(err error) bool {
	if retryableConnectionError(err) {
		return true
	}
	var protocolErr *textproto.Error
	return errors.As(err, &protocolErr) && protocolErr.Code/100 == 4 && protocolErr.Code != ftp.StatusInvalidCredentials
}

func classifyFTPFailure(err error) error {
	if retryableFTPError(err) {
		return MarkRetryableResolveError(err)
	}
	return err
}

func retryableSFTPError(err error) bool {
	if retryableConnectionError(err) || errors.Is(err, sftp.ErrSSHFxNoConnection) || errors.Is(err, sftp.ErrSSHFxConnectionLost) {
		return true
	}
	var statusErr *sftp.StatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	code := statusErr.FxCode()
	return code == sftp.ErrSSHFxNoConnection || code == sftp.ErrSSHFxConnectionLost
}

func classifySFTPFailure(err error) error {
	if retryableSFTPError(err) {
		return MarkRetryableResolveError(err)
	}
	return err
}

type classifiedReader struct {
	reader   io.Reader
	classify func(error) bool
}

func (reader *classifiedReader) Read(p []byte) (int, error) {
	n, err := reader.reader.Read(p)
	if err == nil || errors.Is(err, io.EOF) || !reader.classify(err) {
		return n, err
	}
	return n, MarkRetryableResolveError(err)
}

type retryableContextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (writer *retryableContextWriter) Write(p []byte) (int, error) {
	if err := writer.ctx.Err(); err != nil {
		return 0, MarkRetryableResolveError(err)
	}
	return writer.writer.Write(p)
}

func (r *DefaultImageResolver) resolveTFTPURL(ctx context.Context, u *url.URL, sha256Hex, cacheDir string, maxImageBytes int64) (*materializedImage, error) {
	filename := strings.TrimPrefix(u.Path, "/")
	if filename == "" {
		return nil, errors.New("image source TFTP: URL path is required")
	}
	addr, err := hostPort(u, "69")
	if err != nil {
		return nil, fmt.Errorf("image source TFTP: %w", err)
	}
	c, err := tftp.NewClient(addr)
	if err != nil {
		return nil, classifyConnectionFailure(fmt.Errorf("image source TFTP client: %w", err))
	}
	blockSize := r.TFTPBlockSize
	if blockSize <= 0 {
		blockSize = defaultTFTPBlockSize
	}
	c.SetBlockSize(blockSize)
	c.SetRetries(defaultTFTPRetries)
	c.SetTimeout(defaultTFTPTimeout)
	c.RequestTSize(true)

	return materializeRemoteImage("image source TFTP", sha256Hex, cacheDir, maxImageBytes, func(w io.Writer) (int64, error) {
		if err := ctx.Err(); err != nil {
			return 0, classifyConnectionFailure(err)
		}
		wt, err := c.Receive(filename, "octet")
		if err != nil {
			return 0, classifyConnectionFailure(err)
		}
		if incoming, ok := wt.(tftp.IncomingTransfer); ok {
			if size, known := incoming.Size(); known {
				if err := validateImageSize("image source TFTP transfer size", size, maxImageBytes); err != nil {
					return 0, err
				}
			}
		}
		n, err := wt.WriteTo(&retryableContextWriter{ctx: ctx, writer: w})
		return n, classifyConnectionFailure(err)
	})
}

func (r *DefaultImageResolver) resolveFTPURL(ctx context.Context, namespace string, u *url.URL, src opsv1alpha1.UpgradeImageSource, cacheDir string, maxImageBytes int64) (*materializedImage, error) {
	label := fmt.Sprintf("image source FTP %s", redactURL(u))
	path, err := requiredRemotePath(u, "FTP")
	if err != nil {
		return nil, err
	}
	addr, err := hostPort(u, "21")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	creds, err := r.urlCredentials(ctx, namespace, u, src.URLSecretRef)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	username := creds.Username
	password := creds.Password
	if username == "" {
		username = "anonymous"
	}
	if password == "" {
		password = "anonymous@"
	}
	dialer := net.Dialer{Timeout: 30 * time.Second}
	conn, err := ftp.Dial(addr,
		ftp.DialWithContext(ctx),
		ftp.DialWithShutTimeout(5*time.Second),
		ftp.DialWithDialFunc(func(network, address string) (net.Conn, error) {
			connection, dialErr := dialer.DialContext(ctx, network, address)
			if dialErr != nil {
				return nil, dialErr
			}
			if deadline, ok := ctx.Deadline(); ok {
				if deadlineErr := connection.SetDeadline(deadline); deadlineErr != nil {
					_ = connection.Close()
					return nil, deadlineErr
				}
			}
			context.AfterFunc(ctx, func() { _ = connection.Close() })
			return connection, nil
		}),
	)
	if err != nil {
		return nil, classifyFTPFailure(fmt.Errorf("%s dial: %w", label, err))
	}
	defer func() { _ = conn.Quit() }()
	if err := conn.Login(username, password); err != nil {
		return nil, classifyFTPFailure(fmt.Errorf("%s login: %w", label, err))
	}
	if err := conn.Type(ftp.TransferTypeBinary); err != nil {
		return nil, classifyFTPFailure(fmt.Errorf("%s binary mode: %w", label, err))
	}
	if size, err := conn.FileSize(path); err == nil {
		if err := validateImageSize(label+" file size", size, maxImageBytes); err != nil {
			return nil, err
		}
	}
	resp, err := conn.Retr(path)
	if err != nil {
		return nil, classifyFTPFailure(fmt.Errorf("%s retrieve %s: %w", label, path, err))
	}
	defer func() { _ = resp.Close() }()
	return materializeRemoteImage(label, src.SHA256, cacheDir, maxImageBytes, func(w io.Writer) (int64, error) {
		return io.Copy(w, &classifiedReader{reader: resp, classify: retryableConnectionError})
	})
}

func (r *DefaultImageResolver) resolveSCPURL(ctx context.Context, namespace string, u *url.URL, src opsv1alpha1.UpgradeImageSource, cacheDir string, maxImageBytes int64) (*materializedImage, error) {
	label := fmt.Sprintf("image source SCP %s", redactURL(u))
	path, err := requiredRemotePath(u, "SCP")
	if err != nil {
		return nil, err
	}
	client, err := r.sshClient(ctx, namespace, u, src.URLSecretRef)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	defer func() { _ = client.Close() }()
	stopCancel := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stopCancel()
	return materializeRemoteImage(label, src.SHA256, cacheDir, maxImageBytes, func(w io.Writer) (int64, error) {
		n, err := scpDownload(ctx, client, path, w, maxImageBytes)
		return n, classifyConnectionFailure(err)
	})
}

func (r *DefaultImageResolver) resolveSFTPURL(ctx context.Context, namespace string, u *url.URL, src opsv1alpha1.UpgradeImageSource, cacheDir string, maxImageBytes int64) (*materializedImage, error) {
	label := fmt.Sprintf("image source SFTP %s", redactURL(u))
	path, err := requiredRemotePath(u, "SFTP")
	if err != nil {
		return nil, err
	}
	client, err := r.sshClient(ctx, namespace, u, src.URLSecretRef)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	defer func() { _ = client.Close() }()
	stopCancel := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stopCancel()
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return nil, classifyConnectionFailure(fmt.Errorf("%s client: %w", label, err))
	}
	defer func() { _ = sftpClient.Close() }()
	info, err := sftpClient.Stat(path)
	if err != nil {
		return nil, classifySFTPFailure(fmt.Errorf("%s stat %s: %w", label, path, err))
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: remote path %s is not a regular file", label, path)
	}
	if err := validateImageSize(label+" file size", info.Size(), maxImageBytes); err != nil {
		return nil, err
	}
	file, err := sftpClient.Open(path)
	if err != nil {
		return nil, classifySFTPFailure(fmt.Errorf("%s open %s: %w", label, path, err))
	}
	defer func() { _ = file.Close() }()
	return materializeRemoteImage(label, src.SHA256, cacheDir, maxImageBytes, func(w io.Writer) (int64, error) {
		return io.Copy(w, &classifiedReader{reader: file, classify: retryableSFTPError})
	})
}

type materializedImage struct {
	file *os.File
	path string
	size int64
}

func materializeRemoteImage(label, sha256Hex, cacheDir string, maxImageBytes int64, fetch func(io.Writer) (int64, error)) (*materializedImage, error) {
	tmp, err := os.CreateTemp(cacheDir, cacheTempPrefix+"*")
	if err != nil {
		return nil, fmt.Errorf("%s: temp file: %w", label, err)
	}
	materialized := &materializedImage{file: tmp, path: tmp.Name()}
	fail := func(cause error) (*materializedImage, error) {
		if cleanupErr := materialized.closeAndRemove(); cleanupErr != nil {
			cause = errors.Join(cause, fmt.Errorf("%s: clean up temp file: %w", label, cleanupErr))
		}
		return nil, cause
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(fmt.Errorf("%s: secure temp file: %w", label, err))
	}
	hash := sha256.New()
	limited := &maxBytesWriter{Writer: io.MultiWriter(tmp, hash), Max: maxImageBytes, Label: label}
	_, err = fetch(limited)
	if err != nil {
		return fail(fmt.Errorf("%s: stream into temp file: %w", label, err))
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != sha256Hex {
		return fail(fmt.Errorf("%s: SHA256 mismatch: got %s want %s", label, got, sha256Hex))
	}
	materialized.size = limited.Written
	if err := validateMaterializedImage(materialized, maxImageBytes); err != nil {
		return fail(fmt.Errorf("%s: %w", label, err))
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return fail(fmt.Errorf("%s: temp rewind: %w", label, err))
	}
	return materialized, nil
}

func publishMaterializedImage(materialized *materializedImage, cachePath, sha256Hex string, maxImageBytes int64) (_ *ResolvedImage, retErr error) {
	if materialized == nil {
		return nil, errors.New("image source cache: materialized image is incomplete")
	}
	defer func() {
		if materialized.file == nil {
			return
		}
		if cleanupErr := materialized.closeAndRemove(); cleanupErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("image source cache: clean up temp file: %w", cleanupErr))
		}
	}()
	if materialized.file == nil || materialized.path == "" {
		return nil, errors.New("image source cache: materialized image is incomplete")
	}

	if err := validateMaterializedImage(materialized, maxImageBytes); err != nil {
		return nil, fmt.Errorf("image source cache: %w", err)
	}
	descriptorInfo, err := materialized.file.Stat()
	if err != nil {
		return nil, fmt.Errorf("image source cache: stat verified temp file: %w", err)
	}
	if err := os.Rename(materialized.path, cachePath); err != nil {
		return nil, fmt.Errorf("image source cache: store: %w", err)
	}
	materialized.path = cachePath
	publishedInfo, err := os.Lstat(cachePath)
	if err != nil {
		return nil, fmt.Errorf("image source cache: inspect stored image: %w", err)
	}
	if !publishedInfo.Mode().IsRegular() || !os.SameFile(descriptorInfo, publishedInfo) {
		return nil, fmt.Errorf("%w: %s changed while storing", errUnsafeCacheFile, cachePath)
	}
	if publishedInfo.Size() != materialized.size {
		return nil, fmt.Errorf("image source cache: stored image size changed: got %d want %d", publishedInfo.Size(), materialized.size)
	}
	if publishedInfo.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("%w: %s has permissions %#o; want 0600", errUnsafeCacheFile, cachePath, publishedInfo.Mode().Perm())
	}
	if _, err := materialized.file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("image source cache: rewind stored image: %w", err)
	}

	file := materialized.file
	size := materialized.size
	materialized.file = nil
	materialized.path = ""
	return &ResolvedImage{
		Reader:  file,
		Size:    size,
		Digest:  "sha256:" + sha256Hex,
		Cleanup: file.Close,
	}, nil
}

func validateMaterializedImage(materialized *materializedImage, maxImageBytes int64) error {
	if materialized == nil || materialized.file == nil || materialized.path == "" {
		return errors.New("materialized image is incomplete")
	}
	descriptorInfo, err := materialized.file.Stat()
	if err != nil {
		return fmt.Errorf("stat temp file: %w", err)
	}
	if !descriptorInfo.Mode().IsRegular() {
		return fmt.Errorf("%w: temp descriptor is not a regular file", errUnsafeCacheFile)
	}
	if descriptorInfo.Size() != materialized.size {
		return fmt.Errorf("temp file size changed: got %d want %d", descriptorInfo.Size(), materialized.size)
	}
	if err := validateImageSize("image source cache", descriptorInfo.Size(), maxImageBytes); err != nil {
		return err
	}
	pathInfo, err := os.Lstat(materialized.path)
	if err != nil {
		return fmt.Errorf("inspect temp file: %w", err)
	}
	if !pathInfo.Mode().IsRegular() || !os.SameFile(descriptorInfo, pathInfo) {
		return fmt.Errorf("%w: temp path changed while materializing", errUnsafeCacheFile)
	}
	if pathInfo.Mode().Perm() != 0o600 {
		return fmt.Errorf("%w: temp file has permissions %#o; want 0600", errUnsafeCacheFile, pathInfo.Mode().Perm())
	}
	return nil
}

func (materialized *materializedImage) closeAndRemove() error {
	if materialized == nil {
		return nil
	}
	file := materialized.file
	path := materialized.path
	materialized.file = nil
	materialized.path = ""
	if file == nil {
		return nil
	}
	descriptorInfo, statErr := file.Stat()
	closeErr := file.Close()
	var removeErr error
	if path != "" && statErr == nil {
		pathInfo, err := os.Lstat(path)
		switch {
		case err == nil && os.SameFile(descriptorInfo, pathInfo):
			removeErr = os.Remove(path)
			if os.IsNotExist(removeErr) {
				removeErr = nil
			}
		case err == nil:
			removeErr = fmt.Errorf("%w: temp path changed before cleanup", errUnsafeCacheFile)
		case !os.IsNotExist(err):
			removeErr = err
		}
	}
	return errors.Join(statErr, closeErr, removeErr)
}

type redactedHTTPError struct {
	operation string
	endpoint  string
	cause     error
}

type redactedURLParseError struct {
	endpoint string
	cause    error
}

func (err *redactedURLParseError) Error() string {
	return fmt.Sprintf("image source URL %s is invalid", err.endpoint)
}

func (err *redactedURLParseError) Unwrap() error {
	return err.cause
}

func (err *redactedHTTPError) Error() string {
	return fmt.Sprintf("image source HTTP %s %s: failed", err.operation, err.endpoint)
}

func (err *redactedHTTPError) Unwrap() error {
	return err.cause
}

type retryableHTTPBodyReader struct {
	reader   io.Reader
	endpoint string
}

func (reader *retryableHTTPBodyReader) Read(p []byte) (int, error) {
	n, err := reader.reader.Read(p)
	if err == nil || errors.Is(err, io.EOF) {
		return n, err
	}
	return n, MarkRetryableResolveError(&redactedHTTPError{operation: "read", endpoint: reader.endpoint, cause: err})
}

func unwrapURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}
	return err
}

func openCachedImage(path, sha256Hex string, maxImageBytes int64) (*ResolvedImage, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", errUnsafeCacheFile, path)
	}
	if before.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("%w: %s has permissions %#o; want 0600", errUnsafeCacheFile, path, before.Mode().Perm())
	}
	if err := validateImageSize("image source cache", before.Size(), maxImageBytes); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = f.Close()
		return nil, fmt.Errorf("%w: %s changed while opening", errUnsafeCacheFile, path)
	}
	hash := sha256.New()
	n, err := io.Copy(hash, f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != sha256Hex {
		_ = f.Close()
		return nil, fmt.Errorf("cache SHA256 mismatch: got %s want %s", got, sha256Hex)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &ResolvedImage{
		Reader:  f,
		Size:    n,
		Digest:  "sha256:" + sha256Hex,
		Cleanup: f.Close,
	}, nil
}

func ensurePrivateCacheDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("cache path is not a private directory")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	after, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !after.IsDir() || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, after) {
		return errors.New("cache directory changed while securing it")
	}
	return nil
}

func validateImageSize(label string, size, max int64) error {
	if size >= 0 && size > max {
		return fmt.Errorf("%w: %s is %d bytes; maximum is %d", errImageTooLarge, label, size, max)
	}
	return nil
}

type maxBytesWriter struct {
	Writer  io.Writer
	Max     int64
	Written int64
	Label   string
}

func (w *maxBytesWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	remaining := w.Max - w.Written
	if remaining <= 0 {
		return 0, fmt.Errorf("%w: %s exceeds %d bytes", errImageTooLarge, w.Label, w.Max)
	}
	if int64(len(p)) > remaining {
		n, err := w.Writer.Write(p[:remaining])
		w.Written += int64(n)
		if err != nil {
			return n, err
		}
		if int64(n) != remaining {
			return n, io.ErrShortWrite
		}
		return n, fmt.Errorf("%w: %s exceeds %d bytes", errImageTooLarge, w.Label, w.Max)
	}
	n, err := w.Writer.Write(p)
	w.Written += int64(n)
	if n != len(p) && err == nil {
		return n, io.ErrShortWrite
	}
	return n, err
}

type transferCredentials struct {
	Username   string
	Password   string
	PrivateKey []byte
	Passphrase []byte
	KnownHosts []byte
}

func (r *DefaultImageResolver) urlCredentials(ctx context.Context, namespace string, u *url.URL, ref *corev1.LocalObjectReference) (*transferCredentials, error) {
	creds := &transferCredentials{}
	if ref != nil {
		secret, err := r.authorizedURLSecret(ctx, namespace, u, ref)
		if err != nil {
			return nil, err
		}
		creds.Username = secretString(secret.Data, "username", "user")
		creds.Password = secretString(secret.Data, "password")
		creds.PrivateKey = secretBytes(secret.Data, "privateKey", "private_key", "ssh-privatekey")
		creds.Passphrase = secretBytes(secret.Data, "passphrase")
		creds.KnownHosts = secretBytes(secret.Data, "knownHosts", "known_hosts")
	}
	return creds, nil
}

func (r *DefaultImageResolver) authorizeURLSecret(ctx context.Context, namespace string, u *url.URL, ref *corev1.LocalObjectReference) error {
	if u == nil {
		return errors.New("image source URL is invalid")
	}
	if u.User != nil {
		return errors.New("image source URL must not contain user information; use an endpoint-bound urlSecretRef")
	}
	if ref == nil {
		return nil
	}
	_, err := r.authorizedURLSecret(ctx, namespace, u, ref)
	return err
}

func (r *DefaultImageResolver) authorizedURLSecret(ctx context.Context, namespace string, u *url.URL, ref *corev1.LocalObjectReference) (*corev1.Secret, error) {
	if u == nil {
		return nil, errors.New("image source URL is invalid")
	}
	if u.User != nil {
		return nil, errors.New("image source URL must not contain user information; use an endpoint-bound urlSecretRef")
	}
	if ref == nil || strings.TrimSpace(ref.Name) == "" {
		return nil, errors.New("image source URL secretRef: name is required")
	}
	if r.K8sClient == nil {
		return nil, errors.New("image source URL secretRef: K8sClient not configured on resolver")
	}
	var secret corev1.Secret
	if err := r.K8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, &secret); err != nil {
		getErr := fmt.Errorf("image source URL secretRef get: %w", err)
		if retryableKubernetesGetError(err) {
			return nil, MarkRetryableResolveError(getErr)
		}
		return nil, getErr
	}
	if policy, managed := managedImageSourcePolicyFromContext(ctx); managed {
		if policy.sourceSecretUID == "" {
			return nil, errors.New("managed image source Secret identity binding is missing")
		}
		if string(secret.UID) != policy.sourceSecretUID {
			return nil, errors.New("managed image source Secret incarnation changed after admission")
		}
	}
	if err := validateURLSecretEndpoint(&secret, u); err != nil {
		return nil, err
	}
	return &secret, nil
}

// ValidateURLSecretEndpoint verifies that a Secret is explicitly purposed for
// software-image retrieval and authorizes exactly rawURL's scheme, canonical
// host, and port. Credentials and known-host material may rotate in place, but
// changing the endpoint binding or replacing the Secret UID requires a new
// manager-approved plan. Callers must separately compare the live Secret UID
// with that frozen identity.
func ValidateURLSecretEndpoint(secret *corev1.Secret, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return errors.New("image source URL is invalid")
	}
	return validateURLSecretEndpoint(secret, u)
}

func validateURLSecretEndpoint(secret *corev1.Secret, u *url.URL) error {
	if secret == nil {
		return errors.New("image source URL secretRef is nil")
	}
	if secret.Labels[URLSecretPurposeLabel] != URLSecretPurposeValue {
		return fmt.Errorf("image source URL secretRef must have label %s=%s", URLSecretPurposeLabel, URLSecretPurposeValue)
	}
	scheme, host, port, err := canonicalImageEndpoint(u)
	if err != nil {
		return err
	}
	allowedScheme, allowedHost, allowedPort, err := secretEndpointBinding(secret)
	if err != nil {
		return err
	}
	if scheme != allowedScheme || host != allowedHost || port != allowedPort {
		return errors.New("image source URL secretRef does not authorize the requested URL endpoint")
	}
	return nil
}

func canonicalImageEndpoint(u *url.URL) (scheme, host, port string, err error) {
	if u == nil {
		return "", "", "", errors.New("image source URL is invalid")
	}
	scheme = strings.ToLower(strings.TrimSpace(u.Scheme))
	switch scheme {
	case "ftp":
		port = "21"
	case "scp", "sftp":
		port = "22"
	default:
		return "", "", "", fmt.Errorf("image source URL secretRef is unsupported for scheme %q", scheme)
	}
	host, err = canonicalEndpointHost(u.Hostname())
	if err != nil {
		return "", "", "", err
	}
	if explicitPort := u.Port(); explicitPort != "" {
		parsed, parseErr := strconv.ParseUint(explicitPort, 10, 16)
		if parseErr != nil || parsed == 0 {
			return "", "", "", errors.New("image source URL has an invalid port")
		}
		port = strconv.FormatUint(parsed, 10)
	}
	return scheme, host, port, nil
}

func secretEndpointBinding(secret *corev1.Secret) (scheme, host, port string, err error) {
	if secret == nil {
		return "", "", "", errors.New("image source URL secretRef is nil")
	}
	for _, key := range []string{URLSecretAllowedSchemeKey, URLSecretAllowedHostKey, URLSecretAllowedPortKey} {
		if strings.TrimSpace(string(secret.Data[key])) == "" {
			return "", "", "", fmt.Errorf("image source URL secretRef data[%q] is required", key)
		}
	}
	scheme = strings.ToLower(strings.TrimSpace(string(secret.Data[URLSecretAllowedSchemeKey])))
	switch scheme {
	case "ftp", "scp", "sftp":
	default:
		return "", "", "", fmt.Errorf("image source URL secretRef data[%q] is invalid", URLSecretAllowedSchemeKey)
	}
	host, err = canonicalEndpointHost(string(secret.Data[URLSecretAllowedHostKey]))
	if err != nil {
		return "", "", "", fmt.Errorf("image source URL secretRef data[%q] is invalid", URLSecretAllowedHostKey)
	}
	parsedPort, parseErr := strconv.ParseUint(strings.TrimSpace(string(secret.Data[URLSecretAllowedPortKey])), 10, 16)
	if parseErr != nil || parsedPort == 0 {
		return "", "", "", fmt.Errorf("image source URL secretRef data[%q] is invalid", URLSecretAllowedPortKey)
	}
	port = strconv.FormatUint(parsedPort, 10)
	return scheme, host, port, nil
}

func canonicalEndpointHost(raw string) (string, error) {
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	if host == "" || strings.ContainsAny(host, "/@") {
		return "", errors.New("host is empty or malformed")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		host = addr.Unmap().String()
	}
	return host, nil
}

func secretString(data map[string][]byte, keys ...string) string {
	return string(secretBytes(data, keys...))
}

func secretBytes(data map[string][]byte, keys ...string) []byte {
	for _, key := range keys {
		if v, ok := data[key]; ok {
			return v
		}
	}
	return nil
}

func (r *DefaultImageResolver) sshClient(ctx context.Context, namespace string, u *url.URL, ref *corev1.LocalObjectReference) (*ssh.Client, error) {
	if _, managed := managedImageSourcePolicyFromContext(ctx); managed {
		if err := validateManagedSSHURL(u, ref); err != nil {
			return nil, err
		}
	}
	creds, err := r.urlCredentials(ctx, namespace, u, ref)
	if err != nil {
		return nil, err
	}
	if creds.Username == "" {
		return nil, errors.New("image source SSH: username is required via urlSecretRef")
	}
	auth, err := sshAuthMethods(creds)
	if err != nil {
		return nil, err
	}
	hostKeyCallback, err := sshHostKeyCallback(u, creds)
	if err != nil {
		return nil, err
	}
	addr, err := hostPort(u, "22")
	if err != nil {
		return nil, fmt.Errorf("image source SSH: %w", err)
	}
	cfg := &ssh.ClientConfig{
		User:            creds.Username,
		Auth:            auth,
		HostKeyCallback: hostKeyCallback,
		Timeout:         30 * time.Second,
	}
	var conn net.Conn
	if _, managed := managedImageSourcePolicyFromContext(ctx); managed {
		dialContext, dialErr := managedEndpointDialer(ctx, u, "22", true)
		if dialErr != nil {
			return nil, dialErr
		}
		conn, err = dialContext(ctx, "tcp", addr)
	} else {
		dialer := net.Dialer{Timeout: 30 * time.Second}
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, classifyConnectionFailure(fmt.Errorf("image source SSH dial: %w", err))
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	handshakeDeadline := time.Now().Add(cfg.Timeout)
	contextDeadlineControlsHandshake := false
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(handshakeDeadline) {
		handshakeDeadline = deadline
		contextDeadlineControlsHandshake = true
	}
	if err := conn.SetDeadline(handshakeDeadline); err != nil {
		stopCancel()
		_ = conn.Close()
		return nil, classifyConnectionFailure(fmt.Errorf("image source SSH handshake deadline: %w", err))
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	stopCancel()
	if err != nil {
		_ = conn.Close()
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, classifyConnectionFailure(fmt.Errorf("image source SSH handshake: %w", contextErr))
		}
		var netErr net.Error
		if contextDeadlineControlsHandshake && errors.As(err, &netErr) && netErr.Timeout() {
			// The socket deadline and the context timer expire at the same
			// instant. The socket may win that race before ctx.Err() is set;
			// preserve the causal context classification for callers.
			return nil, classifyConnectionFailure(fmt.Errorf("image source SSH handshake: %w (%v)", context.DeadlineExceeded, err))
		}
		return nil, classifyConnectionFailure(fmt.Errorf("image source SSH handshake: %w", err))
	}
	if err := ctx.Err(); err != nil {
		_ = sshConn.Close()
		return nil, classifyConnectionFailure(fmt.Errorf("image source SSH handshake: %w", err))
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = sshConn.Close()
		return nil, classifyConnectionFailure(fmt.Errorf("image source SSH clear handshake deadline: %w", err))
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

func sshAuthMethods(creds *transferCredentials) ([]ssh.AuthMethod, error) {
	var auth []ssh.AuthMethod
	if len(creds.PrivateKey) > 0 {
		var (
			signer ssh.Signer
			err    error
		)
		if len(creds.Passphrase) > 0 {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(creds.PrivateKey, creds.Passphrase)
		} else {
			signer, err = ssh.ParsePrivateKey(creds.PrivateKey)
		}
		if err != nil {
			return nil, fmt.Errorf("image source SSH privateKey: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if creds.Password != "" {
		auth = append(auth, ssh.Password(creds.Password))
	}
	if len(auth) == 0 {
		return nil, errors.New("image source SSH: password or privateKey is required")
	}
	return auth, nil
}

func sshHostKeyCallback(u *url.URL, creds *transferCredentials) (ssh.HostKeyCallback, error) {
	if queryBool(u, "insecureSkipHostKey") || queryBool(u, "insecure") {
		allowed, _ := strconv.ParseBool(os.Getenv(envAllowInsecureSSH))
		if !allowed {
			return nil, fmt.Errorf("image source SSH: insecure host key verification requires %s=true", envAllowInsecureSSH)
		}
		return ssh.InsecureIgnoreHostKey(), nil //nolint:gosec // explicit per-URL lab escape hatch
	}
	if len(creds.KnownHosts) == 0 {
		return nil, errors.New("image source SSH: knownHosts/known_hosts is required unless insecureSkipHostKey=true is set")
	}
	tmp, err := os.CreateTemp("", "cvk-known-hosts-*")
	if err != nil {
		return nil, fmt.Errorf("image source SSH known_hosts temp file: %w", err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(creds.KnownHosts); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return nil, fmt.Errorf("image source SSH known_hosts write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return nil, fmt.Errorf("image source SSH known_hosts close: %w", err)
	}
	cb, err := knownhosts.New(name)
	_ = os.Remove(name)
	if err != nil {
		return nil, fmt.Errorf("image source SSH known_hosts parse: %w", err)
	}
	return cb, nil
}

func scpDownload(ctx context.Context, client *ssh.Client, path string, w io.Writer, maxImageBytes int64) (int64, error) {
	session, err := client.NewSession()
	if err != nil {
		return 0, fmt.Errorf("image source SCP session: %w", err)
	}
	defer func() { _ = session.Close() }()

	stdout, err := session.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("image source SCP stdout: %w", err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		return 0, fmt.Errorf("image source SCP stdin: %w", err)
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close()
		case <-done:
		}
	}()
	defer close(done)

	if err := session.Start("scp -f " + shellQuote(path)); err != nil {
		return 0, fmt.Errorf("image source SCP start: %w", err)
	}
	ack := func() error {
		_, err := stdin.Write([]byte{0})
		return err
	}
	if err := ack(); err != nil {
		return 0, fmt.Errorf("image source SCP initial ack: %w", err)
	}
	reader := bufio.NewReader(stdout)
	for {
		op, line, err := readSCPLine(reader)
		if err != nil {
			return 0, err
		}
		switch op {
		case 'T':
			if err := ack(); err != nil {
				return 0, fmt.Errorf("image source SCP timestamp ack: %w", err)
			}
			continue
		case 'C':
			fields := strings.SplitN(line, " ", 3)
			if len(fields) < 3 {
				return 0, fmt.Errorf("image source SCP malformed file header %q", line)
			}
			size, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil || size < 0 {
				return 0, fmt.Errorf("image source SCP invalid file size %q", fields[1])
			}
			if err := validateImageSize("image source SCP file size", size, maxImageBytes); err != nil {
				return 0, err
			}
			if err := ack(); err != nil {
				return 0, fmt.Errorf("image source SCP file ack: %w", err)
			}
			n, err := io.CopyN(w, reader, size)
			if err != nil {
				return n, fmt.Errorf("image source SCP copy: %w", err)
			}
			if err := readSCPStatus(reader); err != nil {
				return n, err
			}
			if err := ack(); err != nil {
				return n, fmt.Errorf("image source SCP final ack: %w", err)
			}
			if err := session.Wait(); err != nil {
				return n, fmt.Errorf("image source SCP wait: %w", err)
			}
			return n, nil
		case 'D':
			return 0, errors.New("image source SCP: directories are not supported")
		default:
			return 0, fmt.Errorf("image source SCP unexpected response %q %q", op, line)
		}
	}
}

func readSCPLine(r *bufio.Reader) (byte, string, error) {
	op, err := r.ReadByte()
	if err != nil {
		return 0, "", fmt.Errorf("image source SCP read response: %w", err)
	}
	if op == 1 || op == 2 {
		msg, err := readSCPControlLine(r)
		if err != nil {
			return op, "", fmt.Errorf("image source SCP read remote error: %w", err)
		}
		return op, "", fmt.Errorf("image source SCP remote error: %s", strings.TrimSpace(msg))
	}
	line, err := readSCPControlLine(r)
	if err != nil {
		return 0, "", fmt.Errorf("image source SCP read response line: %w", err)
	}
	return op, strings.TrimRight(line, "\r\n"), nil
}

func readSCPStatus(r *bufio.Reader) error {
	status, err := r.ReadByte()
	if err != nil {
		return fmt.Errorf("image source SCP read file status: %w", err)
	}
	if status == 0 {
		return nil
	}
	if status == 1 || status == 2 {
		msg, err := readSCPControlLine(r)
		if err != nil {
			return fmt.Errorf("image source SCP read remote file error: %w", err)
		}
		return fmt.Errorf("image source SCP remote error: %s", strings.TrimSpace(msg))
	}
	return fmt.Errorf("image source SCP unexpected file status byte %d", status)
}

func readSCPControlLine(r *bufio.Reader) (string, error) {
	line := make([]byte, 0, min(r.Size(), maxSCPControlLineSize))
	for {
		fragment, err := r.ReadSlice('\n')
		if len(fragment) > maxSCPControlLineSize-len(line) {
			return "", fmt.Errorf("%w (%d bytes)", errSCPControlLineTooLong, maxSCPControlLineSize)
		}
		line = append(line, fragment...)
		if err == nil {
			return string(line), nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return "", err
		}
	}
}

func hostPort(u *url.URL, defaultPort string) (string, error) {
	host := u.Hostname()
	if host == "" {
		return "", errors.New("URL host is required")
	}
	port := u.Port()
	if port == "" {
		port = defaultPort
	}
	return net.JoinHostPort(host, port), nil
}

func requiredRemotePath(u *url.URL, scheme string) (string, error) {
	if u.Path == "" || u.Path == "/" {
		return "", fmt.Errorf("image source %s: URL path is required", scheme)
	}
	return u.Path, nil
}

func queryBool(u *url.URL, key string) bool {
	raw := u.Query().Get(key)
	if raw == "" {
		return false
	}
	v, err := strconv.ParseBool(raw)
	return err == nil && v
}

func redactURL(u *url.URL) string {
	redacted := *u
	redacted.User = nil
	redacted.RawQuery = ""
	redacted.ForceQuery = false
	redacted.Fragment = ""
	return redacted.String()
}

func redactRawURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		scheme := strings.Index(raw, "://")
		if scheme >= 0 {
			return raw[:scheme+3] + "<invalid-url-redacted>"
		}
		return "<invalid-url-redacted>"
	}
	return redactURL(u)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func (r *DefaultImageResolver) resolveConfigMap(ctx context.Context, namespace, name string, maxImageBytes int64) (*ResolvedImage, error) {
	if r.K8sClient == nil {
		return nil, errors.New("image source configMapRef: K8sClient not configured on resolver")
	}
	var cm corev1.ConfigMap
	if err := r.K8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &cm); err != nil {
		getErr := fmt.Errorf("image source configMapRef get: %w", err)
		if retryableKubernetesGetError(err) {
			return nil, MarkRetryableResolveError(getErr)
		}
		return nil, getErr
	}
	data, ok := cm.BinaryData["image"]
	if !ok {
		return nil, fmt.Errorf("image source configMapRef: ConfigMap %s/%s has no binaryData[\"image\"]", namespace, name)
	}
	if err := validateImageSize("image source ConfigMap", int64(len(data)), maxImageBytes); err != nil {
		return nil, err
	}
	// ConfigMaps cap at ~1 MiB total; the image must fit. No SHA check
	// here — the operator already controls the ConfigMap and we treat
	// it as the source of truth.
	digest := sha256.Sum256(data)
	rd := &readerCloser{r: byteReader(data)}
	return &ResolvedImage{
		Reader:  rd,
		Size:    int64(len(data)),
		Digest:  "sha256:" + hex.EncodeToString(digest[:]),
		Cleanup: func() error { return nil },
	}, nil
}

func retryableKubernetesGetError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) ||
		apierrors.IsTooManyRequests(err) || apierrors.IsServiceUnavailable(err) ||
		apierrors.IsInternalError(err) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
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
