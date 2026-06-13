// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package kubernetes

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/cache"

	kmocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
)

// countingInformer is a cache.SharedIndexInformer double whose only meaningful
// behaviour is Run(): it records how many times it was invoked and then blocks
// until stopCh is closed. Every other method panics — StartInformers only ever
// calls Run(), so any other call indicates a contract drift worth failing on.
type countingInformer struct {
	mu       sync.Mutex
	runCount int
}

func (c *countingInformer) increment() {
	c.mu.Lock()
	c.runCount++
	c.mu.Unlock()
}

func (c *countingInformer) RunCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runCount
}

func (c *countingInformer) Run(stopCh <-chan struct{}) {
	c.increment()
	<-stopCh // mirror the real informer: stay alive until stopped
}

func (c *countingInformer) AddEventHandler(cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	panic("not used by StartInformers")
}
func (c *countingInformer) AddEventHandlerWithResyncPeriod(cache.ResourceEventHandler, time.Duration) (cache.ResourceEventHandlerRegistration, error) {
	panic("not used by StartInformers")
}
func (c *countingInformer) RemoveEventHandler(cache.ResourceEventHandlerRegistration) error {
	panic("not used by StartInformers")
}
func (c *countingInformer) GetStore() cache.Store { panic("not used by StartInformers") }
func (c *countingInformer) GetController() cache.Controller {
	panic("not used by StartInformers")
}
func (c *countingInformer) HasSynced() bool                          { panic("not used by StartInformers") }
func (c *countingInformer) LastSyncResourceVersion() string           { panic("not used by StartInformers") }
func (c *countingInformer) SetWatchErrorHandler(cache.WatchErrorHandler) error {
	panic("not used by StartInformers")
}
func (c *countingInformer) SetTransform(cache.TransformFunc) error { panic("not used by StartInformers") }
func (c *countingInformer) IsStopped() bool                         { panic("not used by StartInformers") }
func (c *countingInformer) AddIndexers(cache.Indexers) error        { panic("not used by StartInformers") }
func (c *countingInformer) GetIndexer() cache.Indexer               { panic("not used by StartInformers") }

// TestStartInformers_Idempotent verifies that calling StartInformers more than
// once launches exactly one set of informer goroutines. The factory guards
// against double-start with a `started` flag; without it, a second call would
// spawn another pair of goroutines that race the first on the same
// SharedIndexInformer (which is not safe to Run twice).
//
// We pre-install counting informers and assert each one's Run() is invoked
// exactly once across two StartInformers calls — the direct, deterministic
// measure of "one set of goroutines".
func TestStartInformers_Idempotent(t *testing.T) {
	client := kmocks.NewMockLLMSafespaceV1Interface()
	f := NewInformerFactory(client, time.Minute, "default")

	re := &countingInformer{}
	ws := &countingInformer{}
	// Inject our doubles directly — same package, so the private fields are
	// visible. StartInformers then skips construction and calls Run on these.
	f.runtimeEnvInf = re
	f.workspaceInf = ws

	stop := make(chan struct{})
	defer close(stop)

	f.StartInformers(stop)

	// Wait for the first StartInformers' goroutines to actually invoke Run.
	require.Eventually(t, func() bool {
		return re.RunCount() == 1 && ws.RunCount() == 1
	}, time.Second, time.Millisecond, "first StartInformers must invoke Run once per informer")

	// Second call must be a no-op: started is already true, so no additional
	// goroutines are spawned and Run is NOT called again.
	f.StartInformers(stop)

	assert.Equal(t, 1, re.RunCount(), "second StartInformers must not re-Run the runtime env informer")
	assert.Equal(t, 1, ws.RunCount(), "second StartInformers must not re-Run the workspace informer")
	assert.True(t, f.started, "factory must remain marked started")
}

// TestStartInformers_OnlyOnce flag-independent sanity: a fresh factory is not
// started before StartInformers is called.
func TestStartInformers_NotStartedByDefault(t *testing.T) {
	client := kmocks.NewMockLLMSafespaceV1Interface()
	f := NewInformerFactory(client, time.Minute, "default")
	assert.False(t, f.started, "new factory must not be started")
}

// TestInformerFactory_CachesInformerInstances verifies that RuntimeEnvironmentInformer()
// and WorkspaceInformer() return the SAME instance on repeat calls. This is the
// caching contract that lets callers register handlers against a stable object
// before StartInformers; a new instance per call would silently drop handlers.
func TestInformerFactory_CachesInformerInstances(t *testing.T) {
	client := kmocks.NewMockLLMSafespaceV1Interface()
	f := NewInformerFactory(client, time.Minute, "default")

	re1 := f.RuntimeEnvironmentInformer()
	require.NotNil(t, re1)
	re2 := f.RuntimeEnvironmentInformer()
	assert.Same(t, re1, re2, "RuntimeEnvironmentInformer must return the cached instance")

	ws1 := f.WorkspaceInformer()
	require.NotNil(t, ws1)
	ws2 := f.WorkspaceInformer()
	assert.Same(t, ws1, ws2, "WorkspaceInformer must return the cached instance")

	// The two informer types must be distinct instances.
	assert.NotSame(t, re1, ws1, "runtime env and workspace informers are distinct")
}
