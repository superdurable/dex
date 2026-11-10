// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dev

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	connectorsMCPReleaseIndexSchemaVersion = "dex-connectors-mcp/release-index/v1"
	connectorsMCPCachedIndexFileName       = "index.json"
	connectorsMCPInstalledReleaseFileName  = "release.json"
	connectorsMCPIndexRequestTimeout       = 15 * time.Second
	connectorsMCPDownloadRequestTimeout    = 5 * time.Minute
	connectorsMCPMaxIndexBytes             = 4 << 20
	connectorsMCPMaxArchiveBytes           = 256 << 20
	connectorsMCPMaxBinaryBytes            = 256 << 20
)

// supportedConnectorsMCPLaunchContracts lists the server launch contracts this dexcli can start and talk to.
var supportedConnectorsMCPLaunchContracts = []int{1}

func isSupportedConnectorsMCPLaunchContract(launchContract int) bool {
	return slices.Contains(supportedConnectorsMCPLaunchContracts, launchContract)
}

// connectorsMCPReleaseIndex is the connectors-mcp/index.json document of the Connectors library Pages site.
type connectorsMCPReleaseIndex struct {
	SchemaVersion string                 `json:"schemaVersion"`
	Releases      []connectorsMCPRelease `json:"releases"`
}

type connectorsMCPRelease struct {
	Version        string                               `json:"version"`
	LaunchContract int                                  `json:"launchContract"`
	Tag            string                               `json:"tag"`
	Assets         map[string]connectorsMCPReleaseAsset `json:"assets"`
}

type connectorsMCPReleaseAsset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// connectorsMCPInstalledRelease is the release.json that dexcli writes beside each cached binary.
type connectorsMCPInstalledRelease struct {
	Version        string `json:"version"`
	LaunchContract int    `json:"launchContract"`
	SHA256         string `json:"sha256"`
}

// connectorsMCPResolvedBinary names the executable to start and the release it came from.
type connectorsMCPResolvedBinary struct {
	path string
	// version is empty for a --connectors-mcp-binary executable.
	version string
}

// connectorsMCPReleaseResolver finds a compatible released dex-connectors-mcp binary, downloading and verifying it
// into the release cache when needed.
type connectorsMCPReleaseResolver struct {
	cfg        *ConnectorsMCPConfig
	httpClient *http.Client
	// platform is the release index asset key, such as darwin-arm64.
	platform string
	logs     io.Writer
}

func newConnectorsMCPReleaseResolver(
	cfg *ConnectorsMCPConfig,
	httpClient *http.Client,
	platform string,
	logs io.Writer,
) *connectorsMCPReleaseResolver {
	if cfg == nil {
		panic("connectors MCP config must not be nil")
	}
	if httpClient == nil {
		panic("connectors MCP HTTP client must not be nil")
	}
	if platform == "" {
		panic("connectors MCP platform must not be empty")
	}
	if logs == nil {
		panic("connectors MCP logs writer must not be nil")
	}
	return &connectorsMCPReleaseResolver{cfg: cfg, httpClient: httpClient, platform: platform, logs: logs}
}

// resolveBinary returns the executable to start: --connectors-mcp-binary when set, otherwise a verified release
// from the cache or the release index. The error explains why no binary is available.
func (r *connectorsMCPReleaseResolver) resolveBinary(ctx context.Context) (connectorsMCPResolvedBinary, error) {
	if r.cfg.BinaryPath != "" {
		binaryPath, err := exec.LookPath(r.cfg.BinaryPath)
		if err != nil {
			return connectorsMCPResolvedBinary{}, fmt.Errorf("connectors MCP binary %q is not executable: %w", r.cfg.BinaryPath, err)
		}
		return connectorsMCPResolvedBinary{path: binaryPath}, nil
	}
	if err := os.MkdirAll(r.cfg.ToolsDirectory, 0o700); err != nil {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("create connectors MCP release cache: %w", err)
	}
	if r.cfg.Version != "" {
		if installed, err := r.loadInstalledRelease(r.cfg.Version); err == nil {
			return installed, nil
		}
	}
	index, indexErr := r.loadReleaseIndex(ctx)
	if indexErr != nil {
		return r.resolveCachedFallback(indexErr)
	}
	release, err := selectConnectorsMCPRelease(index, r.platform, r.cfg.Version)
	if err != nil {
		return connectorsMCPResolvedBinary{}, err
	}
	if installed, err := r.loadInstalledRelease(release.Version); err == nil {
		return installed, nil
	}
	installed, installErr := r.installRelease(ctx, release)
	if installErr != nil {
		return r.resolveCachedFallback(installErr)
	}
	return installed, nil
}

