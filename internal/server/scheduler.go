package server

import (
	"cmp"
	"slices"
	"strings"
	"sync"
	"time"

	"ecs/internal/config"
)

const (
	dispatchSingle      = "single"
	dispatchBroadcast   = "broadcast"
	dispatchRace        = "race"
	peerFailureCooldown = 30 * time.Second
)

type peerDispatchStats struct {
	peer        string
	inFlight    int
	successes   uint64
	failures    uint64
	latencyEWMA time.Duration
	lastFailure time.Time
}

type peerScheduler struct {
	mu     sync.Mutex
	cursor uint64
	stats  map[string]*peerDispatchStats
}

type peerMetric struct {
	Peer              string     `json:"peer"`
	Identity          string     `json:"identity"`
	InFlight          int        `json:"in_flight"`
	Successes         uint64     `json:"successes"`
	Failures          uint64     `json:"failures"`
	FailurePermille   uint64     `json:"failure_permille"`
	LatencyEWMAMillis int64      `json:"latency_ewma_ms"`
	LastFailure       *time.Time `json:"last_failure,omitempty"`
}

func newPeerScheduler() *peerScheduler {
	return &peerScheduler{stats: make(map[string]*peerDispatchStats)}
}

func normalizeDispatch(mode string, fanout int, defaultMode string, defaultFanout int) (string, int, bool) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = defaultMode
	}
	if mode == "" {
		mode = dispatchSingle
	}
	switch mode {
	case dispatchSingle:
		return mode, 1, true
	case dispatchBroadcast:
		return mode, 0, true
	case dispatchRace:
		if fanout == 0 {
			fanout = defaultFanout
		}
		if fanout < 1 || fanout > 32 {
			return "", 0, false
		}
		return mode, fanout, true
	default:
		return "", 0, false
	}
}

// selectAndReserve scores peers from locally observed work only. Group and
// online filtering happen before this call. Reserving under the same lock keeps
// concurrent requests from all selecting a peer that merely looked idle.
func (s *peerScheduler) selectAndReserve(peers []config.Peer, mode string, fanout int) []config.Peer {
	if len(peers) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	ordered := append([]config.Peer(nil), peers...)
	slices.SortFunc(ordered, func(a, b config.Peer) int {
		return cmp.Compare(peerKey(a), peerKey(b))
	})
	if len(ordered) > 1 {
		offset := int(s.cursor % uint64(len(ordered)))
		ordered = append(ordered[offset:], ordered[:offset]...)
	}
	s.cursor++
	now := time.Now()
	slices.SortStableFunc(ordered, func(a, b config.Peer) int {
		return compareDispatchStats(s.stats[peerKey(a)], s.stats[peerKey(b)], now)
	})

	count := len(ordered)
	switch mode {
	case dispatchSingle:
		count = 1
	case dispatchRace:
		count = min(fanout, count)
	}
	selected := append([]config.Peer(nil), ordered[:count]...)
	for _, peer := range selected {
		key := peerKey(peer)
		stats := s.stats[key]
		if stats == nil {
			stats = &peerDispatchStats{}
			s.stats[key] = stats
		}
		if peer.Name != "" {
			stats.peer = peer.Name
		}
		stats.inFlight++
	}
	return selected
}

func (s *peerScheduler) complete(peer config.Peer, duration time.Duration, success bool, observed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := peerKey(peer)
	stats := s.stats[key]
	if stats == nil {
		stats = &peerDispatchStats{}
		s.stats[key] = stats
	}
	if peer.Name != "" {
		stats.peer = peer.Name
	}
	if stats.inFlight > 0 {
		stats.inFlight--
	}
	if !observed {
		return
	}
	if success {
		stats.successes++
	} else {
		stats.failures++
		stats.lastFailure = time.Now()
	}
	if stats.latencyEWMA == 0 {
		stats.latencyEWMA = duration
	} else {
		stats.latencyEWMA = (stats.latencyEWMA*4 + duration) / 5
	}
}

func (s *peerScheduler) snapshot() []peerMetric {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.stats))
	for key := range s.stats {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	result := make([]peerMetric, 0, len(keys))
	for _, key := range keys {
		value := s.stats[key]
		peerName := value.peer
		if peerName == "" {
			peerName = key
		}
		metric := peerMetric{
			Peer: peerName, Identity: key, InFlight: value.inFlight, Successes: value.successes,
			Failures: value.failures, FailurePermille: failurePermille(*value),
			LatencyEWMAMillis: value.latencyEWMA.Milliseconds(),
		}
		if !value.lastFailure.IsZero() {
			lastFailure := value.lastFailure.UTC()
			metric.LastFailure = &lastFailure
		}
		result = append(result, metric)
	}
	return result
}

func compareDispatchStats(a, b *peerDispatchStats, now time.Time) int {
	var aValue, bValue peerDispatchStats
	if a != nil {
		aValue = *a
	}
	if b != nil {
		bValue = *b
	}
	if result := cmp.Compare(aValue.inFlight, bValue.inFlight); result != 0 {
		return result
	}
	if result := cmp.Compare(recentFailurePermille(aValue, now), recentFailurePermille(bValue, now)); result != 0 {
		return result
	}
	return cmp.Compare(aValue.latencyEWMA, bValue.latencyEWMA)
}

func recentFailurePermille(stats peerDispatchStats, now time.Time) uint64 {
	if stats.lastFailure.IsZero() || now.Sub(stats.lastFailure) >= peerFailureCooldown {
		return 0
	}
	return failurePermille(stats)
}

func failurePermille(stats peerDispatchStats) uint64 {
	total := stats.successes + stats.failures
	if total == 0 {
		return 0
	}
	return stats.failures * 1000 / total
}

func peerKey(peer config.Peer) string {
	if peer.NodeID != "" {
		return "node:" + peer.NodeID
	}
	return "url:" + strings.TrimRight(peer.URL, "/")
}

func peerNames(peers []config.Peer) []string {
	result := make([]string, 0, len(peers))
	for _, peer := range peers {
		if peer.Name != "" {
			result = append(result, peer.Name)
		} else {
			result = append(result, peerKey(peer))
		}
	}
	return result
}
