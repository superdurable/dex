// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/superdurable/dex/web/embedded/internal/web/api"
)

const (
	connectorPublicCatalogURL        = "https://connectors.superdurable.io/catalog.json"
	connectorPublicCatalogAPIVersion = "connectors.dex.dev/catalog/v1alpha1"
	connectorPublicCatalogKind       = "ConnectorCatalog"
	connectorPublicCatalogLimit      = 8 << 20
	connectorPublicCatalogTTL        = 10 * time.Minute
	connectorPublicCatalogRetryDelay = 30 * time.Second
	connectorPublicCatalogTimeout    = 15 * time.Second
	officialConnectorModulePrefix    = "github.com/superdurable/dex-connectors-library/"
)

var connectorPublicCatalogDirectoryPattern = regexp.MustCompile(`^connectors/[a-z0-9][a-z0-9-]*(?:/[a-z0-9][a-z0-9-]*)*$`)

// connectorPublicCatalog lists the official Connector releases a developer can add a connection for.
type connectorPublicCatalog struct {
	url        string
	httpClient *http.Client
	mu         sync.Mutex
	entries    []connectorCatalogEntry
	fetchedAt  time.Time
	// failure is the last fetch error, cleared by a successful fetch; failedAt is when it happened.
	failure  error
	failedAt time.Time
	inFlight *connectorPublicCatalogFetch
}

// connectorPublicCatalogFetch is one catalog download that every concurrent caller waits for.
type connectorPublicCatalogFetch struct {
	done chan struct{}
	// waiterCount is how many callers wait for this fetch; catalog.mu guards it.
	waiterCount int
	entries     []connectorCatalogEntry
	err         error
}

// connectorCatalogEntry is one Connector release that a new standalone connection can use.
type connectorCatalogEntry struct {
	ConnectorID   string `json:"connectorId"`
	DisplayName   string `json:"displayName"`
	Description   string `json:"description,omitempty"`
	ModulePath    string `json:"modulePath"`
	ModuleVersion string `json:"moduleVersion"`
	LocalOverride bool   `json:"localOverride,omitempty"`
}

type connectorCatalogListResponse struct {
	Connectors               []connectorCatalogEntry `json:"connectors"`
	IsPublicCatalogAvailable bool                    `json:"publicCatalogAvailable"`
}

type connectorPublicCatalogDocument struct {
	APIVersion string                        `json:"apiVersion"`
	Kind       string                        `json:"kind"`
	Connectors []connectorPublicCatalogEntry `json:"connectors"`
}

type connectorPublicCatalogEntry struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Version     string            `json:"version"`
	Directory   string            `json:"directory"`
	Languages   map[string]string `json:"languages"`
}

func newConnectorPublicCatalog() *connectorPublicCatalog {
	catalogURL, err := url.Parse(connectorPublicCatalogURL)
	if err != nil {
		panic("Connector public catalog URL is invalid")
	}
	return &connectorPublicCatalog{
		url: connectorPublicCatalogURL,
		httpClient: &http.Client{
			Timeout: connectorPublicCatalogTimeout,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) >= 5 || !strings.EqualFold(request.URL.Hostname(), catalogURL.Hostname()) {
					return fmt.Errorf("Connector catalog redirect is not allowed")
				}
				return nil
			},
		},
	}
}

func (setup *connectorSetup) handleListConnectorCatalog(response http.ResponseWriter, request *http.Request) {
	entries, err := setup.publicCatalog.list(request.Context())
	isPublicCatalogAvailable := err == nil
	merged := mergeConnectorCatalogOverrides(entries, setup.releases.localReleases)
	if !isPublicCatalogAvailable && len(merged) == 0 {
		api.WriteCodedError(response, http.StatusBadGateway, "CONNECTOR_CATALOG_UNAVAILABLE", "Connector catalog is unavailable")
		return
	}
	writeWebJSON(response, http.StatusOK, connectorCatalogListResponse{
		Connectors: merged, IsPublicCatalogAvailable: isPublicCatalogAvailable,
	})
}

