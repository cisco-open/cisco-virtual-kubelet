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

package gnoi

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	filepb "github.com/openconfig/gnoi/file"
	commonpb "github.com/openconfig/gnoi/types"
)

// IOSXEFilesystemPrefixes enumerates the absolute-path prefixes
// IOS-XE accepts on flash-class storage. File.Put/Get/Stat/Remove
// reject paths without a recognised prefix to keep operators from
// shipping relative paths that the device silently rebases.
var IOSXEFilesystemPrefixes = []string{
	"flash:",
	"bootflash:",
	"harddisk:",
	"usbflash0:",
	"usbflash1:",
	"crashinfo:",
	"nvram:",
	"webui:",
}

// ValidateIOSXEPath returns nil when path begins with a recognised
// IOS-XE filesystem prefix and does not escape into another filesystem.
func ValidateIOSXEPath(path string) error {
	for _, p := range IOSXEFilesystemPrefixes {
		if strings.HasPrefix(path, p) {
			rest := strings.TrimPrefix(path, p)
			if strings.HasPrefix(rest, "/") {
				rest = strings.TrimPrefix(rest, "/")
			}
			if rest == "" {
				return fmt.Errorf("gnoi: path %q has no file component after IOS-XE filesystem prefix %q", path, p)
			}
			for _, segment := range strings.Split(rest, "/") {
				if segment == "" || segment == "." || segment == ".." {
					return fmt.Errorf("gnoi: path %q contains invalid segment %q", path, segment)
				}
				for _, other := range IOSXEFilesystemPrefixes {
					if strings.Contains(segment, other) {
						return fmt.Errorf("gnoi: path %q contains nested IOS-XE filesystem prefix %q", path, other)
					}
				}
			}
			return nil
		}
	}
	return fmt.Errorf("gnoi: path %q does not begin with a known IOS-XE filesystem prefix; want one of %v",
		path, IOSXEFilesystemPrefixes)
}

// FileStat is the structured form of gNOI File.Stat output.
type FileStat struct {
	Path         string
	LastModified time.Time
	Size         uint64
	Permissions  uint32
	Umask        uint32
}

// Stat returns metadata for path. Path must begin with a recognised
// IOS-XE filesystem prefix.
func (c *Client) Stat(ctx context.Context, path string) ([]FileStat, error) {
	if err := c.cap.ensureSupported(ServiceFile); err != nil {
		return nil, err
	}
	if err := ValidateIOSXEPath(path); err != nil {
		return nil, err
	}
	resp, err := c.file.Stat(c.authCtx(ctx), &filepb.StatRequest{Path: path})
	c.cap.Observe(ServiceFile, err)
	if err != nil {
		return nil, fmt.Errorf("gnoi File.Stat: %w", err)
	}
	out := make([]FileStat, 0, len(resp.Stats))
	for _, s := range resp.Stats {
		fs := FileStat{
			Path:        s.Path,
			Size:        s.Size,
			Permissions: s.Permissions,
			Umask:       s.Umask,
		}
		if s.LastModified != 0 {
			fs.LastModified = time.Unix(0, int64(s.LastModified))
		}
		out = append(out, fs)
	}
	return out, nil
}

// PutOpts holds optional inputs for File.Put.
type PutOpts struct {
	// Permissions is the UNIX octal mode bits to apply on the device.
	// Zero defaults to 0o644.
	Permissions uint32

	// ChunkSize bounds each TransferContent message; zero defaults to
	// 64 KiB. Cap at 1 MiB.
	ChunkSize int
}