// resolveCachedFallback returns the newest cached compatible release when the index or download failed, or an error
// joining cause with the empty cache.
func (r *connectorsMCPReleaseResolver) resolveCachedFallback(cause error) (connectorsMCPResolvedBinary, error) {
	cached, err := r.newestInstalledRelease()
	if err != nil {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("%w; no compatible release is cached in %s", cause, r.cfg.ToolsDirectory)
	}
	fmt.Fprintf(r.logs, "Connectors MCP: %v; using cached release %s\n", cause, cached.version)
	return cached, nil
}

// loadReleaseIndex returns the cached index while it is younger than IndexCheckInterval, otherwise fetches, checks,
// and caches a new one.
func (r *connectorsMCPReleaseResolver) loadReleaseIndex(ctx context.Context) (*connectorsMCPReleaseIndex, error) {
	cachedPath := filepath.Join(r.cfg.ToolsDirectory, connectorsMCPCachedIndexFileName)
	if info, err := os.Stat(cachedPath); err == nil && time.Since(info.ModTime()) < r.cfg.IndexCheckInterval {
		content, readErr := os.ReadFile(cachedPath)
		if readErr == nil {
			if index, parseErr := parseConnectorsMCPReleaseIndex(content); parseErr == nil &&
				(r.cfg.Version == "" || index.hasVersion(r.cfg.Version)) {
				return index, nil
			}
		}
		// An unreadable, invalid, or outdated cached index is replaced by a fresh fetch.
	}
	content, err := r.fetchReleaseIndex(ctx)
	if err != nil {
		return nil, err
	}
	index, err := parseConnectorsMCPReleaseIndex(content)
	if err != nil {
		return nil, fmt.Errorf("read connectors MCP release index %s: %w", r.cfg.IndexURL, err)
	}
	if err := writeFileAtomically(cachedPath, content, 0o600); err != nil {
		return nil, fmt.Errorf("cache connectors MCP release index: %w", err)
	}
	return index, nil
}

func (r *connectorsMCPReleaseResolver) fetchReleaseIndex(ctx context.Context) ([]byte, error) {
	requestCtx, cancel := context.WithTimeout(ctx, connectorsMCPIndexRequestTimeout)
	defer cancel()
	response, err := r.getHTTPS(requestCtx, r.cfg.IndexURL)
	if err != nil {
		return nil, fmt.Errorf("fetch connectors MCP release index: %w", err)
	}
	content, readErr := io.ReadAll(io.LimitReader(response.Body, connectorsMCPMaxIndexBytes+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return nil, fmt.Errorf("read connectors MCP release index %s: %w", r.cfg.IndexURL, errors.Join(readErr, closeErr))
	}
	if len(content) > connectorsMCPMaxIndexBytes {
		return nil, fmt.Errorf("connectors MCP release index %s exceeds %d bytes", r.cfg.IndexURL, connectorsMCPMaxIndexBytes)
	}
	return content, nil
}

// getHTTPS returns a 200 response for an https URL, or an error for another scheme, a transport failure, or another
// status. The caller closes the body.
func (r *connectorsMCPReleaseResolver) getHTTPS(ctx context.Context, rawURL string) (*http.Response, error) {
	if !strings.HasPrefix(rawURL, "https://") {
		return nil, fmt.Errorf("%q is not an https URL", rawURL)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", rawURL, err)
	}
	response, err := r.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		closeErr := response.Body.Close()
		return nil, errors.Join(fmt.Errorf("%s returned HTTP %d", rawURL, response.StatusCode), closeErr)
	}
	return response, nil
}

