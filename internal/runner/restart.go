package runner

import (
	"sync"
	"time"

	"github.com/gwg605/vsystemd/internal/unit"
)

type restartController struct {
	policy     unit.RestartPolicy
	restartSec time.Duration
	burst      int
	interval   time.Duration

	mu           sync.Mutex
	attempts     []time.Time
	restartCount int
	now          func() time.Time
}

func newRestartController(service *unit.ServiceUnit) *restartController {
	return &restartController{
		policy:     service.Restart,
		restartSec: service.RestartSec,
		burst:      service.StartLimitBurst,
		interval:   service.StartLimitIntervalSec,
		now:        time.Now,
	}
}

func (r *restartController) AllowStart() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	if r.burst > 0 {
		if r.interval > 0 {
			r.prune(now)
		}
		if len(r.attempts) >= r.burst {
			return false
		}
	}
	r.attempts = append(r.attempts, now)
	return true
}

func (r *restartController) ShouldRestart(exitCode int) bool {
	switch r.policy {
	case unit.RestartAlways:
		return true
	case unit.RestartOnFailure:
		return exitCode != 0
	default:
		return false
	}
}

func (r *restartController) Delay() time.Duration {
	return r.restartSec
}

func (r *restartController) RecordRestart() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restartCount++
}

func (r *restartController) RestartCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.restartCount
}

func (r *restartController) prune(now time.Time) {
	cutoff := now.Add(-r.interval)
	index := 0
	for index < len(r.attempts) && r.attempts[index].Before(cutoff) {
		index++
	}
	r.attempts = r.attempts[index:]
}
