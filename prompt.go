package copier

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"charm.land/huh/v2"
)

// TerminalPrompter implements Prompter using charm.land/huh/v2 for terminal UI.
type TerminalPrompter struct{}

// NewTerminalPrompter creates a new terminal prompter.
func NewTerminalPrompter() *TerminalPrompter { return &TerminalPrompter{} }

// Ask prompts the user for an answer to the given question and returns the
// parsed, validated answer.
func (p *TerminalPrompter) Ask(q *Question) (any, error) {
	if !isInteractive() {
		return nil, ErrInteractiveNeeded
	}
	typeName, err := q.TypeName()
	if err != nil {
		return nil, err
	}
	choices, err := q.Choices()
	if err != nil {
		return nil, err
	}
	title := q.Message()
	description := q.Placeholder()

	switch {
	case len(choices) > 0 && q.Def.Multiselect:
		return p.askMultiSelect(q, title, description, choices)
	case len(choices) > 0:
		return p.askSelect(q, title, description, choices)
	case typeName == "bool":
		return p.askBool(q, title, description)
	default:
		return p.askText(q, title, typeName)
	}
}

// Confirm asks a yes/no question.
func (p *TerminalPrompter) Confirm(message string, defaultVal bool) (bool, error) {
	if !isInteractive() {
		return false, ErrInteractiveNeeded
	}
	result := defaultVal
	err := huh.NewConfirm().
		Title(message).
		Value(&result).
		Affirmative("Yes").
		Negative("No").
		Run()
	if err != nil {
		return false, wrapPromptError(err)
	}
	return result, nil
}

func wrapPromptError(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrInterrupted
	}
	return err
}

func (p *TerminalPrompter) askBool(q *Question, title, description string) (any, error) {
	result := false
	if def, present, err := q.Default(); err != nil {
		return nil, err
	} else if present {
		result = castToBool(def)
	}
	err := huh.NewConfirm().
		Title(title).
		Description(description).
		Value(&result).
		Run()
	if err != nil {
		return nil, wrapPromptError(err)
	}
	return q.ParseAnswer(result)
}

func (p *TerminalPrompter) askText(q *Question, title, typeName string) (any, error) {
	var result string
	if def, present, err := q.DefaultRendered(); err != nil {
		return nil, err
	} else if present {
		result = fmt.Sprintf("%v", def)
	}
	validate := func(s string) error {
		parsed, err := q.ParseAnswer(s)
		if err != nil {
			return errors.New("invalid input")
		}
		return q.ValidateAnswer(parsed)
	}

	if q.Def.Secret {
		err := huh.NewInput().
			Title(title).
			EchoMode(huh.EchoModePassword).
			Value(&result).
			Validate(validate).
			Run()
		if err != nil {
			return nil, wrapPromptError(err)
		}
		return q.ParseAnswer(result)
	}

	if q.Multiline() {
		err := huh.NewText().
			Title(title).
			Placeholder(q.Placeholder()).
			Value(&result).
			Validate(validate).
			Run()
		if err != nil {
			return nil, wrapPromptError(err)
		}
		return q.ParseAnswer(result)
	}

	err := huh.NewInput().
		Title(title).
		Placeholder(q.Placeholder()).
		Value(&result).
		Validate(validate).
		Run()
	if err != nil {
		return nil, wrapPromptError(err)
	}
	return q.ParseAnswer(result)
}

func choiceLabel(c Choice) string {
	if c.Disabled != "" {
		return fmt.Sprintf("%s (%s)", c.Name, c.Disabled)
	}
	return c.Name
}

func (p *TerminalPrompter) askSelect(q *Question, title, description string, choices []Choice) (any, error) {
	options := make([]huh.Option[int], 0, len(choices))
	for i, c := range choices {
		options = append(options, huh.NewOption(choiceLabel(c), i))
	}
	selected := 0
	if def, present, err := q.Default(); err != nil {
		return nil, err
	} else if present {
		for i, c := range choices {
			cv, err := q.CastAnswer(c.Value)
			if err == nil && reflect.DeepEqual(cv, def) {
				selected = i
				break
			}
		}
	}
	err := huh.NewSelect[int]().
		Title(title).
		Description(description).
		Options(options...).
		Value(&selected).
		Validate(func(i int) error {
			if i >= 0 && i < len(choices) && choices[i].Disabled != "" {
				return errors.New(choices[i].Disabled)
			}
			return nil
		}).
		Run()
	if err != nil {
		return nil, wrapPromptError(err)
	}
	return q.ParseAnswer(choices[selected].Value)
}

func (p *TerminalPrompter) askMultiSelect(q *Question, title, description string, choices []Choice) (any, error) {
	options := make([]huh.Option[int], 0, len(choices))
	for i, c := range choices {
		options = append(options, huh.NewOption(choiceLabel(c), i))
	}
	var selected []int
	if def, present, err := q.Default(); err != nil {
		return nil, err
	} else if present {
		if defaults, ok := def.([]any); ok {
			for i, c := range choices {
				cv, err := q.CastAnswer(c.Value)
				if err != nil {
					continue
				}
				for _, d := range defaults {
					if reflect.DeepEqual(cv, d) {
						selected = append(selected, i)
						break
					}
				}
			}
		}
	}
	err := huh.NewMultiSelect[int]().
		Title(title).
		Description(description).
		Options(options...).
		Value(&selected).
		Validate(func(sel []int) error {
			for _, i := range sel {
				if i >= 0 && i < len(choices) && choices[i].Disabled != "" {
					return errors.New(choices[i].Disabled)
				}
			}
			return nil
		}).
		Run()
	if err != nil {
		return nil, wrapPromptError(err)
	}
	values := make([]any, 0, len(selected))
	for _, i := range selected {
		values = append(values, choices[i].Value)
	}
	return q.ParseAnswer(values)
}

func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// resolveChoiceLabels is kept for callers that only need display labels.
func resolveChoiceLabels(choices []Choice) []string {
	out := make([]string, 0, len(choices))
	for _, c := range choices {
		out = append(out, strings.TrimSpace(c.Name))
	}
	return out
}
