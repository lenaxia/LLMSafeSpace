package handlers

import (
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

type PhaseChangeCallback func(sandbox *v1.Sandbox)

type SandboxWatcher struct {
	k8sClient      pkginterfaces.KubernetesClient
	logger         pkginterfaces.LoggerInterface
	namespace      string
	onPhaseChange  PhaseChangeCallback
	stopCh         chan struct{}
	stopOnce       sync.Once
	knownPhases    map[string]string
	knownPhasesMu  sync.RWMutex
	watchRestartMu sync.Mutex
}

func NewSandboxWatcher(
	k8sClient pkginterfaces.KubernetesClient,
	logger pkginterfaces.LoggerInterface,
	namespace string,
	onPhaseChange PhaseChangeCallback,
) (*SandboxWatcher, error) {
	if k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client cannot be nil")
	}
	if onPhaseChange == nil {
		return nil, fmt.Errorf("phase change callback cannot be nil")
	}
	return &SandboxWatcher{
		k8sClient:     k8sClient,
		logger:        logger,
		namespace:     namespace,
		onPhaseChange: onPhaseChange,
		stopCh:        make(chan struct{}),
		knownPhases:   make(map[string]string),
	}, nil
}

func (w *SandboxWatcher) Start() error {
	go w.runWatchLoop()
	return nil
}

func (w *SandboxWatcher) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
}

func (w *SandboxWatcher) GetKnownPhase(name string) (string, bool) {
	w.knownPhasesMu.RLock()
	defer w.knownPhasesMu.RUnlock()
	phase, ok := w.knownPhases[name]
	return phase, ok
}

func (w *SandboxWatcher) runWatchLoop() {
	for {
		select {
		case <-w.stopCh:
			return
		default:
		}

		if err := w.watchOnce(); err != nil {
			w.logger.Error("Sandbox watch error, restarting", err)
			select {
			case <-w.stopCh:
				return
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (w *SandboxWatcher) watchOnce() error {
	w.watchRestartMu.Lock()
	defer w.watchRestartMu.Unlock()

	watcher, err := w.k8sClient.LlmsafespaceV1().Sandboxes(w.namespace).Watch(metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("starting sandbox watch: %w", err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-w.stopCh:
			return nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("watch channel closed")
			}
			w.handleEvent(event)
		}
	}
}

func (w *SandboxWatcher) handleEvent(event watch.Event) {
	sandbox, ok := event.Object.(*v1.Sandbox)
	if !ok {
		return
	}

	name := sandbox.Name
	newPhase := sandbox.Status.Phase

	w.knownPhasesMu.Lock()
	oldPhase, existed := w.knownPhases[name]
	w.knownPhases[name] = newPhase
	w.knownPhasesMu.Unlock()

	if existed && oldPhase != newPhase {
		w.onPhaseChange(sandbox)
	}
}
