package copier

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// AnswersMap holds layered answer data with defined precedence.
// Lookup order: User → Init → Metadata → Last → UserDefaults → External → Builtin.
type AnswersMap struct {
	User         map[string]any  // Interactive answers from the current session.
	Init         map[string]any  // Pre-set answers from --data flags.
	Metadata     map[string]any  // Template metadata (_src_path, _commit).
	Last         map[string]any  // Answers from a previous .copier-answers.yml.
	UserDefaults map[string]any  // User-configured defaults from settings.
	External     map[string]any  // Data loaded through `_external_data`.
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
		External:     make(map[string]any),
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
	layers := a.layers()
	for i := len(layers) - 1; i >= 0; i-- {
		for k, v := range layers[i] {
			result[k] = v
		}
	}
	return result
}

// Remembered returns answers suitable for writing to the answers file,
// excluding hidden questions, metadata and built-in data.
func (a *AnswersMap) Remembered() map[string]any {
	combined := a.Combined()
	result := make(map[string]any)
	for k, v := range combined {
		if strings.HasPrefix(k, "_") || a.Hidden[k] {
			continue
		}
		if _, builtin := builtinDefaults()[k]; builtin {
			continue
		}
		if !isJSONSerializable(v) {
			continue
		}
		result[k] = v
	}
	return result
}

// Hide marks an answer as not to be persisted.
func (a *AnswersMap) Hide(key string) { a.Hidden[key] = true }

func (a *AnswersMap) layers() []map[string]any {
	external := a.External
	if external == nil {
		external = map[string]any{}
	}
	return []map[string]any{
		a.User,
		a.Init,
		a.Metadata,
		a.Last,
		a.UserDefaults,
		{"_external_data": external},
		builtinDefaults(),
	}
}

// builtinDefaults returns the (deprecated upstream) built-in data available
// to all templates. Like upstream, `now` and `make_secret` are callables.
func builtinDefaults() map[string]any {
	return map[string]any{
		"now":         func() string { return time.Now().UTC().Format("2006-01-02 15:04:05.000000+00:00") },
		"make_secret": makeSecret,
	}
}

func makeSecret() string {
	b := make([]byte, 48)
	_, _ = rand.Read(b)
	h := sha512.Sum512(b)
	return hex.EncodeToString(h[:])
}