// parseConnectorsMCPReleaseIndex decodes an index and rejects an unknown schemaVersion.
func parseConnectorsMCPReleaseIndex(content []byte) (*connectorsMCPReleaseIndex, error) {
	var index connectorsMCPReleaseIndex
	if err := json.Unmarshal(content, &index); err != nil {
		return nil, fmt.Errorf("decode release index: %w", err)
	}
	if index.SchemaVersion != connectorsMCPReleaseIndexSchemaVersion {
		return nil, fmt.Errorf("unsupported release index schemaVersion %q", index.SchemaVersion)
	}
	return &index, nil
}

func (index *connectorsMCPReleaseIndex) hasVersion(version string) bool {
	for _, release := range index.Releases {
		if release.Version == version {
			return true
		}
	}
	return false
}

// selectConnectorsMCPRelease returns the requested version, or the highest version with a supported launch contract
// and an asset for platform. The error says why no release qualifies.
func selectConnectorsMCPRelease(
	index *connectorsMCPReleaseIndex,
	platform string,
	version string,
) (connectorsMCPRelease, error) {
	var selected *connectorsMCPRelease
	hasCompatibleRelease := false
	for position := range index.Releases {
		release := &index.Releases[position]
		if !connectorsMCPReleaseVersionPattern.MatchString(release.Version) {
			continue
		}
		if version != "" && release.Version != version {
			continue
		}
		if !isSupportedConnectorsMCPLaunchContract(release.LaunchContract) {
			if version != "" {
				return connectorsMCPRelease{}, fmt.Errorf(
					"connectors MCP release %s uses launch contract %d, which this dexcli does not support; update dexcli",
					version, release.LaunchContract)
			}
			continue
		}
		hasCompatibleRelease = true
		if _, ok := release.Assets[platform]; !ok {
			continue
		}
		if selected == nil || compareConnectorsMCPVersions(release.Version, selected.Version) > 0 {
			selected = release
		}
	}
	switch {
	case selected != nil:
		return *selected, nil
	case version != "" && !hasCompatibleRelease:
		return connectorsMCPRelease{}, fmt.Errorf("connectors MCP release %s is not in the release index", version)
	case hasCompatibleRelease:
		return connectorsMCPRelease{}, fmt.Errorf("no connectors MCP release is published for %s", platform)
	default:
		return connectorsMCPRelease{}, fmt.Errorf("the release index has no connectors MCP release this dexcli supports; update dexcli")
	}
}

// compareConnectorsMCPVersions compares two X.Y.Z versions that match connectorsMCPReleaseVersionPattern and returns
// -1, 0, or 1.
func compareConnectorsMCPVersions(left string, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	for position := range leftParts {
		leftNumber, leftErr := strconv.Atoi(leftParts[position])
		rightNumber, rightErr := strconv.Atoi(rightParts[position])
		if leftErr != nil || rightErr != nil {
			panic(fmt.Sprintf("compare unvalidated connectors MCP versions %q and %q", left, right))
		}
		if leftNumber != rightNumber {
			if leftNumber < rightNumber {
				return -1
			}
			return 1
		}
	}
	return 0
}

