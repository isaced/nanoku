package api

import "sync"

// DeployLock serializes HTTP-triggered deploys per app so a duplicated
// request (CI retries, two pushes in the same second) doesn't race on the
// same container / caddyfile. Manual UI deploys also take the lock so an
// external trigger never lands mid-manual-redeploy.
//
// Different apps deploy in parallel.
type DeployLock struct {
	mu   sync.Mutex
	busy map[int]struct{}
}

func NewDeployLock() *DeployLock {
	return &DeployLock{busy: make(map[int]struct{})}
}

func (l *DeployLock) TryAcquire(appID int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.busy[appID]; ok {
		return false
	}
	l.busy[appID] = struct{}{}
	return true
}

func (l *DeployLock) Release(appID int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.busy, appID)
}

func (l *DeployLock) IsBusy(appID int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.busy[appID]
	return ok
}
