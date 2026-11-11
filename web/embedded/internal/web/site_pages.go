// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"bytes"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/superdurable/dex/web/embedded/internal/web/api"
)

// basePathMetaName names the index.html meta tag that tells the SPA its base path.
const basePathMetaName = "dex-web-base-path"

// sitePages serves the embedded SPA; it is immutable after construction, so many targets may share it.
type sitePages struct {
	assets fs.FS
	files  http.Handler
	// indexTemplate is the built index.html, whose asset URLs are relative ("./assets/...").
	indexTemplate string
	// framePolicy is applied at the handler entry, so every response carries it, not only pages.
	framePolicy framePolicy
}

// newSitePages opens the "dist" directory of a Web asset file system.
func newSitePages(assets fs.FS, frameAncestors []string) (*sitePages, error) {
	if assets == nil {
		panic("Web assets must not be nil")
	}
	framePolicy, err := newFramePolicy(frameAncestors)
	if err != nil {
		return nil, err
	}
	assetRoot, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(fmt.Sprintf("open embedded Web assets: %v", err))
	}
	index, err := fs.ReadFile(assetRoot, "index.html")
	if err != nil {
		panic(fmt.Sprintf("read embedded Web index: %v", err))
	}
	if !strings.Contains(string(index), "</head>") {
		panic("embedded Web index has no </head>")
	}
	return &sitePages{
		assets: assetRoot, files: http.FileServer(http.FS(assetRoot)),
		indexTemplate: string(index), framePolicy: framePolicy,
	}, nil
}

// spaHandler serves assets, and index.html for every other page path so the SPA router can resolve it.
func (pages *sitePages) spaHandler(basePath string, hasConnectorSetup bool) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !hasConnectorSetup && strings.HasPrefix(request.URL.Path, "/api/v2/connector-") {
			api.WriteCodedError(response, http.StatusNotFound, "CONNECTOR_SETUP_UNAVAILABLE", "Connector configuration is not available in this Dex Web")
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		requestPath := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
		if requestPath == "." || requestPath == "" {
			pages.serveIndex(response, request, basePath)
			return
		}
		file, err := pages.assets.Open(requestPath)
		if err == nil {
			if closeErr := file.Close(); closeErr != nil {
				http.Error(response, closeErr.Error(), http.StatusInternalServerError)
				return
			}
			pages.files.ServeHTTP(response, request)
			return
		}
		if strings.HasPrefix(request.URL.Path, "/api/") {
			api.WriteError(response, http.StatusNotFound, "API route not found", nil)
			return
		}
		pages.serveIndex(response, request, basePath)
	})
}

func (pages *sitePages) serveIndex(response http.ResponseWriter, request *http.Request, basePath string) {
	response.Header().Set("Cache-Control", "no-store")
	index := renderIndexForBasePath(pages.indexTemplate, basePath)
	http.ServeContent(response, request, "index.html", time.Time{}, bytes.NewReader(index))
}

// renderIndexForBasePath makes the relative asset URLs absolute under basePath and records basePath for the SPA.
func renderIndexForBasePath(indexTemplate string, basePath string) []byte {
	prefix := html.EscapeString(strings.TrimSuffix(basePath, "/"))
	index := strings.NewReplacer(`src="./`, `src="`+prefix+`/`, `href="./`, `href="`+prefix+`/`).Replace(indexTemplate)
	meta := `<meta name="` + basePathMetaName + `" content="` + html.EscapeString(basePath) + `" />`
	return []byte(strings.Replace(index, "</head>", meta+"\n  </head>", 1))
}
