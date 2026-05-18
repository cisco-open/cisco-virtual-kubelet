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
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/pin/tftp/v3"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
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

// DefaultImageResolver dispatches on the populated field. URL → remote
// fetch into a temp file with SHA256 verification; ConfigMapRef → read
// binaryData["image"]; LocalPath → no resolution, the reconciler jumps
// to Activating.
type DefaultImageResolver struct {
	HTTPClient    *http.Client
	K8sClient     client.Client
	TFTPBlockSize int
}

const (
	defaultTFTPBlockSize = 8192
	defaultTFTPRetries   = 10
	defaultTFTPTimeout   = 10 * time.Second
)

// NewDefaultImageResolver constructs a resolver with sensible
// defaults. K8s is mandatory (for ConfigMap reads); httpClient may be
// nil to use http.DefaultClient.
func NewDefaultImageResolver(k8s client.Client, httpClient *http.Client) *DefaultImageResolver {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &DefaultImageResolver{
		HTTPClient:    httpClient,
		K8sClient:     k8s,
		TFTPBlockSize: defaultTFTPBlockSize,
	}
}

func (r *DefaultImageResolver) Resolve(ctx context.Context, namespace string, src opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error) {
	switch {
	case src.LocalPath != "":
		return &ResolvedImage{Local: true}, nil
	case src.URL != "":
		return r.resolveURL(ctx, namespace, src)
	case src.ConfigMapRef != nil:
		return r.resolveConfigMap(ctx, namespace, src.ConfigMapRef.Name)
	default:
		return nil, errors.New("image source: one of url, configMapRef, or localPath is required")
	}
}

func (r *DefaultImageResolver) resolveURL(ctx context.Context, namespace string, src opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error) {
	if src.SHA256 == "" {
		return nil, errors.New("image source URL requires SHA256 verification")
	}
	u, err := url.Parse(src.URL)
	if err != nil {
		return nil, fmt.Errorf("image source URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return r.resolveHTTPURL(ctx, u, src.SHA256)
	case "tftp":
		return r.resolveTFTPURL(ctx, u, src.SHA256)
	case "ftp":
		return r.resolveFTPURL(ctx, namespace, u, src)
	case "scp":
		return r.resolveSCPURL(ctx, namespace, u, src)
	case "sftp":
		return r.resolveSFTPURL(ctx, namespace, u, src)
	default:
		return nil, fmt.Errorf("image source URL: unsupported scheme %q", u.Scheme)
	}
}

func (r *DefaultImageResolver) resolveHTTPURL(ctx context.Context, u *url.URL, sha256Hex string) (*ResolvedImage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("image source HTTP: %w", err)
	}
	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("image source HTTP get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("image source HTTP %s returned status %d", redactURL(u), resp.StatusCode)
	}
	return materializeRemoteImage("image source HTTP", sha256Hex, func(w io.Writer) (int64, error) {
		return io.Copy(w, resp.Body)
	})
}

func (r *DefaultImageResolver) resolveTFTPURL(ctx context.Context, u *url.URL, sha256Hex string) (*ResolvedImage, error) {
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
		return nil, fmt.Errorf("image source TFTP client: %w", err)
	}
	blockSize := r.TFTPBlockSize
	if blockSize <= 0 {
		blockSize = defaultTFTPBlockSize
	}
	c.SetBlockSize(blockSize)
	c.SetRetries(defaultTFTPRetries)
	c.SetTimeout(defaultTFTPTimeout)
	c.RequestTSize(true)

	return materializeRemoteImage("image source TFTP", sha256Hex, func(w io.Writer) (int64, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		wt, err := c.Receive(filename, "octet")
		if err != nil {
			return 0, err
		}
		return wt.WriteTo(w)
	})
}

func (r *DefaultImageResolver) resolveFTPURL(ctx context.Context, namespace string, u *url.URL, src opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error) {
	path, err := requiredRemotePath(u, "FTP")
	if err != nil {
		return nil, err
	}
	addr, err := hostPort(u, "21")
	if err != nil {
		return nil, fmt.Errorf("image source FTP: %w", err)
	}
	creds, err := r.urlCredentials(ctx, namespace, u, src.URLSecretRef)
	if err != nil {
		return nil, err
	}
	username := creds.Username
	password := creds.Password
	if username == "" {
		username = "anonymous"
	}
	if password == "" {
		password = "anonymous@"
	}
	conn, err := ftp.Dial(addr, ftp.DialWithContext(ctx), ftp.DialWithTimeout(30*time.Second))
	if err != nil {
		return nil, fmt.Errorf("image source FTP dial: %w", err)
	}
	defer func() { _ = conn.Quit() }()
	if err := conn.Login(username, password); err != nil {
		return nil, fmt.Errorf("image source FTP login: %w", err)
	}
	if err := conn.Type(ftp.TransferTypeBinary); err != nil {
		return nil, fmt.Errorf("image source FTP binary mode: %w", err)
	}
	resp, err := conn.Retr(path)
	if err != nil {
		return nil, fmt.Errorf("image source FTP retrieve %s: %w", path, err)
	}
	defer func() { _ = resp.Close() }()
	return materializeRemoteImage("image source FTP", src.SHA256, func(w io.Writer) (int64, error) {
		return io.Copy(w, resp)
	})
}