func isJSONSerializable(v any) bool {
	switch v.(type) {
	case nil, string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64,
		[]any, map[string]any, []string, map[string]string:
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// Prompter handles interactive question prompting.
// It is an interface to allow testing and alternative UIs.
type Prompter interface {
	// Ask prompts for an answer. It must return the parsed answer.
	Ask(q *Question) (any, error)
	// Confirm asks a yes/no question.
	Confirm(message string, defaultVal bool) (bool, error)
}

// Choice is a rendered question choice.
type Choice struct {
	Name     string
	Value    any
	Disabled string // Non-empty when the choice cannot be selected; holds the reason.
}

// unsetMarker is rendered by the special `UNSET` variable to force asking.
const unsetMarker = "\x00COPIER_UNSET\x00"

// Question is one question of the questionnaire, bound to the current answers
// and render context. It mirrors upstream Copier's `Question` class.
type Question struct {
	Def QuestionDef

	answers  *AnswersMap
	settings *Settings
	renderer *Renderer
	ctx      map[string]any

	choices      []Choice
	choicesDone  bool
	choicesError error
}

func newQuestion(def QuestionDef, answers *AnswersMap, settings *Settings, renderer *Renderer, ctx map[string]any) *Question {
	if def.When == nil {
		def.When = true
	}
	return &Question{Def: def, answers: answers, settings: settings, renderer: renderer, ctx: ctx}
}

// Name returns the question variable name.
func (q *Question) Name() string { return q.Def.Name }

// renderValue renders a templated value: strings are rendered, lists are
// rendered element-wise and anything else is returned as is.
func (q *Question) renderValue(value any, extra map[string]any) (any, error) {
	switch v := value.(type) {
	case string:
		ctx := make(map[string]any, len(q.ctx)+len(extra))
		for k, val := range q.ctx {
			ctx[k] = val
		}
		for k, val := range extra {
			ctx[k] = val
		}
		return q.renderer.RenderString(v, ctx)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			r, err := q.renderValue(item, extra)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	}
	return value, nil
}

func (q *Question) renderString(value string, extra map[string]any) (string, error) {
	out, err := q.renderValue(value, extra)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%v", out), nil
}

var validTypeNames = map[string]bool{"bool": true, "float": true, "int": true, "json": true, "str": true, "yaml": true, "path": true}

// TypeName returns the rendered question type.
func (q *Question) TypeName() (string, error) {
	name := q.Def.Type
	if name == "" {
		if q.Def.HasDefault {
			name = string(inferType(q.Def.Default))
		} else {
			name = string(TypeYAML)
		}
	} else if strings.Contains(name, "{") {
		rendered, err := q.renderString(name, nil)
		if err != nil {
			return "", err
		}
		name = rendered
	}
	if !validTypeNames[name] {
		return "", fmt.Errorf("%w: unsupported type %q in question %q", ErrInvalidType, name, q.Def.Name)
	}
	return name, nil
}

// CastAnswer casts an answer to the question's type.
func (q *Question) CastAnswer(answer any) (any, error) {
	typeName, err := q.TypeName()
	if err != nil {
		return nil, err
	}
	if answer == nil && typeName != "json" && typeName != "yaml" {
		return nil, fmt.Errorf("%w: invalid answer \"None\" to question %q of type %q", ErrInvalidType, q.Def.Name, typeName)
	}
	if q.Def.Multiselect {
		if list, ok := answer.([]any); ok {
			out := make([]any, len(list))
			for i, item := range list {
				v, err := castToType(typeName, item)
				if err != nil {
					return nil, err
				}
				out[i] = v
			}
			return out, nil
		}
	}
	return castToType(typeName, answer)
}

func castToType(typeName string, value any) (any, error) {
	switch typeName {
	case "bool":
		return castToBool(value), nil
	case "float":
		return castToFloat(value)
	case "int":
		return castToInt(value)
	case "json":
		s, ok := value.(string)
		if !ok {
			return value, nil
		}
		var out any
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		return normalizeJSONNumbers(out), nil
	case "yaml":
		s, ok := value.(string)
		if !ok {
			return value, nil
		}
		return parseYAMLString(s)
	case "str":
		return castToStr(value)
	case "path":
		return fmt.Sprintf("%v", value), nil
	}
	return nil, fmt.Errorf("%w: %s", ErrInvalidType, typeName)
}

// castToBool mirrors upstream `cast_to_bool`.
func castToBool(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f != 0
		}
		lower := strings.ToLower(strings.TrimSpace(v))
		if lower == "" {
			return false
		}
		switch lower {
		case "y", "yes", "t", "true", "on":
			return true
		case "n", "no", "f", "false", "off", "~", "null", "none":
			return false
		}
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map, reflect.Array:
		return rv.Len() > 0
	}
	return true
}

func castToStr(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case bool:
		if v {
			return "True", nil
		}
		return "False", nil
	case int, int64, int32:
		return fmt.Sprintf("%d", v), nil
	case float64:
		if v == math.Trunc(v) && math.Abs(v) < 1e16 {
			return strconv.FormatFloat(v, 'f', 1, 64), nil
		}
		return strconv.FormatFloat(v, 'g', -1, 64), nil
	case []byte:
		return string(v), nil
	}
	return "", fmt.Errorf("%w: could not convert %v to string", ErrInvalidType, value)
}

func castToInt(value any) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case int32:
		return int64(v), nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case float64:
		return int64(v), nil
	case string:
		s := strings.ReplaceAll(strings.TrimSpace(v), "_", "")
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: invalid literal for int(): %q", ErrInvalidType, v)
		}
		return n, nil
	}
	return 0, fmt.Errorf("%w: cannot convert %v to int", ErrInvalidType, value)
}

func castToFloat(value any) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("%w: could not convert %q to float", ErrInvalidType, v)
		}
		return f, nil
	}
	return 0, fmt.Errorf("%w: cannot convert %v to float", ErrInvalidType, value)
}

