// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsprpc

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/jsonrpc2"
)

// revisionDefinitionMethod asks the primary server to resolve a definition in
// the given text at the file's real path, without opening or changing the
// workspace document.
const revisionDefinitionMethod = "openidea/revisionDefinition"

type revisionDefinitionParams struct {
	TextDocument protocol.TextDocumentIdentifier `json:"textDocument"`
	Position     protocol.Position               `json:"position"`
	Text         string                          `json:"text"`
}

// revisionHandler answers OpenIDEA's revision requests on the primary server.
// The request text is analysed in a snapshot of its own, so the workspace's
// document and its overlays are untouched.
func revisionHandler(session *cache.Session, next jsonrpc2.Handler) jsonrpc2.Handler {
	cache := newRevisionCache()
	return func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		if req.Method() != revisionDefinitionMethod {
			return next(ctx, reply, req)
		}
		var params revisionDefinitionParams
		if err := json.Unmarshal(req.Params(), &params); err != nil {
			return reply(ctx, nil, err)
		}
		locations, err := revisionDefinition(ctx, session, cache, params)
		return reply(ctx, locations, err)
	}
}

func revisionDefinition(ctx context.Context, session *cache.Session, cache *revisionCache, params revisionDefinitionParams) ([]protocol.Location, error) {
	uri := params.TextDocument.URI
	snapshot, release, err := session.SnapshotOf(ctx, uri)
	if err != nil {
		return nil, err
	}
	defer release()

	derived, err := cache.snapshotFor(ctx, snapshot, uri, []byte(params.Text))
	if err != nil {
		return nil, err
	}

	fh, err := derived.ReadFile(ctx, uri)
	if err != nil {
		return nil, err
	}
	// A zero-width range at the cursor: Definition accepts a range because it
	// also handles selections.
	return golang.Definition(ctx, derived, fh, protocol.Range{Start: params.Position, End: params.Position})
}

// A revisionCache keeps the HEAD variant of a file alive between requests.
// The workspace and the revision differ in one file, so rebuilding the
// derived snapshot for every hover or click is wasteful: the package is
// type-checked once and then read from the cached snapshot.
type revisionCache struct {
	mu      sync.Mutex
	entries map[revisionKey]*revisionEntry
}

type revisionKey struct {
	uri  protocol.DocumentURI
	hash file.Hash
	// The parent's sequence id invalidates the entry when the workspace
	// snapshot changes (an edit, a dependency change), without invalidating it
	// on unrelated requests.
	sequenceID uint64
}

type revisionEntry struct {
	snapshot *cache.Snapshot
	release  func()
	used     time.Time
}

const (
	revisionCacheEntries = 4
	revisionCacheTTL     = 2 * time.Minute
)

func newRevisionCache() *revisionCache {
	return &revisionCache{entries: make(map[revisionKey]*revisionEntry)}
}

func (c *revisionCache) snapshotFor(ctx context.Context, parent *cache.Snapshot, uri protocol.DocumentURI, text []byte) (*cache.Snapshot, error) {
	key := revisionKey{uri: uri, hash: file.HashOf(text), sequenceID: parent.SequenceID()}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for k, entry := range c.entries {
		if now.Sub(entry.used) > revisionCacheTTL {
			entry.release()
			delete(c.entries, k)
		}
	}
	if entry, ok := c.entries[key]; ok {
		entry.used = now
		return entry.snapshot, nil
	}

	for len(c.entries) >= revisionCacheEntries {
		var oldest revisionKey
		var found bool
		for k, entry := range c.entries {
			if !found || entry.used.Before(c.entries[oldest].used) {
				oldest, found = k, true
			}
		}
		if !found {
			break
		}
		c.entries[oldest].release()
		delete(c.entries, oldest)
	}

	derived, release := parent.SnapshotWithText(ctx, uri, text)
	c.entries[key] = &revisionEntry{snapshot: derived, release: release, used: now}
	return derived, nil
}
