package copier

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// AnswersMap holds layered answer data with defined precedence.
// Lookup order: User → Init → Metadata → Last → UserDefaults → Builtin.
type AnswersMap struct {
	User         map[string]any // Interactive answers from the current session.
	Init         map[string]any // Pre-set answers from --data flags.
	Metadata     map[string]any // Template metadata (_src_path, _commit).
	Last         map[string]any // Answers from a previous .copier-answers.yml.
	UserDefaults map[string]any // User-configured defaults from settings.
	Hidden       map[string]bool // Questions whose answers should not be persisted.
}

// NewAnswersMap creates an AnswersMap with initialised maps.
func NewAnswersMap() *AnswersMap {
	return &AnswersMap{
		User:         make(map[string]any),
		Init:         make(map[string]any),
		Metadata:     make(map[string]any),
		Last:         make(map[string]any),
		UserDefaults: make(map[string]any),
		Hidden:       make(map[string]bool),
	}
}

// Get looks up a value following the precedence chain.
func (a *AnswersMap) Get(key string) (any, bool) {
	for _, layer := range a.layers() {
		if v, ok := layer[key]; ok {
			return v, true
		}
	}
	return nil, false
}

// Combined returns a flat map merging all layers in precedence order.
func (a *AnswersMap) Combined() map[string]any {
	result := make(map[string]any)
	// Apply in reverse precedence so higher-priority layers overwrite.
	layers := a.layers()
	for i := len(layers) - 1; i >= 0; i-- {
		for k, v := range layers[i] {
			result[k] = v
		}
	}
	return result
}

// Remembered returns answers suitable for writing to the answers file,
// excluding hidden questions and metadata.
func (a *AnswersMap) Remembered() map[string]any {
	combined := a.Combined()
	result := make(map[string]any)
	for k, v := range combined {
		if strings.HasPrefix(k, "_") {
			continue
		}
		if a.Hidden[k] {
			continue
		}
		result[k] = v
	}
	return result
}

func (a *AnswersMap) layers() []map[string]any {
	return []map[string]any{
		a.User,
		a.Init,
		a.Metadata,
		a.Last,
		a.UserDefaults,
		builtinDefaults(),
	}
}

// builtinDefaults returns the built-in default data available to all templates.
func builtinDefaults() map[string]any {
	return map[string]any{
		"now":         time.Now().UTC().Format(time.RFC3339),
		"make_secret": makeSecret(),
	}
}

func makeSecret() string {
	b := make([]byte, 48)
	_, _ = rand.Read(b)
	h := sha512.Sum512(b)
	return hex.EncodeToString(h[:])
}

// Prompter handles interactive question prompting.
// It is an interface to allow testing and alternative UIs.
type Prompter interface {
	Ask(q QuestionDef, currentAnswers map[string]any) (any, error)
	Confirm(message string, defaultVal bool) (bool, error)
}

// ResolveDefault returns the effective default for a question given all answer sources.
// Priority: Init → Last → UserDefaults → Settings → Question.Default.
func ResolveDefault(q QuestionDef, answers *AnswersMap, settings *Settings) any {
	if v, ok := answers.Init[q.Name]; ok {
		return v
	}
	if v, ok := answers.Last[q.Name]; ok {
		return v
	}
	if v, ok := answers.UserDefaults[q.Name]; ok {
		return v
	}
	if settings != nil {
		if v, ok := settings.DefaultFor(q.Name); ok {
			return v
		}
	}
	return q.Default
}

// ParseAnswer converts a raw answer value to the expected Go type for the question.
func ParseAnswer(q QuestionDef, raw any) (any, error) {
	if raw == nil {
		return nil, nil
	}

	switch q.Type {
	case TypeStr, "":
		return fmt.Sprintf("%v", raw), nil
	case TypeBool:
		return parseBool(raw)
	case TypeInt:
		return parseInt(raw)
	case TypeFloat:
		return parseFloat(raw)
	case TypeYAML:
		return parseYAML(raw)
	case TypeJSON:
		return parseJSON(raw)
	case TypePath:
		return fmt.Sprintf("%v", raw), nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrInvalidType, q.Type)
	}
}

func parseBool(raw any) (bool, error) {
	switch v := raw.(type) {
	case bool:
		return v, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "on", "1":
			return true, nil
		case "false", "no", "off", "0", "":
			return false, nil
		}
		return false, fmt.Errorf("cannot parse %q as bool", v)
	case int, int64:
		return v != 0, nil
	case float64:
		return v != 0, nil
	}
	return false, fmt.Errorf("cannot parse %T as bool", raw)
}

func parseInt(raw any) (int64, error) {
	switch v := raw.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		return int64(v), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	}
	return 0, fmt.Errorf("cannot parse %T as int", raw)
}

func parseFloat(raw any) (float64, error) {
	switch v := raw.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(strings.TrimSpace(v), 64)
	}
	return 0, fmt.Errorf("cannot parse %T as float", raw)
}

func parseYAML(raw any) (any, error) {
	switch v := raw.(type) {
	case string:
		var out any
		if err := yaml.Unmarshal([]byte(v), &out); err != nil {
			return nil, fmt.Errorf("parsing YAML answer: %w", err)
		}
		return out, nil
	default:
		return raw, nil
	}
}

func parseJSON(raw any) (any, error) {
	// JSON is valid YAML, so we reuse the YAML parser.
	return parseYAML(raw)
}

// ShouldAsk evaluates the when condition for a question.
// Returns true if the question should be shown.
func ShouldAsk(q QuestionDef, renderer *Renderer, answers map[string]any) bool {
	switch v := q.When.(type) {
	case nil:
		return true
	case bool:
		return v
	case string:
		if v == "" {
			return true
		}
		result, err := renderer.RenderString(v, answers)
		if err != nil {
			return true // show on error
		}
		trimmed := strings.TrimSpace(strings.ToLower(result))
		return trimmed != "" && trimmed != "false" && trimmed != "0"
	default:
		return true
	}
}

// ValidateAnswer runs the question's validator template.
// Returns nil if valid, or a ValidationError if the validator returns a non-empty string.
func ValidateAnswer(q QuestionDef, answer any, renderer *Renderer, answers map[string]any) error {
	if q.Validator == "" {
		return nil
	}
	ctx := make(map[string]any, len(answers)+1)
	for k, v := range answers {
		ctx[k] = v
	}
	ctx[q.Name] = answer

	result, err := renderer.RenderString(q.Validator, ctx)
	if err != nil {
		return &ValidationError{Question: q.Name, Message: err.Error()}
	}
	trimmed := strings.TrimSpace(result)
	if trimmed != "" {
		return &ValidationError{Question: q.Name, Message: trimmed}
	}
	return nil
}