func parseYAMLString(s string) (any, error) {
	var out any
	if err := yaml.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	return out, nil
}

// normalizeJSONNumbers converts JSON float64 integers into int64 so JSON and
// YAML answers behave alike.
func normalizeJSONNumbers(v any) any {
	switch x := v.(type) {
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return int64(x)
		}
		return x
	case []any:
		for i := range x {
			x[i] = normalizeJSONNumbers(x[i])
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = normalizeJSONNumbers(x[k])
		}
		return x
	}
	return v
}

// parseYAMLList shallowly parses a YAML list, keeping the items as raw strings.
func parseYAMLList(s string) ([]any, error) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(s), &node); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	content := &node
	if content.Kind == yaml.DocumentNode && len(content.Content) > 0 {
		content = content.Content[0]
	}
	if content.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%w: not a YAML list: %q", ErrInvalidType, s)
	}
	items := make([]any, 0, len(content.Content))
	for _, item := range content.Content {
		if item.Kind == yaml.ScalarNode {
			items = append(items, item.Value)
			continue
		}
		raw, err := yaml.Marshal(item)
		if err != nil {
			return nil, err
		}
		items = append(items, strings.TrimSpace(string(raw)))
	}
	return items, nil
}

// Choices returns the rendered choices of the question (empty when none).
func (q *Question) Choices() ([]Choice, error) {
	if q.choicesDone {
		return q.choices, q.choicesError
	}
	q.choicesDone = true
	q.choices, q.choicesError = q.formatChoices()
	return q.choices, q.choicesError
}

func (q *Question) formatChoices() ([]Choice, error) {
	raw := q.Def.Choices
	if raw == nil {
		return nil, nil
	}
	if s, ok := raw.(string); ok {
		rendered, err := q.renderString(s, nil)
		if err != nil {
			return nil, err
		}
		parsed, err := parseYAMLString(rendered)
		if err != nil {
			return nil, err
		}
		raw = parsed
	}
	var items []any
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case []any:
		items = v
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		for _, k := range sortedStrings(keys) {
			items = append(items, []any{k, v[k]})
		}
	default:
		return nil, fmt.Errorf("%w: invalid choices for question %q", ErrConfig, q.Def.Name)
	}
	result := make([]Choice, 0, len(items))
	for _, item := range items {
		var name, value any
		pair, isPair := item.([]any)
		if isPair && len(pair) == 2 {
			name, value = pair[0], pair[1]
		} else {
			name, value = item, item
		}
		renderedName, err := q.renderValue(name, nil)
		if err != nil {
			return nil, err
		}
		nameStr := fmt.Sprintf("%v", renderedName)
		disabled := ""
		if ext, ok := value.(map[string]any); ok && isPair {
			val, ok := ext["value"]
			if !ok {
				return nil, fmt.Errorf("%w: choice property 'value' is required", ErrConfig)
			}
			if validator, ok := ext["validator"]; ok {
				vs, ok := validator.(string)
				if !ok {
					return nil, fmt.Errorf("%w: choice property 'validator' must be a string", ErrConfig)
				}
				disabled, err = q.renderString(vs, nil)
				if err != nil {
					return nil, err
				}
			}
			value = val
		}
		renderedValue, err := q.renderValue(value, nil)
		if err != nil {
			return nil, err
		}
		if renderedValue == nil {
			renderedValue = nameStr
		}
		if _, err := q.CastAnswer(renderedValue); err != nil {
			return nil, err
		}
		result = append(result, Choice{Name: nameStr, Value: renderedValue, Disabled: disabled})
	}
	return result, nil
}