// installRelease downloads the platform archive, verifies its SHA-256 against the index, extracts the binary, and
// moves the finished release directory into the cache in one rename.
func (r *connectorsMCPReleaseResolver) installRelease(
	ctx context.Context,
	release connectorsMCPRelease,
) (connectorsMCPResolvedBinary, error) {
	asset := release.Assets[r.platform]
	expectedSHA256 := strings.ToLower(asset.SHA256)
	if decoded, err := hex.DecodeString(expectedSHA256); err != nil || len(decoded) != sha256.Size {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("connectors MCP release %s has an invalid sha256 for %s", release.Version, r.platform)
	}
	fmt.Fprintf(r.logs, "Connectors MCP: downloading release %s for %s\n", release.Version, r.platform)
	stagingDirectory, err := os.MkdirTemp(r.cfg.ToolsDirectory, ".install-")
	if err != nil {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("create connectors MCP staging directory: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(stagingDirectory); removeErr != nil {
			fmt.Fprintf(r.logs, "Connectors MCP: remove staging directory %s: %v\n", stagingDirectory, removeErr)
		}
	}()
	archivePath := filepath.Join(stagingDirectory, "release.tar.gz")
	actualSHA256, err := r.downloadArchive(ctx, asset.URL, archivePath)
	if err != nil {
		return connectorsMCPResolvedBinary{}, err
	}
	if actualSHA256 != expectedSHA256 {
		return connectorsMCPResolvedBinary{}, fmt.Errorf(
			"connectors MCP release %s archive sha256 is %s, but the release index lists %s; refusing to install it",
			release.Version, actualSHA256, expectedSHA256)
	}
	releaseDirectory := filepath.Join(stagingDirectory, release.Version)
	if err := os.Mkdir(releaseDirectory, 0o700); err != nil {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("create connectors MCP release directory: %w", err)
	}
	if err := extractConnectorsMCPBinary(archivePath, filepath.Join(releaseDirectory, connectorsMCPBinaryName)); err != nil {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("extract connectors MCP release %s: %w", release.Version, err)
	}
	installed, err := json.Marshal(connectorsMCPInstalledRelease{
		Version: release.Version, LaunchContract: release.LaunchContract, SHA256: actualSHA256,
	})
	if err != nil {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("encode connectors MCP release record: %w", err)
	}
	if err := os.WriteFile(filepath.Join(releaseDirectory, connectorsMCPInstalledReleaseFileName), installed, 0o600); err != nil {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("write connectors MCP release record: %w", err)
	}
	if err := os.Rename(releaseDirectory, filepath.Join(r.cfg.ToolsDirectory, release.Version)); err != nil {
		// A concurrent dexcli may have installed the same release first.
		if existing, loadErr := r.loadInstalledRelease(release.Version); loadErr == nil {
			return existing, nil
		}
		return connectorsMCPResolvedBinary{}, fmt.Errorf("install connectors MCP release %s: %w", release.Version, err)
	}
	return r.loadInstalledRelease(release.Version)
}

// downloadArchive writes the archive at rawURL to destination and returns its lowercase hex SHA-256.
func (r *connectorsMCPReleaseResolver) downloadArchive(ctx context.Context, rawURL string, destination string) (string, error) {
	requestCtx, cancel := context.WithTimeout(ctx, connectorsMCPDownloadRequestTimeout)
	defer cancel()
	response, err := r.getHTTPS(requestCtx, rawURL)
	if err != nil {
		return "", fmt.Errorf("download connectors MCP release: %w", err)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", errors.Join(fmt.Errorf("create connectors MCP archive: %w", err), response.Body.Close())
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, connectorsMCPMaxArchiveBytes+1))
	closeErr := errors.Join(response.Body.Close(), file.Close())
	if copyErr != nil || closeErr != nil {
		return "", fmt.Errorf("download connectors MCP release %s: %w", rawURL, errors.Join(copyErr, closeErr))
	}
	if written > connectorsMCPMaxArchiveBytes {
		return "", fmt.Errorf("connectors MCP release archive %s exceeds %d bytes", rawURL, connectorsMCPMaxArchiveBytes)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// extractConnectorsMCPBinary copies the archive's regular file named dex-connectors-mcp to destination with mode
// 0755. Every other entry is ignored, so archive paths never reach the file system.
func extractConnectorsMCPBinary(archivePath string, destination string) (extractErr error) {
	archive, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { extractErr = errors.Join(extractErr, archive.Close()) }()
	decompressed, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer func() { extractErr = errors.Join(extractErr, decompressed.Close()) }()
	entries := tar.NewReader(decompressed)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("archive has no %s file", connectorsMCPBinaryName)
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(filepath.Clean(header.Name)) != connectorsMCPBinaryName {
			continue
		}
		if header.Size > connectorsMCPMaxBinaryBytes {
			return fmt.Errorf("%s in the archive exceeds %d bytes", connectorsMCPBinaryName, connectorsMCPMaxBinaryBytes)
		}
		binary, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(binary, io.LimitReader(entries, connectorsMCPMaxBinaryBytes))
		return errors.Join(copyErr, binary.Close())
	}
}

