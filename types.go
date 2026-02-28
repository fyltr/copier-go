package copier

import (
	"sync"
)

// Phase represents the current stage of a copier operation.
type Phase string

const (
	PhaseUndefined Phase = "undefined"
	PhasePrompt    Phase = "prompt"
	PhaseRender    Phase = "render"
	PhaseTasks     Phase = "tasks"
	PhaseMigrate   Phase = "migrate"
)

// Operation represents the type of copier operation being performed.
type Operation string

const (
	OpCopy   Operation = "copy"
	OpUpdate Operation = "update"
)

// VcsRef is a special enum for VCS reference handling.
type VcsRef string

const (
	// VcsRefCurrent tells copier to use the existing template ref from .copier-answers.yml.
	VcsRefCurrent VcsRef = ":current:"
)

// ConflictStrategy defines how merge conflicts are handled during updates.
type ConflictStrategy string

const (
	ConflictInline ConflictStrategy = "inline"
	ConflictReject ConflictStrategy = "rej"
)

// QuestionType defines the expected answer type for a question.
type QuestionType string

const (
	TypeStr   QuestionType = "str"
	TypeInt   QuestionType = "int"
	TypeFloat QuestionType = "float"
	TypeBool  QuestionType = "bool"
	TypeYAML  QuestionType = "yaml"
	TypeJSON  QuestionType = "json"
	TypePath  QuestionType = "path"
)

// DefaultExclude contains patterns excluded from all template renders.
var DefaultExclude = []string{
	"copier.yaml",
	"copier.yml",
	"~*",
	"*.py[co]",
	"__pycache__",
	".git",
	".DS_Store",
	".svn",
}

// AnswersFileName is the default file for storing copier answers.
const AnswersFileName = ".copier-answers.yml"

// DefaultTemplateSuffix is the suffix identifying Jinja template files.
const DefaultTemplateSuffix = ".jinja"

// LazyMap is a concurrent-safe map where values are lazily computed on first access.
// Compute functions are called at most once per key.
type LazyMap[V any] struct {
	mu      sync.RWMutex
	cached  map[string]V
	funcs   map[string]func() V
}

// NewLazyMap creates a LazyMap with the given compute functions.
func NewLazyMap[V any](funcs map[string]func() V) *LazyMap[V] {
	return &LazyMap[V]{
		cached: make(map[string]V, len(funcs)),
		funcs:  funcs,
	}
}

// Get retrieves a value, computing it on first access.
func (m *LazyMap[V]) Get(key string) (V, bool) {
	m.mu.RLock()
	if v, ok := m.cached[key]; ok {
		m.mu.RUnlock()
		return v, true
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock.
	if v, ok := m.cached[key]; ok {
		return v, true
	}
	fn, ok := m.funcs[key]
	if !ok {
		var zero V
		return zero, false
	}
	v := fn()
	m.cached[key] = v
	return v, true
}

// Set stores a pre-computed value directly.
func (m *LazyMap[V]) Set(key string, val V) {
	m.mu.Lock()
	m.cached[key] = val
	m.mu.Unlock()
}

// Keys returns all registered keys.
func (m *LazyMap[V]) Keys() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.funcs))
	for k := range m.funcs {
		keys = append(keys, k)
	}
	return keys
}
