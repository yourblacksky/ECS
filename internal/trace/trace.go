package trace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"ecs/internal/config"
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type Trace struct {
	Version     string     `json:"version"`
	TraceID     string     `json:"trace_id"`
	Owner       string     `json:"owner,omitempty"`
	RequestID   string     `json:"request_id"`
	Node        string     `json:"node"`
	Endpoint    string     `json:"endpoint"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	DurationMS  int64      `json:"duration_ms"`
	Status      string     `json:"status"`
	Summary     string     `json:"summary,omitempty"`
	Error       string     `json:"error,omitempty"`
	Links       []Link     `json:"links,omitempty"`
	Root        *Span      `json:"root"`
}

type Link struct {
	Node    string `json:"node"`
	URL     string `json:"url"`
	TraceID string `json:"trace_id"`
}

type Summary struct {
	TraceID     string     `json:"trace_id"`
	Owner       string     `json:"owner,omitempty"`
	RequestID   string     `json:"request_id"`
	Node        string     `json:"node"`
	Endpoint    string     `json:"endpoint"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	DurationMS  int64      `json:"duration_ms"`
	Status      string     `json:"status"`
	Summary     string     `json:"summary,omitempty"`
	Error       string     `json:"error,omitempty"`
	LinkCount   int        `json:"link_count"`
}

type EvidenceNode struct {
	Node     string         `json:"node"`
	URL      string         `json:"url,omitempty"`
	Trace    *Trace         `json:"trace,omitempty"`
	Error    string         `json:"error,omitempty"`
	Children []EvidenceNode `json:"children,omitempty"`
}

type EvidenceBundle struct {
	Version     string       `json:"version"`
	TraceID     string       `json:"trace_id"`
	GeneratedAt time.Time    `json:"generated_at"`
	Root        EvidenceNode `json:"root"`
}

type Span struct {
	SpanID      string         `json:"span_id"`
	ParentID    string         `json:"parent_id,omitempty"`
	Kind        string         `json:"kind"`
	Name        string         `json:"name"`
	Node        string         `json:"node,omitempty"`
	Agent       string         `json:"agent,omitempty"`
	Depth       int            `json:"depth"`
	ModelRound  int            `json:"model_round,omitempty"`
	StartedAt   time.Time      `json:"started_at"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
	DurationMS  int64          `json:"duration_ms"`
	Status      string         `json:"status"`
	Input       any            `json:"input,omitempty"`
	Output      any            `json:"output,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
	Error       string         `json:"error,omitempty"`
	Children    []*Span        `json:"children,omitempty"`
}

type Store struct {
	cfg config.TraceConfig
	mu  sync.Mutex
}

type Recorder struct {
	store    *Store
	path     string
	mu       sync.Mutex
	trace    Trace
	index    map[string]*Span
	next     uint64
	maxChars int
}

func NewStore(cfg config.TraceConfig) (*Store, error) {
	if !cfg.Enabled {
		return &Store{cfg: cfg}, nil
	}
	if err := os.MkdirAll(cfg.Directory, 0o700); err != nil {
		return nil, err
	}
	store := &Store{cfg: cfg}
	if err := store.Prune(time.Now().UTC()); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Start(traceID string, requestID string, node string, endpoint string, input any) *Recorder {
	return s.StartOwned("local", traceID, requestID, node, endpoint, input)
}

func (s *Store) StartOwned(owner string, traceID string, requestID string, node string, endpoint string, input any) *Recorder {
	if s == nil || !s.cfg.Enabled || !safeID.MatchString(traceID) {
		return nil
	}
	// Reserve the evidence filename before recording. An existing audit record
	// is immutable from a new request: a collision receives a fresh internal ID
	// instead of allowing the later atomic rename to overwrite prior evidence.
	s.mu.Lock()
	path := filepath.Join(s.cfg.Directory, traceID+".json")
	for {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_ = file.Close()
			break
		}
		if !os.IsExist(err) {
			s.mu.Unlock()
			return nil
		}
		traceID = NewID()
		path = filepath.Join(s.cfg.Directory, traceID+".json")
	}
	s.mu.Unlock()
	now := time.Now().UTC()
	root := &Span{
		SpanID:    "span-1",
		Kind:      "request",
		Name:      endpoint,
		Node:      node,
		StartedAt: now,
		Status:    "running",
		Input:     normalize(input, s.cfg.MaxValueChars),
	}
	recorder := &Recorder{
		store: s,
		path:  path,
		trace: Trace{
			Version:   "ecs.trace/v1",
			TraceID:   traceID,
			Owner:     normalizeOwner(owner),
			RequestID: requestID,
			Node:      node,
			Endpoint:  endpoint,
			StartedAt: now,
			Status:    "running",
			Root:      root,
		},
		index:    map[string]*Span{"span-1": root},
		next:     1,
		maxChars: s.cfg.MaxValueChars,
	}
	_ = recorder.persistLocked()
	return recorder
}

