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
	// SHA256 is the verified archive digest from the release index.
	SHA256 string `json:"sha256"`
	// BinarySHA256 is the digest of the binary extracted from that archive; it is checked before every start.
	BinarySHA256 string `json:"binarySha256"`
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
	return &connectorsMCPReleaseResolver{
		cfg: cfg, httpClient: newHTTPSOnlyClient(httpClient), platform: platform, logs: logs,
	}
}

// newHTTPSOnlyClient copies httpClient and refuses to follow a redirect to a non-https URL.
func newHTTPSOnlyClient(httpClient *http.Client) *http.Client {
	httpsOnlyClient := *httpClient
	httpsOnlyClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" {
			return fmt.Errorf("refusing redirect to non-https URL %s", request.URL.Redacted())
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &httpsOnlyClient
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
		if installed, err := r.cachedRelease(r.cfg.Version, nil); err == nil {
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
	if installed, err := r.cachedRelease(release.Version, &release); err == nil {
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
	if response.Request == nil || response.Request.URL.Scheme != "https" {
		return nil, errors.Join(fmt.Errorf("%s was not answered over https", rawURL), response.Body.Close())
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
	binarySHA256, err := extractConnectorsMCPBinary(archivePath, filepath.Join(releaseDirectory, connectorsMCPBinaryName))
	if err != nil {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("extract connectors MCP release %s: %w", release.Version, err)
	}
	installed, err := json.Marshal(connectorsMCPInstalledRelease{
		Version: release.Version, LaunchContract: release.LaunchContract, SHA256: actualSHA256, BinarySHA256: binarySHA256,
	})
	if err != nil {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("encode connectors MCP release record: %w", err)
	}
	if err := os.WriteFile(filepath.Join(releaseDirectory, connectorsMCPInstalledReleaseFileName), installed, 0o600); err != nil {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("write connectors MCP release record: %w", err)
	}
	if err := os.Rename(releaseDirectory, filepath.Join(r.cfg.ToolsDirectory, release.Version)); err != nil {
		// A concurrent dexcli may have installed the same release first.
		if existing, loadErr := r.verifyInstalledRelease(release.Version, &release); loadErr == nil {
			return existing, nil
		}
		return connectorsMCPResolvedBinary{}, fmt.Errorf("install connectors MCP release %s: %w", release.Version, err)
	}
	return r.verifyInstalledRelease(release.Version, &release)
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
// 0755 and returns its lowercase hex SHA-256. Every other entry is ignored, so archive paths never reach the file
// system.
func extractConnectorsMCPBinary(archivePath string, destination string) (binarySHA256 string, extractErr error) {
	archive, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer func() { extractErr = errors.Join(extractErr, archive.Close()) }()
	decompressed, err := gzip.NewReader(archive)
	if err != nil {
		return "", fmt.Errorf("open gzip stream: %w", err)
	}
	defer func() { extractErr = errors.Join(extractErr, decompressed.Close()) }()
	entries := tar.NewReader(decompressed)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("archive has no %s file", connectorsMCPBinaryName)
		}
		if err != nil {
			return "", fmt.Errorf("read archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(filepath.Clean(header.Name)) != connectorsMCPBinaryName {
			continue
		}
		if header.Size > connectorsMCPMaxBinaryBytes {
			return "", fmt.Errorf("%s in the archive exceeds %d bytes", connectorsMCPBinaryName, connectorsMCPMaxBinaryBytes)
		}
		binary, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err != nil {
			return "", err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(io.MultiWriter(binary, hash), io.LimitReader(entries, connectorsMCPMaxBinaryBytes))
		if err := errors.Join(copyErr, binary.Close()); err != nil {
			return "", err
		}
		return hex.EncodeToString(hash.Sum(nil)), nil
	}
}

// errConnectorsMCPReleaseNotCached reports that a release version has no usable cache entry.
var errConnectorsMCPReleaseNotCached = errors.New("connectors MCP release is not cached")

// cachedRelease returns the verified cached release version, or an error wrapping errConnectorsMCPReleaseNotCached.
// It removes a cache entry that fails verification, so the release is downloaded again.
func (r *connectorsMCPReleaseResolver) cachedRelease(
	version string,
	expected *connectorsMCPRelease,
) (connectorsMCPResolvedBinary, error) {
	installed, err := r.verifyInstalledRelease(version, expected)
	if err == nil {
		return installed, nil
	}
	if errors.Is(err, errConnectorsMCPReleaseNotCached) {
		return connectorsMCPResolvedBinary{}, err
	}
	fmt.Fprintf(r.logs, "Connectors MCP: discarding cached release %s: %v\n", version, err)
	if removeErr := os.RemoveAll(filepath.Join(r.cfg.ToolsDirectory, version)); removeErr != nil {
		return connectorsMCPResolvedBinary{}, errors.Join(err, fmt.Errorf("remove cached release %s: %w", version, removeErr))
	}
	return connectorsMCPResolvedBinary{}, fmt.Errorf("%w: %v", errConnectorsMCPReleaseNotCached, err)
}

// verifyInstalledRelease returns the cached release version after checking it: no symlink below the Dex state
// directory, a supported launch contract matching expected when given, and a binary whose SHA-256 matches the digest
// recorded from the verified archive. It wraps errConnectorsMCPReleaseNotCached when the version directory is absent.
//
// The check detects a corrupt or swapped cache entry. It does not defend against another process of the same user:
// one that can write the Dex state directory can also replace dexcli itself.
func (r *connectorsMCPReleaseResolver) verifyInstalledRelease(
	version string,
	expected *connectorsMCPRelease,
) (connectorsMCPResolvedBinary, error) {
	if !connectorsMCPReleaseVersionPattern.MatchString(version) {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("invalid connectors MCP release version %q", version)
	}
	releaseDirectory := filepath.Join(r.cfg.ToolsDirectory, version)
	for _, directory := range []string{filepath.Dir(r.cfg.ToolsDirectory), r.cfg.ToolsDirectory, releaseDirectory} {
		info, err := os.Lstat(directory)
		if errors.Is(err, os.ErrNotExist) {
			return connectorsMCPResolvedBinary{}, fmt.Errorf("%w: %s does not exist", errConnectorsMCPReleaseNotCached, directory)
		}
		if err != nil {
			return connectorsMCPResolvedBinary{}, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return connectorsMCPResolvedBinary{}, fmt.Errorf("%s is not a plain directory", directory)
		}
	}
	recordPath := filepath.Join(releaseDirectory, connectorsMCPInstalledReleaseFileName)
	if err := requirePlainFile(recordPath); err != nil {
		return connectorsMCPResolvedBinary{}, err
	}
	content, err := os.ReadFile(recordPath)
	if err != nil {
		return connectorsMCPResolvedBinary{}, err
	}
	var installed connectorsMCPInstalledRelease
	if err := json.Unmarshal(content, &installed); err != nil {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("decode %s: %w", recordPath, err)
	}
	if installed.Version != version || !isSupportedConnectorsMCPLaunchContract(installed.LaunchContract) {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("%s names version %q with launch contract %d",
			recordPath, installed.Version, installed.LaunchContract)
	}
	if expected != nil {
		asset := expected.Assets[r.platform]
		if installed.LaunchContract != expected.LaunchContract || installed.SHA256 != strings.ToLower(asset.SHA256) {
			return connectorsMCPResolvedBinary{}, fmt.Errorf("%s does not match release %s in the release index", recordPath, version)
		}
	}
	binaryPath := filepath.Join(releaseDirectory, connectorsMCPBinaryName)
	if err := requirePlainFile(binaryPath); err != nil {
		return connectorsMCPResolvedBinary{}, err
	}
	actualSHA256, err := fileSHA256(binaryPath)
	if err != nil {
		return connectorsMCPResolvedBinary{}, err
	}
	if len(installed.BinarySHA256) != 2*sha256.Size || actualSHA256 != installed.BinarySHA256 {
		return connectorsMCPResolvedBinary{}, fmt.Errorf("%s sha256 is %s, but %s recorded %q",
			binaryPath, actualSHA256, recordPath, installed.BinarySHA256)
	}
	return connectorsMCPResolvedBinary{path: binaryPath, version: version}, nil
}

// requirePlainFile returns an error unless path is a regular file and not a symlink.
func requirePlainFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

// fileSHA256 returns the lowercase hex SHA-256 of the file at path.
func fileSHA256(path string) (digest string, hashErr error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { hashErr = errors.Join(hashErr, file.Close()) }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// newestInstalledRelease returns the highest verified cached release, honoring Version when it is set, or an error
// when none is cached. Cache entries that fail verification are removed.
func (r *connectorsMCPReleaseResolver) newestInstalledRelease() (connectorsMCPResolvedBinary, error) {
	if r.cfg.Version != "" {
		return r.cachedRelease(r.cfg.Version, nil)
	}
	entries, err := os.ReadDir(r.cfg.ToolsDirectory)
	if err != nil {
		return connectorsMCPResolvedBinary{}, err
	}
	var newest connectorsMCPResolvedBinary
	for _, entry := range entries {
		if !connectorsMCPReleaseVersionPattern.MatchString(entry.Name()) {
			continue
		}
		installed, err := r.cachedRelease(entry.Name(), nil)
		if err != nil {
			continue
		}
		if newest.version == "" || compareConnectorsMCPVersions(installed.version, newest.version) > 0 {
			newest = installed
		}
	}
	if newest.version == "" {
		return connectorsMCPResolvedBinary{}, errConnectorsMCPReleaseNotCached
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