// Default returns the default answer, cast and validated. present is false
// when the question has no default (the user must answer).
func (q *Question) Default() (value any, present bool, err error) {
	name := q.Def.Name
	var result any
	found := false
	if v, ok := q.answers.Init[name]; ok {
		result, found = v, true
	} else if v, ok := q.answers.Last[name]; ok {
		result, found = v, true
	} else if v, ok := q.answers.UserDefaults[name]; ok {
		result, found = v, true
	} else {
		var raw any
		if v, ok := q.settings.DefaultFor(name); ok {
			raw = v
		} else if q.Def.HasDefault {
			raw = q.Def.Default
		} else {
			return nil, false, nil
		}
		rendered, err := q.renderValue(raw, map[string]any{"UNSET": unsetMarker})
		if err != nil {
			return nil, false, err
		}
		if s, ok := rendered.(string); ok && strings.Contains(s, unsetMarker) {
			return nil, false, nil
		}
		result, found = rendered, true
	}
	if !found {
		return nil, false, nil
	}
	parsed, err := q.ParseAnswer(result)
	if err != nil {
		return nil, false, err
	}
	when, err := q.When()
	if err != nil {
		return nil, false, err
	}
	if when && !q.Def.Secret {
		if err := q.ValidateAnswer(parsed); err != nil {
			return nil, false, err
		}
	}
	return parsed, true, nil
}

// DefaultRendered returns the default in the form a prompt should pre-fill:
// bools stay bools, everything else becomes a string (JSON/YAML are dumped).
func (q *Question) DefaultRendered() (any, bool, error) {
	def, present, err := q.Default()
	if err != nil || !present {
		return nil, present, err
	}
	typeName, err := q.TypeName()
	if err != nil {
		return nil, false, err
	}
	if b, ok := def.(bool); ok && typeName == "bool" {
		return b, true, nil
	}
	if def == nil {
		return "", true, nil
	}
	switch typeName {
	case "json":
		var b []byte
		if q.Multiline() {
			b, err = json.MarshalIndent(def, "", "  ")
		} else {
			b, err = json.Marshal(def)
		}
		if err != nil {
			return nil, false, err
		}
		return string(b), true, nil
	case "yaml":
		if q.Multiline() {
			b, err := yaml.Marshal(def)
			if err != nil {
				return nil, false, err
			}
			return strings.TrimSpace(string(b)), true, nil
		}
		return strings.TrimSpace(yamlFlow(def)), true, nil
	}
	s, err := castToStr(def)
	if err != nil {
		return fmt.Sprintf("%v", def), true, nil
	}
	return s, true, nil
}