// TraceID returns the actual collision-safe identifier reserved by Start.
func (r *Recorder) TraceID() string {
	if r == nil {
		return ""
	}
	return r.trace.TraceID
}

func (r *Recorder) RootID() string {
	if r == nil {
		return ""
	}
	return "span-1"
}

func (r *Recorder) StartSpan(parentID string, kind string, name string, fields map[string]any) string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	parent := r.index[parentID]
	if parent == nil {
		parent = r.trace.Root
		parentID = parent.SpanID
	}
	r.next++
	id := fmt.Sprintf("span-%d", r.next)
	span := &Span{
		SpanID:     id,
		ParentID:   parentID,
		Kind:       kind,
		Name:       name,
		StartedAt:  time.Now().UTC(),
		Status:     "running",
		Attributes: normalizeMap(fields, r.maxChars),
	}
	if value, ok := fields["node"].(string); ok {
		span.Node = value
	}
	if value, ok := fields["agent"].(string); ok {
		span.Agent = value
	}
	if value, ok := integer(fields["depth"]); ok {
		span.Depth = value
	}
	if value, ok := integer(fields["model_round"]); ok {
		span.ModelRound = value
	}
	parent.Children = append(parent.Children, span)
	r.index[id] = span
	return id
}

func (r *Recorder) FinishSpan(id string, status string, output any, err error, fields map[string]any) {
	if r == nil || id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	span := r.index[id]
	if span == nil || span.Status != "running" {
		return
	}
	now := time.Now().UTC()
	span.CompletedAt = &now
	span.DurationMS = now.Sub(span.StartedAt).Milliseconds()
	if duration, ok := integer(fields["duration_ms"]); ok {
		span.DurationMS = int64(duration)
	}
	span.Status = status
	span.Output = normalize(output, r.maxChars)
	if err != nil {
		span.Error = truncate(err.Error(), r.maxChars)
	}
	for key, value := range normalizeMap(fields, r.maxChars) {
		if span.Attributes == nil {
			span.Attributes = make(map[string]any)
		}
		span.Attributes[key] = value
	}
}

func (r *Recorder) Event(parentID string, kind string, name string, status string, fields map[string]any) {
	id := r.StartSpan(parentID, kind, name, nil)
	r.FinishSpan(id, status, fields, nil, nil)
}

func (r *Recorder) AddLink(link Link) {
	if r == nil || !safeID.MatchString(link.TraceID) ||
		strings.TrimSpace(link.Node) == "" || strings.TrimSpace(link.URL) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.trace.Links {
		if existing.Node == link.Node && existing.URL == link.URL &&
			existing.TraceID == link.TraceID {
			return
		}
	}
	r.trace.Links = append(r.trace.Links, link)
}

func (r *Recorder) Finish(status string, summary string, err error) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	r.trace.CompletedAt = &now
	r.trace.DurationMS = now.Sub(r.trace.StartedAt).Milliseconds()
	r.trace.Status = status
	r.trace.Summary = truncate(summary, r.maxChars)
	if err != nil {
		r.trace.Error = truncate(err.Error(), r.maxChars)
	}
	if r.trace.Root.Status == "running" {
		r.trace.Root.Status = status
		r.trace.Root.CompletedAt = &now
		r.trace.Root.DurationMS = r.trace.DurationMS
		r.trace.Root.Output = normalize(summary, r.maxChars)
		if err != nil {
			r.trace.Root.Error = truncate(err.Error(), r.maxChars)
		}
	}
	_ = r.persistLocked()
	_ = r.store.Prune(now)
}

func (s *Store) Load(traceID string) (Trace, error) {
	if s == nil || !s.cfg.Enabled || !safeID.MatchString(traceID) {
		return Trace{}, os.ErrNotExist
	}
	data, err := os.ReadFile(filepath.Join(s.cfg.Directory, traceID+".json"))
	if err != nil {
		return Trace{}, err
	}
	var result Trace
	if err := json.Unmarshal(data, &result); err != nil {
		return Trace{}, err
	}
	return result, nil
}

func (s *Store) LoadOwned(owner, traceID string) (Trace, error) {
	value, err := s.Load(traceID)
	if err != nil {
		return Trace{}, err
	}
	owner = normalizeOwner(owner)
	if normalizeOwner(value.Owner) != owner {
		return Trace{}, os.ErrNotExist
	}
	return value, nil
}

func (s *Store) List(limit int, since time.Time) ([]Summary, error) {
	return s.list("", limit, since)
}

