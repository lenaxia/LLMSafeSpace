// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package activity

import (
	"context"
	"fmt"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

const flushInterval = 60 * time.Second

type ActivityTracker struct {
	mu        sync.Mutex
	activity  map[string]time.Time
	lastFlush map[string]time.Time
	k8sClient pkginterfaces.KubernetesClient
	logger    pkginterfaces.LoggerInterface
	namespace string
	stopCh    chan struct{}
	stopOnce  sync.Once
	done      chan struct{}
}

func NewActivityTracker(
	k8sClient pkginterfaces.KubernetesClient,
	logger pkginterfaces.LoggerInterface,
	namespace string,
) *ActivityTracker {
	return &ActivityTracker{
		activity:  make(map[string]time.Time),
		lastFlush: make(map[string]time.Time),
		k8sClient: k8sClient,
		logger:    logger,
		namespace: namespace,
		stopCh:    make(chan struct{}),
		done:      make(chan struct{}),
	}
}

func (t *ActivityTracker) Start() error {
	go t.runFlushLoop()
	return nil
}

func (t *ActivityTracker) Stop() error {
	t.stopOnce.Do(func() {
		close(t.stopCh)
	})
	<-t.done
	return nil
}

func (t *ActivityTracker) Record(workspaceID string) {
	if workspaceID == "" {
		return
	}
	t.mu.Lock()
	t.activity[workspaceID] = time.Now()
	t.mu.Unlock()
}

func (t *ActivityTracker) Flush() {
	t.mu.Lock()
	now := time.Now()
	var toFlush []struct {
		id   string
		time time.Time
	}
	for wsID, activityTime := range t.activity {
		lastTime, flushed := t.lastFlush[wsID]
		if !flushed || activityTime.After(lastTime) {
			toFlush = append(toFlush, struct {
				id   string
				time time.Time
			}{wsID, activityTime})
			t.lastFlush[wsID] = now
		}
	}
	t.mu.Unlock()

	for _, item := range toFlush {
		if err := t.flushOne(context.Background(), item.id, item.time); err != nil {
			if apierrors.IsNotFound(err) {
				t.Delete(item.id)
			} else {
				t.logger.Error("Failed to flush activity", err, "workspaceID", item.id)
			}
		}
	}
}

func (t *ActivityTracker) PendingCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.activity)
}

func (t *ActivityTracker) Delete(workspaceID string) {
	t.mu.Lock()
	delete(t.activity, workspaceID)
	delete(t.lastFlush, workspaceID)
	t.mu.Unlock()
}

func (t *ActivityTracker) runFlushLoop() {
	defer close(t.done)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.Flush()
		case <-t.stopCh:
			t.Flush()
			return
		}
	}
}

func (t *ActivityTracker) flushOne(ctx context.Context, workspaceID string, activityTime time.Time) error {
	// US-23.3: write LastActivityAt to the metadata annotation via a
	// strategic-merge Patch instead of Status.UpdateStatus. This uses
	// the main-resource optimistic-concurrency lane, which is separate
	// from the Status subresource lane the controller writes to. The
	// cross-writer race between ActivityTracker and the controller is
	// eliminated: the Patch only touches annotations, so a conflict-
	// retry re-applies just the annotation without clobbering status.
	patch := fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`,
		v1.AnnotationLastActivityAt, activityTime.Format(time.RFC3339))
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		v1Client, err := t.k8sClient.LlmsafespaceV1()
		if err != nil {
			return fmt.Errorf("initialize LLMSafespaceV1 client: %w", err)
		}
		_, err = v1Client.Workspaces(t.namespace).Patch(
			ctx, workspaceID, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
		return err
	})
}
