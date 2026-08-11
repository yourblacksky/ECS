package observability

import (
	"sort"
	"sync"
	"time"

	"ecs/internal/protocol"
)

type Metrics struct {
	mu            sync.RWMutex
	startedAt     time.Time
	requests      uint64
	toolRuns      map[string]uint64
	skillRuns     map[string]uint64
	verifications map[string]uint64
}

type MetricsSnapshot struct {
	GeneratedAt   time.Time         `json:"generated_at"`
	UptimeSeconds int64             `json:"uptime_seconds"`
	Requests      uint64            `json:"requests"`
	ToolRuns      map[string]uint64 `json:"tool_runs"`
	SkillRuns     map[string]uint64 `json:"skill_runs"`
	Verifications map[string]uint64 `json:"verifications"`
}

func NewMetrics() *Metrics {
	return &Metrics{
		startedAt:     time.Now().UTC(),
		toolRuns:      make(map[string]uint64),
		skillRuns:     make(map[string]uint64),
		verifications: make(map[string]uint64),
	}
}

func (m *Metrics) ObserveReply(response protocol.ReplyResponse) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests++
	for _, run := range response.ToolRuns {
		m.toolRuns[run.Name]++
	}
	for _, run := range response.SkillRuns {
		m.skillRuns[run.Name]++
	}
	if response.Verification != nil {
		m.verifications[response.Verification.Validation]++
	}
}

func (m *Metrics) ObserveVerification(verification *protocol.Verification) {
	if m == nil || verification == nil {
		return
	}
	m.mu.Lock()
	m.verifications[verification.Validation]++
	m.mu.Unlock()
}

func (m *Metrics) Snapshot(now time.Time) MetricsSnapshot {
	if m == nil {
		return MetricsSnapshot{GeneratedAt: now.UTC()}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return MetricsSnapshot{
		GeneratedAt:   now.UTC(),
		UptimeSeconds: int64(now.Sub(m.startedAt).Seconds()),
		Requests:      m.requests,
		ToolRuns:      cloneCounts(m.toolRuns),
		SkillRuns:     cloneCounts(m.skillRuns),
		Verifications: cloneCounts(m.verifications),
	}
}

func cloneCounts(values map[string]uint64) map[string]uint64 {
	result := make(map[string]uint64, len(values))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result[key] = values[key]
	}
	return result
}