// list returns the cached public catalog, refetching it after the TTL. Concurrent callers share one fetch. After a
// failed fetch, it returns the cached copy, or else the error, without fetching for connectorPublicCatalogRetryDelay.
func (catalog *connectorPublicCatalog) list(ctx context.Context) ([]connectorCatalogEntry, error) {
	catalog.mu.Lock()
	now := time.Now()
	if catalog.entries != nil && now.Sub(catalog.fetchedAt) < connectorPublicCatalogTTL {
		entries := catalog.entries
		catalog.mu.Unlock()
		return entries, nil
	}
	if catalog.failure != nil && now.Sub(catalog.failedAt) < connectorPublicCatalogRetryDelay {
		entries, failure := catalog.entries, catalog.failure
		catalog.mu.Unlock()
		if entries != nil {
			return entries, nil
		}
		return nil, failure
	}
	fetch := catalog.inFlight
	if fetch == nil {
		fetch = &connectorPublicCatalogFetch{done: make(chan struct{})}
		catalog.inFlight = fetch
		// The shared fetch outlives the request that started it; the HTTP client timeout bounds it.
		go catalog.runFetch(context.WithoutCancel(ctx), fetch)
	}
	fetch.waiterCount++
	catalog.mu.Unlock()
	select {
	case <-fetch.done:
		return fetch.entries, fetch.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (catalog *connectorPublicCatalog) runFetch(ctx context.Context, fetch *connectorPublicCatalogFetch) {
	entries, err := catalog.fetch(ctx)
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	defer close(fetch.done)
	catalog.inFlight = nil
	if err == nil {
		catalog.entries, catalog.fetchedAt, catalog.failure = entries, time.Now(), nil
		fetch.entries = entries
		return
	}
	catalog.failure, catalog.failedAt = err, time.Now()
	if catalog.entries != nil {
		fetch.entries = catalog.entries
		return
	}
	fetch.err = err
}

func (catalog *connectorPublicCatalog) fetch(ctx context.Context) ([]connectorCatalogEntry, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, catalog.url, nil)
	if err != nil {
		return nil, fmt.Errorf("create Connector catalog request: %w", err)
	}
	response, err := catalog.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download Connector catalog: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download Connector catalog: HTTP %d", response.StatusCode)
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, connectorPublicCatalogLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read Connector catalog: %w", err)
	}
	if len(contents) > connectorPublicCatalogLimit {
		return nil, fmt.Errorf("Connector catalog exceeds size limit")
	}
	return decodeConnectorPublicCatalog(contents)
}

// decodeConnectorPublicCatalog returns every entry with a released Go module; it skips malformed entries.
func decodeConnectorPublicCatalog(contents []byte) ([]connectorCatalogEntry, error) {
	var document connectorPublicCatalogDocument
	if err := json.Unmarshal(contents, &document); err != nil {
		return nil, fmt.Errorf("decode Connector catalog: %w", err)
	}
	if document.APIVersion != connectorPublicCatalogAPIVersion || document.Kind != connectorPublicCatalogKind {
		return nil, fmt.Errorf("unsupported Connector catalog %s %s", document.APIVersion, document.Kind)
	}
	entries := make([]connectorCatalogEntry, 0, len(document.Connectors))
	for _, candidate := range document.Connectors {
		// Every Connector release, whatever its package language, is published under its Go module tag.
		moduleVersion := candidate.Languages["go"]
		modulePath := officialConnectorModulePrefix + candidate.Directory + "/go"
		if !connectorReleaseIDPattern.MatchString(candidate.ID) ||
			!connectorPublicCatalogDirectoryPattern.MatchString(candidate.Directory) ||
			!officialConnectorModulePattern.MatchString(modulePath) ||
			!exactConnectorReleaseVersionPattern.MatchString(moduleVersion) {
			continue
		}
		displayName := strings.TrimSpace(candidate.Name)
		if displayName == "" {
			displayName = candidate.ID
		}
		entries = append(entries, connectorCatalogEntry{
			ConnectorID: candidate.ID, DisplayName: displayName, Description: strings.TrimSpace(candidate.Description),
			ModulePath: modulePath, ModuleVersion: moduleVersion,
		})
	}
	return entries, nil
}

// mergeConnectorCatalogOverrides replaces public entries with local release overrides and sorts by display name.
func mergeConnectorCatalogOverrides(
	entries []connectorCatalogEntry,
	overrides map[string]resolvedConnectorRelease,
) []connectorCatalogEntry {
	merged := make([]connectorCatalogEntry, 0, len(entries)+len(overrides))
	for _, entry := range entries {
		if _, isOverridden := overrides[entry.ConnectorID]; !isOverridden {
			merged = append(merged, entry)
		}
	}
	for connectorID, resolved := range overrides {
		displayName := strings.TrimSpace(resolved.release.Manifest.Metadata.DisplayName)
		if displayName == "" {
			displayName = connectorID
		}
		merged = append(merged, connectorCatalogEntry{
			ConnectorID: connectorID, DisplayName: displayName,
			Description: strings.TrimSpace(resolved.release.Manifest.Metadata.Description),
			ModulePath:  resolved.release.ModulePath, ModuleVersion: resolved.release.Version, LocalOverride: true,
		})
	}
	sort.Slice(merged, func(left int, right int) bool {
		leftName, rightName := strings.ToLower(merged[left].DisplayName), strings.ToLower(merged[right].DisplayName)
		if leftName == rightName {
			return merged[left].ConnectorID < merged[right].ConnectorID
		}
		return leftName < rightName
	})
	return merged
}
