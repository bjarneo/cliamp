package ipc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const (
	DefaultJobStoreCapacity = 256
	DefaultJobTTL           = 15 * time.Minute
)

var (
	ErrJobNotFound      = errors.New("job not found")
	ErrJobStoreFull     = errors.New("job store is full")
	ErrInvalidJobState  = errors.New("invalid job state transition")
	ErrInvalidJobResult = errors.New("invalid job result")
)

// JobState describes a job's lifecycle.
type JobState string

const (
	JobQueued    JobState = "queued"
	JobRunning   JobState = "running"
	JobSucceeded JobState = "succeeded"
	JobFailed    JobState = "failed"
	JobCanceled  JobState = "canceled"
)

// Job contains immutable-at-the-boundary state for an asynchronous operation.
type Job struct {
	ID         string           `json:"id"`
	Operation  string           `json:"operation"`
	State      JobState         `json:"state"`
	CreatedAt  time.Time        `json:"created_at"`
	StartedAt  *time.Time       `json:"started_at,omitempty"`
	FinishedAt *time.Time       `json:"finished_at,omitempty"`
	Result     json.RawMessage  `json:"result,omitempty"`
	Snapshot   *RuntimeSnapshot `json:"snapshot,omitempty"`
	Error      *V2Error         `json:"error,omitempty"`
}

// JobEvent is emitted once for every terminal job transition.
type JobEvent struct {
	Type string `json:"type"`
	Job  Job    `json:"job"`
}

type storedJob struct {
	ctx    context.Context
	job    Job
	cancel context.CancelFunc
}

// JobStoreOption configures a JobStore.
type JobStoreOption func(*jobStoreConfig)

type jobStoreConfig struct {
	capacity int
	ttl      time.Duration
}

// WithJobStoreCapacity sets the maximum number of retained jobs.
func WithJobStoreCapacity(capacity int) JobStoreOption {
	return func(config *jobStoreConfig) {
		if capacity > 0 {
			config.capacity = capacity
		}
	}
}

// WithJobTTL sets how long terminal jobs remain available.
func WithJobTTL(ttl time.Duration) JobStoreOption {
	return func(config *jobStoreConfig) {
		if ttl > 0 {
			config.ttl = ttl
		}
	}
}

// JobStore retains a bounded set of jobs. All returned jobs are copies, so
// callers cannot mutate store-owned state.
type JobStore struct {
	mu       sync.Mutex
	jobs     map[string]*storedJob
	capacity int
	ttl      time.Duration
	events   chan JobEvent
}

// NewJobStore creates an in-memory job store with sensible bounded defaults.
func NewJobStore(options ...JobStoreOption) *JobStore {
	config := jobStoreConfig{
		capacity: DefaultJobStoreCapacity,
		ttl:      DefaultJobTTL,
	}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	return &JobStore{
		jobs:     make(map[string]*storedJob),
		capacity: config.capacity,
		ttl:      config.ttl,
		events:   make(chan JobEvent, config.capacity),
	}
}

// Create adds a queued job with a context derived from Background.
func (s *JobStore) Create(operation string) (Job, error) {
	return s.CreateWithContext(context.Background(), operation)
}

// CreateWithContext adds a queued job whose execution context inherits parent.
func (s *JobStore) CreateWithContext(parent context.Context, operation string) (Job, error) {
	if operation == "" {
		return Job{}, ErrInvalidJobState
	}
	if parent == nil {
		parent = context.Background()
	}

	id, err := newJobID()
	if err != nil {
		return Job{}, err
	}
	ctx, cancel := context.WithCancel(parent)
	job := Job{
		ID:        id,
		Operation: operation,
		State:     JobQueued,
		CreatedAt: time.Now().UTC(),
	}

	s.mu.Lock()
	s.pruneExpiredLocked(time.Now())
	if len(s.jobs) >= s.capacity && !s.evictTerminalLocked() {
		s.mu.Unlock()
		cancel()
		return Job{}, ErrJobStoreFull
	}
	s.jobs[id] = &storedJob{ctx: ctx, job: job, cancel: cancel}
	s.mu.Unlock()
	return cloneJob(job), nil
}

// Start marks a queued job as running and returns its cancellation context.
func (s *JobStore) Start(id string) (context.Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredLocked(time.Now())
	stored, ok := s.jobs[id]
	if !ok {
		return nil, ErrJobNotFound
	}
	if stored.job.State != JobQueued {
		return nil, ErrInvalidJobState
	}
	now := time.Now().UTC()
	stored.job.State = JobRunning
	stored.job.StartedAt = &now

	return stored.ctx, nil
}

// Succeed stores a JSON result and marks a running job complete.
func (s *JobStore) Succeed(id string, result json.RawMessage) error {
	if err := validV2Result(result); err != nil {
		return ErrInvalidJobResult
	}
	return s.finish(id, JobSucceeded, cloneRawMessage(result), nil, nil)
}

