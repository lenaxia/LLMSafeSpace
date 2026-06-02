// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package sessionindex

import (
	"context"
	"sync"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// Service manages the session_index table with non-blocking writes.
type Service struct {
	db     interfaces.DatabaseService
	logger *logger.Logger
	queue  chan recordEvent
	closeC chan struct{}
	wg     sync.WaitGroup
}

type recordEvent struct {
	workspaceID string
	sessionID   string
	title       string
	at          time.Time
}

// New creates a new SessionIndexService.
func New(db interfaces.DatabaseService, log *logger.Logger) *Service {
	return &Service{
		db:     db,
		logger: log,
		queue:  make(chan recordEvent, 1024),
		closeC: make(chan struct{}),
	}
}

// Start begins the background drainer goroutine.
func (s *Service) Start() error {
	s.wg.Add(1)
	go s.drain()
	s.logger.Info("SessionIndexService started")
	return nil
}

// Stop signals the drainer to stop and waits for it to finish.
func (s *Service) Stop() error {
	close(s.closeC)
	s.wg.Wait()
	s.logger.Info("SessionIndexService stopped")
	return nil
}

// RecordMessage is non-blocking: pushes to a bounded channel.
// If the channel is full, the oldest event is dropped.
func (s *Service) RecordMessage(workspaceID, sessionID, title string, at time.Time) {
	select {
	case s.queue <- recordEvent{workspaceID: workspaceID, sessionID: sessionID, title: title, at: at}:
	default:
		// Channel full — drop oldest by reading one and pushing new
		select {
		case <-s.queue:
		default:
		}
		s.queue <- recordEvent{workspaceID: workspaceID, sessionID: sessionID, title: title, at: at}
	}
}

// ListByWorkspace returns session metadata ordered by last_message_at DESC.
func (s *Service) ListByWorkspace(ctx context.Context, workspaceID string) ([]types.SessionListItem, error) {
	return s.db.ListSessionIndex(ctx, workspaceID)
}

// DeleteByWorkspace removes all session index entries for a workspace.
func (s *Service) DeleteByWorkspace(ctx context.Context, workspaceID string) error {
	return s.db.DeleteSessionIndex(ctx, workspaceID)
}

// UpsertTitle updates just the title for a session.
func (s *Service) UpsertTitle(ctx context.Context, workspaceID, sessionID, title string) error {
	return s.db.UpsertSessionTitle(ctx, workspaceID, sessionID, title)
}

// UpsertParent records the parent session of a (sub)session. Used by the
// proxy to mirror opencode's session.parentID into the sidebar's
// session_index so the sidebar can render hierarchy without round-tripping
// the agent. Empty parentID is allowed (top-level session) but uncommon —
// callers typically only invoke this when they have observed a non-empty
// parentID on a session.
func (s *Service) UpsertParent(ctx context.Context, workspaceID, sessionID, parentID string) error {
	return s.db.UpsertSessionParent(ctx, workspaceID, sessionID, parentID)
}

func (s *Service) drain() {
	defer s.wg.Done()
	for {
		select {
		case ev := <-s.queue:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := s.db.UpsertSessionMessage(ctx, ev.workspaceID, ev.sessionID, ev.at); err != nil {
				if s.logger != nil {
					s.logger.Error("session index upsert failed", err)
				}
			}
			cancel()
		case <-s.closeC:
			// Drain remaining
			for len(s.queue) > 0 {
				ev := <-s.queue
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = s.db.UpsertSessionMessage(ctx, ev.workspaceID, ev.sessionID, ev.at)
				cancel()
			}
			return
		}
	}
}
