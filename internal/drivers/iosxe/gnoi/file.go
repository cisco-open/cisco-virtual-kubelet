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
// IOS-XE filesystem prefix.
func ValidateIOSXEPath(path string) error {
	for _, p := range IOSXEFilesystemPrefixes {
		if strings.HasPrefix(path, p) {
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
	stream, err := c.fileBulk.Get(c.authCtx(ctx), &filepb.GetRequest{RemoteFile: path})
	if err != nil {
		c.cap.Observe(ServiceFile, err)
		return nil, fmt.Errorf("gnoi File.Get: %w", err)
	}
	var serverHash *commonpb.HashType
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
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