// SucceedSnapshot stores a runtime snapshot and marks a running job complete.
func (s *JobStore) SucceedSnapshot(id string, snapshot RuntimeSnapshot) error {
	return s.finish(id, JobSucceeded, nil, cloneSnapshot(&snapshot), nil)
}

// SucceedWithSnapshot stores a JSON result and the state committed by the
// operation. GUI clients can use the result for operation-specific data and
// the snapshot to advance their local runtime view without another round trip.
func (s *JobStore) SucceedWithSnapshot(id string, result json.RawMessage, snapshot RuntimeSnapshot) error {
	if err := validV2Result(result); err != nil {
		return ErrInvalidJobResult
	}
	return s.finish(id, JobSucceeded, cloneRawMessage(result), cloneSnapshot(&snapshot), nil)
}

// Fail records a structured failure and marks a running job complete.
func (s *JobStore) Fail(id string, failure V2Error) error {
	if failure.Code == "" || failure.Message == "" {
		return ErrInvalidJobResult
	}
	return s.finish(id, JobFailed, nil, nil, &failure)
}

// Cancel cancels the execution context and marks a queued or running job as
// canceled. The terminal event is emitted after the state is durable.
func (s *JobStore) Cancel(id string) error {
	return s.finish(id, JobCanceled, nil, nil, v2Error(V2ErrorCodeCanceled, V2MessageCanceled))
}

// Get returns a copy of a job if it has not expired.
func (s *JobStore) Get(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredLocked(time.Now())
	stored, ok := s.jobs[id]
	if !ok {
		return Job{}, false
	}
	return cloneJob(stored.job), true
}

// Context returns the cancellation context for an active job. It is intended
// for owner-side workers so cancellation prevents late results from committing.
func (s *JobStore) Context(id string) (context.Context, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredLocked(time.Now())
	stored, ok := s.jobs[id]
	if !ok {
		return nil, false
	}
	return stored.ctx, true
}

// Events returns structured terminal transitions. The channel is bounded; if a
// caller falls behind, newer terminal events are dropped rather than blocking
// runtime work.
func (s *JobStore) Events() <-chan JobEvent {
	return s.events
}

// CancelAll cancels every queued or running job. Terminal jobs stay available
// until their normal expiry so clients can observe shutdown cancellation.
func (s *JobStore) CancelAll() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.jobs))
	for id, stored := range s.jobs {
		if !terminalJobState(stored.job.State) {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	for _, id := range ids {
		_ = s.Cancel(id)
	}
}

func (s *JobStore) finish(id string, state JobState, result json.RawMessage, snapshot *RuntimeSnapshot, failure *V2Error) error {
	s.mu.Lock()
	s.pruneExpiredLocked(time.Now())
	stored, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return ErrJobNotFound
	}
	if state == JobCanceled {
		if stored.job.State != JobQueued && stored.job.State != JobRunning {
			s.mu.Unlock()
			return ErrInvalidJobState
		}
	} else if stored.job.State != JobRunning {
		s.mu.Unlock()
		return ErrInvalidJobState
	}
	now := time.Now().UTC()
	stored.job.State = state
	stored.job.FinishedAt = &now
	stored.job.Result = result
	stored.job.Snapshot = snapshot
	stored.job.Error = cloneV2Error(failure)
	stored.cancel()
	event := JobEvent{Type: "job." + string(state), Job: cloneJob(stored.job)}
	s.mu.Unlock()

	select {
	case s.events <- event:
	default:
	}
	return nil
}

func (s *JobStore) pruneExpiredLocked(now time.Time) {
	for id, stored := range s.jobs {
		if !terminalJobState(stored.job.State) || stored.job.FinishedAt == nil {
			continue
		}
		if now.Sub(*stored.job.FinishedAt) >= s.ttl {
			stored.cancel()
			delete(s.jobs, id)
		}
	}
}

func (s *JobStore) evictTerminalLocked() bool {
	var oldestID string
	var oldest time.Time
	for id, stored := range s.jobs {
		if !terminalJobState(stored.job.State) || stored.job.FinishedAt == nil {
			continue
		}
		if oldestID == "" || stored.job.FinishedAt.Before(oldest) {
			oldestID = id
			oldest = *stored.job.FinishedAt
		}
	}
	if oldestID == "" {
		return false
	}
	s.jobs[oldestID].cancel()
	delete(s.jobs, oldestID)
	return true
}

func terminalJobState(state JobState) bool {
	return state == JobSucceeded || state == JobFailed || state == JobCanceled
}

func cloneJob(job Job) Job {
	job.Result = cloneRawMessage(job.Result)
	job.Snapshot = cloneSnapshot(job.Snapshot)
	job.Error = cloneV2Error(job.Error)
	if job.StartedAt != nil {
		started := *job.StartedAt
		job.StartedAt = &started
	}
	if job.FinishedAt != nil {
		finished := *job.FinishedAt
		job.FinishedAt = &finished
	}
	return job
}

func newJobID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}
