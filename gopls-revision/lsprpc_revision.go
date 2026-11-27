// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsprpc

import (
	"context"
	"encoding/json"

	"golang.org/x/tools/gopls/internal/cache"
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
	return func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		if req.Method() != revisionDefinitionMethod {
			return next(ctx, reply, req)
		}
		var params revisionDefinitionParams
		if err := json.Unmarshal(req.Params(), &params); err != nil {
			return reply(ctx, nil, err)
		}
		locations, err := revisionDefinition(ctx, session, params)
		return reply(ctx, locations, err)
	}
}

func revisionDefinition(ctx context.Context, session *cache.Session, params revisionDefinitionParams) ([]protocol.Location, error) {
	uri := params.TextDocument.URI
	snapshot, release, err := session.SnapshotOf(ctx, uri)
	if err != nil {
		return nil, err
	}
	defer release()

	derived, releaseDerived := snapshot.SnapshotWithText(ctx, uri, []byte(params.Text))
	defer releaseDerived()

	fh, err := derived.ReadFile(ctx, uri)
	if err != nil {
		return nil, err
	}
	// A zero-width range at the cursor: Definition accepts a range because it
	// also handles selections.
	return golang.Definition(ctx, derived, fh, protocol.Range{Start: params.Position, End: params.Position})
}