// loadInstalledRelease returns the cached release version, or an error when it is missing, incomplete, or uses an
// unsupported launch contract.
func (r *connectorsMCPReleaseResolver) loadInstalledRelease(version string) (connectorsMCPResolvedBinary, error) {
	if !connectorsMCPReleaseVersionPattern.MatchString(version) {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("invalid connectors MCP release version %q", version)
	}
	releaseDirectory := filepath.Join(r.cfg.ToolsDirectory, version)
	content, err := os.ReadFile(filepath.Join(releaseDirectory, connectorsMCPInstalledReleaseFileName))
	if err != nil {
		return connectorsMCPResolvedBinary{}, err
	}
	var installed connectorsMCPInstalledRelease
	if err := json.Unmarshal(content, &installed); err != nil {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("decode %s: %w", releaseDirectory, err)
	}
	if installed.Version != version || !isSupportedConnectorsMCPLaunchContract(installed.LaunchContract) {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("cached connectors MCP release %s is not usable", version)
	}
	binaryPath := filepath.Join(releaseDirectory, connectorsMCPBinaryName)
	info, err := os.Stat(binaryPath)
	if err != nil {
		return connectorsMCPResolvedBinary{}, err
	}
	if !info.Mode().IsRegular() {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("%s is not a regular file", binaryPath)
	}
	return connectorsMCPResolvedBinary{path: binaryPath, version: version}, nil
}

// newestInstalledRelease returns the highest cached release with a supported launch contract, honoring Version when
// it is set, or an error when none is cached.
func (r *connectorsMCPReleaseResolver) newestInstalledRelease() (connectorsMCPResolvedBinary, error) {
	if r.cfg.Version != "" {
		return r.loadInstalledRelease(r.cfg.Version)
	}
	entries, err := os.ReadDir(r.cfg.ToolsDirectory)
	if err != nil {
		return connectorsMCPResolvedBinary{}, err
	}
	var newest connectorsMCPResolvedBinary
	for _, entry := range entries {
		if !entry.IsDir() || !connectorsMCPReleaseVersionPattern.MatchString(entry.Name()) {
			continue
		}
		installed, err := r.loadInstalledRelease(entry.Name())
		if err != nil {
			continue
		}
		if newest.version == "" || compareConnectorsMCPVersions(installed.version, newest.version) > 0 {
			newest = installed
		}
	}
	if newest.version == "" {
		return connectorsMCPResolvedBinary{}, errors.New("no cached release")
	}
	return newest, nil
}

// writeFileAtomically replaces path with content through a temporary file in the same directory.
func writeFileAtomically(path string, content []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	_, writeErr := temporary.Write(content)
	closeErr := temporary.Close()
	if writeErr != nil || closeErr != nil {
		return errors.Join(writeErr, closeErr, os.Remove(temporaryPath))
	}
	if err := os.Chmod(temporaryPath, mode); err != nil {
		return errors.Join(err, os.Remove(temporaryPath))
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return errors.Join(err, os.Remove(temporaryPath))
	}
	return nil
}