func (r *DefaultImageResolver) resolveSCPURL(ctx context.Context, namespace string, u *url.URL, src opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error) {
	path, err := requiredRemotePath(u, "SCP")
	if err != nil {
		return nil, err
	}
	client, err := r.sshClient(ctx, namespace, u, src.URLSecretRef)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	return materializeRemoteImage("image source SCP", src.SHA256, func(w io.Writer) (int64, error) {
		return scpDownload(ctx, client, path, w)
	})
}

func (r *DefaultImageResolver) resolveSFTPURL(ctx context.Context, namespace string, u *url.URL, src opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error) {
	path, err := requiredRemotePath(u, "SFTP")
	if err != nil {
		return nil, err
	}
	client, err := r.sshClient(ctx, namespace, u, src.URLSecretRef)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("image source SFTP client: %w", err)
	}
	defer func() { _ = sftpClient.Close() }()
	file, err := sftpClient.Open(path)
	if err != nil {
		return nil, fmt.Errorf("image source SFTP open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	return materializeRemoteImage("image source SFTP", src.SHA256, func(w io.Writer) (int64, error) {
		return io.Copy(w, file)
	})
}

func materializeRemoteImage(label, sha256Hex string, fetch func(io.Writer) (int64, error)) (*ResolvedImage, error) {
	tmp, err := os.CreateTemp("", "cvk-upgrade-*.bin")
	if err != nil {
		return nil, fmt.Errorf("%s: temp file: %w", label, err)
	}
	hash := sha256.New()
	n, err := fetch(io.MultiWriter(tmp, hash))
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, fmt.Errorf("%s: stream into temp file: %w", label, err)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != sha256Hex {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, fmt.Errorf("%s: SHA256 mismatch: got %s want %s", label, got, sha256Hex)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, fmt.Errorf("%s: temp rewind: %w", label, err)
	}
	cleanup := func() error {
		_ = tmp.Close()
		return os.Remove(tmp.Name())
	}
	return &ResolvedImage{Reader: tmp, Size: n, Cleanup: cleanup}, nil
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
		if ref.Name == "" {
			return nil, errors.New("image source URL secretRef: name is required")
		}
		if r.K8sClient == nil {
			return nil, errors.New("image source URL secretRef: K8sClient not configured on resolver")
		}
		var secret corev1.Secret
		if err := r.K8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, &secret); err != nil {
			return nil, fmt.Errorf("image source URL secretRef get: %w", err)
		}
		creds.Username = secretString(secret.Data, "username", "user")
		creds.Password = secretString(secret.Data, "password")
		creds.PrivateKey = secretBytes(secret.Data, "privateKey", "private_key", "ssh-privatekey")
		creds.Passphrase = secretBytes(secret.Data, "passphrase")
		creds.KnownHosts = secretBytes(secret.Data, "knownHosts", "known_hosts")
	}
	if u.User != nil {
		if username := u.User.Username(); username != "" {
			creds.Username = username
		}
		if password, ok := u.User.Password(); ok {
			creds.Password = password
		}
	}
	return creds, nil
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
	creds, err := r.urlCredentials(ctx, namespace, u, ref)
	if err != nil {
		return nil, err
	}
	if creds.Username == "" {
		return nil, errors.New("image source SSH: username is required via URL userinfo or urlSecretRef")
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
	dialer := net.Dialer{Timeout: 30 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("image source SSH dial: %w", err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("image source SSH handshake: %w", err)
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

func scpDownload(ctx context.Context, client *ssh.Client, path string, w io.Writer) (int64, error) {
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
		msg, _ := r.ReadString('\n')
		return op, "", fmt.Errorf("image source SCP remote error: %s", strings.TrimSpace(msg))
	}
	line, err := r.ReadString('\n')
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
		msg, _ := r.ReadString('\n')
		return fmt.Errorf("image source SCP remote error: %s", strings.TrimSpace(msg))
	}
	return fmt.Errorf("image source SCP unexpected file status byte %d", status)
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
	if redacted.User != nil {
		redacted.User = url.User(redacted.User.Username())
	}
	return redacted.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
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