// yamlFlow dumps a value in YAML flow style (single line) where possible.
func yamlFlow(v any) string {
	switch x := v.(type) {
	case string:
		b, _ := yaml.Marshal(x)
		return strings.TrimSpace(string(b))
	case nil:
		return "null"
	case []any, map[string]any:
		var node yaml.Node
		if err := node.Encode(x); err != nil {
			b, _ := yaml.Marshal(x)
			return strings.TrimSpace(string(b))
		}
		setFlowStyle(&node)
		b, err := yaml.Marshal(&node)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	b, _ := yaml.Marshal(v)
	return strings.TrimSpace(string(b))
}

func setFlowStyle(n *yaml.Node) {
	if n.Kind == yaml.SequenceNode || n.Kind == yaml.MappingNode {
		n.Style = yaml.FlowStyle
	}
	for _, c := range n.Content {
		setFlowStyle(c)
	}
}

// Message returns the prompt message: the rendered help, or the variable name
// with its type.
func (q *Question) Message() string {
	if q.Def.Help != "" {
		if rendered, err := q.renderString(q.Def.Help, nil); err == nil && rendered != "" {
			return rendered
		}
	}
	msg := q.Def.Name
	if t, err := q.TypeName(); err == nil && t != "str" {
		msg += fmt.Sprintf(" (%s)", t)
	}
	return msg
}

// Placeholder returns the rendered placeholder.
func (q *Question) Placeholder() string {
	if q.Def.Placeholder == "" {
		return ""
	}
	rendered, err := q.renderString(q.Def.Placeholder, nil)
	if err != nil {
		return q.Def.Placeholder
	}
	return rendered
}

// Multiline reports whether the question uses multi-line input.
func (q *Question) Multiline() bool {
	if q.Def.Multiline == nil {
		return false
	}
	rendered, err := q.renderValue(q.Def.Multiline, nil)
	if err != nil {
		return false
	}
	return castToBool(rendered)
}

// Secret reports whether the answer must be hidden and not persisted.
func (q *Question) Secret() bool { return q.Def.Secret }

// ValidateAnswer runs the question's validator template. The template must
// render nothing for a valid answer.
func (q *Question) ValidateAnswer(answer any) error {
	if q.Def.Validator == "" {
		return nil
	}
	msg, err := q.renderString(q.Def.Validator, map[string]any{q.Def.Name: answer})
	if err != nil {
		msg = err.Error()
	}
	msg = strings.TrimSpace(msg)
	if msg != "" {
		return &ValidationError{Question: q.Def.Name, Message: msg}
	}
	return nil
}

// When evaluates the skip condition of the question.
func (q *Question) When() (bool, error) {
	rendered, err := q.renderValue(q.Def.When, nil)
	if err != nil {
		return false, err
	}
	return castToBool(rendered), nil
}

// ParseAnswer parses an answer according to the question's type and choices.
func (q *Question) ParseAnswer(answer any) (any, error) {
	if q.Def.Multiselect {
		var items []any
		switch v := answer.(type) {
		case string:
			list, err := parseYAMLList(v)
			if err != nil {
				return nil, err
			}
			items = list
		case []any:
			items = v
		case []string:
			for _, s := range v {
				items = append(items, s)
			}
		case nil:
			items = nil
		default:
			return nil, fmt.Errorf("%w: multiselect answer must be a list", ErrInvalidType)
		}
		parsed := make([]any, 0, len(items))
		for _, item := range items {
			p, err := q.parseSingle(item)
			if err != nil {
				return nil, err
			}
			parsed = append(parsed, p)
		}
		choices, err := q.Choices()
		if err != nil {
			return nil, err
		}
		result := make([]any, 0, len(parsed))
		for _, c := range choices {
			cv, err := q.CastAnswer(c.Value)
			if err != nil {
				return nil, err
			}
			for _, p := range parsed {
				if reflect.DeepEqual(cv, p) {
					result = append(result, cv)
					break
				}
			}
		}
		return result, nil
	}
	return q.parseSingle(answer)
}

func (q *Question) parseSingle(answer any) (any, error) {
	ans, err := q.CastAnswer(answer)
	if err != nil {
		return nil, err
	}
	choices, err := q.Choices()
	if err != nil {
		return nil, err
	}
	if len(choices) == 0 {
		return ans, nil
	}
	choiceError := ""
	valid := make([]string, 0, len(choices))
	for _, c := range choices {
		cv, err := q.CastAnswer(c.Value)
		if err != nil {
			return nil, err
		}
		if c.Disabled == "" {
			valid = append(valid, pyRepr(cv))
		}
		if reflect.DeepEqual(cv, ans) {
			if c.Disabled == "" {
				return ans, nil
			}
			if choiceError == "" {
				choiceError = c.Disabled
			}
		}
	}
	detail := choiceError
	if detail == "" {
		detail = fmt.Sprintf("%s is not in [%s]", pyRepr(ans), strings.Join(valid, ", "))
	}
	return nil, &InvalidChoiceError{Question: q.Def.Name, Detail: detail}
}

// pyRepr formats a value roughly like Python's repr(), for error messages.
func pyRepr(v any) string {
	switch x := v.(type) {
	case string:
		return "'" + x + "'"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	}
	return fmt.Sprintf("%v", v)
}

// ResolveDefault returns the raw effective default for a question, without
// rendering or casting. Kept for API compatibility; prefer Question.Default.
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

// ParseAnswer converts a raw answer value to the Go type for the question,
// without choice validation. Kept for API compatibility.
func ParseAnswer(q QuestionDef, raw any) (any, error) {
	if raw == nil {
		return nil, nil
	}
	typeName := q.Type
	if typeName == "" {
		if q.HasDefault || q.Default != nil {
			typeName = string(inferType(q.Default))
		} else {
			typeName = string(TypeStr)
		}
	}
	return castToType(typeName, raw)
}

func parseBool(raw any) (bool, error) {
	switch v := raw.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "on", "1", "y", "t":
			return true, nil
		case "false", "no", "off", "0", "", "n", "f":
			return false, nil
		}
		return false, fmt.Errorf("cannot parse %q as bool", v)
	}
	return castToBool(raw), nil
}

func parseInt(raw any) (int64, error) { return castToInt(raw) }
