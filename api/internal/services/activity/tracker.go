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
	"k8s.io/client-go/util/retry"

	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

const flushInterval = 60 * time.Second

type ActivityTracker struct {
	Mu        sync.Mutex
	Activity  map[string]time.Time
	LastFlush map[string]time.Time
	K8sClient pkginterfaces.KubernetesClient
	Logger    pkginterfaces.LoggerInterface
	Namespace string
	StopCh    chan struct{}
	StopOnce  sync.Once
	Done      chan struct{}
}

func NewActivityTracker(
	k8sClient pkginterfaces.KubernetesClient,
	logger pkginterfaces.LoggerInterface,
	namespace string,
) *ActivityTracker {
	return &ActivityTracker{
		Activity:  make(map[string]time.Time),
		LastFlush: make(map[string]time.Time),
		K8sClient: k8sClient,
		Logger:    logger,
		Namespace: namespace,
		StopCh:    make(chan struct{}),
		Done:      make(chan struct{}),
	}
}

func (t *ActivityTracker) Start() error {
	go t.runFlushLoop()
	return nil
}

func (t *ActivityTracker) Stop() error {
	t.StopOnce.Do(func() {
		close(t.StopCh)
	})
	<-t.Done
	return nil
}

func (t *ActivityTracker) Record(workspaceID string) {
	if workspaceID == "" {
		return
	}
	t.Mu.Lock()
	t.Activity[workspaceID] = time.Now()
	t.Mu.Unlock()
}

func (t *ActivityTracker) Flush() {
	t.Mu.Lock()
	now := time.Now()
	var toFlush []struct {
		id   string
		time time.Time
	}
	for wsID, activityTime := range t.Activity {
		lastTime, flushed := t.LastFlush[wsID]
		if !flushed || activityTime.After(lastTime) {
			toFlush = append(toFlush, struct {
				id   string
				time time.Time
			}{wsID, activityTime})
			t.LastFlush[wsID] = now
		}
	}
	t.Mu.Unlock()

	for _, item := range toFlush {
		if err := t.flushOne(context.Background(), item.id, item.time); err != nil {
			if apierrors.IsNotFound(err) {
				t.Delete(item.id)
			} else {
				t.Logger.Error("Failed to flush activity", err, "workspaceID", item.id)
			}
		}
	}
}

func (t *ActivityTracker) PendingCount() int {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	return len(t.Activity)
}

func (t *ActivityTracker) Delete(workspaceID string) {
	t.Mu.Lock()
	delete(t.Activity, workspaceID)
	delete(t.LastFlush, workspaceID)
	t.Mu.Unlock()
}

func (t *ActivityTracker) runFlushLoop() {
	defer close(t.Done)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.Flush()
		case <-t.StopCh:
			t.Flush()
			return
		}
	}
}

func (t *ActivityTracker) flushOne(ctx context.Context, workspaceID string, activityTime time.Time) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		v1Client, err := t.K8sClient.LlmsafespaceV1()
		if err != nil {
			return fmt.Errorf("initialize LLMSafespaceV1 client: %w", err)
		}
		ws, err := v1Client.Workspaces(t.Namespace).Get(ctx, workspaceID, metav1.GetOptions{})
		if err != nil {
			return err
		}
		now := metav1.NewTime(activityTime)
		ws.Status.LastActivityAt = &now
		_, err = v1Client.Workspaces(t.Namespace).UpdateStatus(ctx, ws)
		return err
	})
}