func (s *Store) ListOwned(owner string, limit int, since time.Time) ([]Summary, error) {
	return s.list(normalizeOwner(owner), limit, since)
}

func (s *Store) list(owner string, limit int, since time.Time) ([]Summary, error) {
	if s == nil || !s.cfg.Enabled {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	entries, err := os.ReadDir(s.cfg.Directory)
	if err != nil {
		return nil, err
	}
	result := make([]Summary, 0, min(limit, len(entries)))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" ||
			strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		traceID := strings.TrimSuffix(entry.Name(), ".json")
		value, err := s.Load(traceID)
		if err != nil {
			continue
		}
		if !since.IsZero() && value.StartedAt.Before(since) {
			continue
		}
		if owner != "" && normalizeOwner(value.Owner) != owner {
			continue
		}
		result = append(result, summaryOf(value))
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].StartedAt.Equal(result[j].StartedAt) {
			return result[i].StartedAt.After(result[j].StartedAt)
		}
		return result[i].TraceID > result[j].TraceID
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *Store) Count() int {
	values, err := s.List(200, time.Time{})
	if err != nil {
		return 0
	}
	if len(values) < 200 {
		return len(values)
	}
	entries, err := os.ReadDir(s.cfg.Directory)
	if err != nil {
		return len(values)
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" &&
			!strings.HasPrefix(entry.Name(), ".") {
			count++
		}
	}
	return count
}

func (s *Store) Prune(now time.Time) error {
	if s == nil || !s.cfg.Enabled {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.cfg.Directory)
	if err != nil {
		return err
	}
	type candidate struct {
		path      string
		startedAt time.Time
	}
	completed := make([]candidate, 0, len(entries))
	cutoff := time.Time{}
	if s.cfg.Retention.Duration() > 0 {
		cutoff = now.Add(-s.cfg.Retention.Duration())
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" ||
			strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(s.cfg.Directory, entry.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		var value Trace
		if json.Unmarshal(data, &value) != nil || value.CompletedAt == nil {
			continue
		}
		if !cutoff.IsZero() && value.CompletedAt.Before(cutoff) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		completed = append(completed, candidate{path: path, startedAt: value.StartedAt})
	}
	if s.cfg.MaxTraces <= 0 || len(completed) <= s.cfg.MaxTraces {
		return nil
	}
	sort.Slice(completed, func(i, j int) bool {
		return completed[i].startedAt.After(completed[j].startedAt)
	})
	for _, item := range completed[s.cfg.MaxTraces:] {
		if err := os.Remove(item.path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func summaryOf(value Trace) Summary {
	return Summary{
		TraceID: value.TraceID, Owner: value.Owner, RequestID: value.RequestID, Node: value.Node,
		Endpoint: value.Endpoint, StartedAt: value.StartedAt, CompletedAt: value.CompletedAt,
		DurationMS: value.DurationMS, Status: value.Status, Summary: value.Summary,
		Error: value.Error, LinkCount: len(value.Links),
	}
}

func normalizeOwner(owner string) string {
	if strings.TrimSpace(owner) == "" {
		return "local"
	}
	return owner
}

func (r *Recorder) persistLocked() error {
	data, err := json.MarshalIndent(r.trace, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.CreateTemp(r.store.cfg.Directory, ".ecs-trace-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temp, r.path)
}

type contextValue struct {
	recorder *Recorder
	parentID string
}

type contextKey struct{}

func WithRecorder(ctx context.Context, recorder *Recorder, parentID string) context.Context {
	if recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, contextValue{recorder: recorder, parentID: parentID})
}

func FromContext(ctx context.Context) (*Recorder, string) {
	value, _ := ctx.Value(contextKey{}).(contextValue)
	return value.recorder, value.parentID
}

func WithParent(ctx context.Context, parentID string) context.Context {
	recorder, _ := FromContext(ctx)
	return WithRecorder(ctx, recorder, parentID)
}

func NewID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}

func IsValidID(value string) bool {
	return safeID.MatchString(value)
}

func normalize(value any, max int) any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return truncate(fmt.Sprint(value), max)
	}
	if len([]rune(string(data))) > max {
		return truncate(string(data), max)
	}
	var result any
	if err := json.Unmarshal(data, &result); err != nil {
		return truncate(string(data), max)
	}
	return result
}

func normalizeMap(value map[string]any, max int) map[string]any {
	if len(value) == 0 {
		return nil
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = normalize(item, max)
	}
	return result
}

func truncate(value string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max-1]) + "…"
}

func integer(value any) (int, bool) {
	switch item := value.(type) {
	case int:
		return item, true
	case int64:
		return int(item), true
	case float64:
		return int(item), true
	default:
		return 0, false
	}
}