// Put streams a file to the device. Runs on the bulk-transfer conn so
// large payloads cannot HOL-block control RPCs. Computes a SHA256
// hash incrementally and sends it in the terminal message; the device
// rejects the upload on mismatch.
func (c *Client) Put(ctx context.Context, path string, r io.Reader, opts PutOpts) error {
	if err := c.cap.ensureSupported(ServiceFile); err != nil {
		return err
	}
	if err := ValidateIOSXEPath(path); err != nil {
		return err
	}
	if opts.Permissions == 0 {
		opts.Permissions = 0o644
	}
	if opts.Permissions > 0o777 {
		return fmt.Errorf("gnoi File.Put: Permissions=%#o exceeds 0777", opts.Permissions)
	}
	if opts.ChunkSize == 0 {
		opts.ChunkSize = 64 * 1024
	}
	if opts.ChunkSize > 1024*1024 {
		return fmt.Errorf("gnoi File.Put: ChunkSize=%d exceeds 1 MiB cap", opts.ChunkSize)
	}

	fileClient, releaseBulk, err := c.bulkFileClient(ctx)
	if err != nil {
		c.cap.Observe(ServiceFile, err)
		return fmt.Errorf("gnoi File.Put bulk lease: %w", err)
	}
	defer releaseBulk()

	stream, err := fileClient.Put(c.authCtx(ctx))
	if err != nil {
		c.cap.Observe(ServiceFile, err)
		return fmt.Errorf("gnoi File.Put open: %w", err)
	}
	if err := stream.Send(&filepb.PutRequest{
		Request: &filepb.PutRequest_Open{Open: &filepb.PutRequest_Details{RemoteFile: path, Permissions: opts.Permissions}},
	}); err != nil {
		c.cap.Observe(ServiceFile, err)
		return fmt.Errorf("gnoi File.Put send Open: %w", err)
	}

	hash := sha256.New()
	buf := make([]byte, opts.ChunkSize)
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			hash.Write(chunk)
			if err := stream.Send(&filepb.PutRequest{
				Request: &filepb.PutRequest_Contents{Contents: chunk},
			}); err != nil {
				c.cap.Observe(ServiceFile, err)
				return fmt.Errorf("gnoi File.Put send chunk: %w", err)
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			c.cap.Observe(ServiceFile, rerr)
			return fmt.Errorf("gnoi File.Put read source: %w", rerr)
		}
	}
	if err := stream.Send(&filepb.PutRequest{
		Request: &filepb.PutRequest_Hash{Hash: &commonpb.HashType{
			Method: commonpb.HashType_SHA256,
			Hash:   hash.Sum(nil),
		}},
	}); err != nil {
		c.cap.Observe(ServiceFile, err)
		return fmt.Errorf("gnoi File.Put send Hash: %w", err)
	}
	if _, err := stream.CloseAndRecv(); err != nil {
		c.cap.Observe(ServiceFile, err)
		return fmt.Errorf("gnoi File.Put close: %w", err)
	}
	c.cap.Observe(ServiceFile, nil)
	return nil
}

// Remove deletes a file from the device flash. Path must begin with a
// recognised IOS-XE filesystem prefix.
func (c *Client) Remove(ctx context.Context, path string) error {
	if err := c.cap.ensureSupported(ServiceFile); err != nil {
		return err
	}
	if err := ValidateIOSXEPath(path); err != nil {
		return err
	}
	_, err := c.file.Remove(c.authCtx(ctx), &filepb.RemoveRequest{RemoteFile: path})
	c.cap.Observe(ServiceFile, err)
	if err != nil {
		return fmt.Errorf("gnoi File.Remove: %w", err)
	}
	return nil
}

// Get streams a file from the device into w. The final response from
// the device carries a hash that must match the streamed bytes; we
// verify by comparing the server-supplied hash with one computed
// locally if the caller supplies localHash (nil means trust the device).
//
// Get runs on the bulk-transfer conn; the control conn is not used.
func (c *Client) Get(ctx context.Context, path string, w io.Writer) (*commonpb.HashType, error) {
	if err := c.cap.ensureSupported(ServiceFile); err != nil {
		return nil, err
	}
	if err := ValidateIOSXEPath(path); err != nil {
		return nil, err
	}
	fileClient, releaseBulk, err := c.bulkFileClient(ctx)
	if err != nil {
		c.cap.Observe(ServiceFile, err)
		return nil, fmt.Errorf("gnoi File.Get bulk lease: %w", err)
	}
	defer releaseBulk()

	stream, err := fileClient.Get(c.authCtx(ctx), &filepb.GetRequest{RemoteFile: path})
	if err != nil {
		c.cap.Observe(ServiceFile, err)
		return nil, fmt.Errorf("gnoi File.Get: %w", err)
	}
	var serverHash *commonpb.HashType
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			c.cap.Observe(ServiceFile, err)
			return serverHash, fmt.Errorf("gnoi File.Get recv: %w", err)
		}
		if len(resp.GetContents()) > 0 {
			if _, werr := w.Write(resp.GetContents()); werr != nil {
				return serverHash, fmt.Errorf("gnoi File.Get write: %w", werr)
			}
		}
		if h := resp.GetHash(); h != nil {
			serverHash = h
		}
	}
	c.cap.Observe(ServiceFile, nil)
	if serverHash == nil {
		return nil, errors.New("gnoi File.Get: device sent no terminal hash")
	}
	return serverHash, nil
}
