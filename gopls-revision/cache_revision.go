// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"time"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
)

// SnapshotWithText returns a snapshot derived from s in which uri has the
// given text, without changing the session's overlays or the view's current
// snapshot. The derived snapshot shares s's store; release must be called
// when it is no longer needed.
func (s *Snapshot) SnapshotWithText(ctx context.Context, uri protocol.DocumentURI, text []byte) (*Snapshot, func()) {
	fh := &overlay{
		uri:     uri,
		content: text,
		modTime: time.Now(),
		hash:    file.HashOf(text),
		version: 0,
		kind:    file.Go,
		saved:   false,
	}
	// Detach from the parent snapshot's background context: a didChange that
	// replaces that snapshot cancels it, and must not cancel this request.
	derived, _ := s.clone(ctx, context.WithoutCancel(s.backgroundCtx), StateChange{
		Files: map[protocol.DocumentURI]file.Handle{uri: fh},
	}, func() {})
	return derived, func() {
		derived.cancel()
		derived.decref()
	}
}
