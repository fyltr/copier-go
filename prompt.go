package copier

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
)

// TerminalPrompter implements Prompter using charmbracelet/huh for terminal UI.
type TerminalPrompter struct{}

// NewTerminalPrompter creates a new terminal prompter.
func NewTerminalPrompter() *TerminalPrompter { return &TerminalPrompter{} }

// Ask prompts the user for an answer to the given question.
func (p *TerminalPrompter) Ask(q QuestionDef, currentAnswers map[string]any) (any, error) {
	if !isInteractive() {
		return nil, ErrInteractiveNeeded
	}

	switch {
	case q.Type == TypeBool:
		return p.askBool(q)
	case q.Choices != nil:
		return p.askChoice(q, currentAnswers)
	case q.Secret:
		return p.askSecret(q)
	case q.Type == TypePath:
		return p.askText(q) // path is just text input
	default:
		return p.askText(q)
	}
}

// Confirm asks a yes/no question.
func (p *TerminalPrompter) Confirm(message string, defaultVal bool) (bool, error) {
	if !isInteractive() {
		return defaultVal, nil
	}
	var result bool
	err := huh.NewConfirm().
		Title(message).
		Value(&result).
		Affirmative("Yes").
		Negative("No").
		Run()
	if err != nil {
		return false, err
	}
	return result, nil
}

func (p *TerminalPrompter) askBool(q QuestionDef) (any, error) {
	var result bool
	if d, ok := q.Default.(bool); ok {
		result = d
	}

	title := q.Name
	if q.Help != "" {
		title = q.Help
	}

	err := huh.NewConfirm().
		Title(title).
		Description(q.Placeholder).
		Value(&result).
		Run()
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (p *TerminalPrompter) askText(q QuestionDef) (any, error) {
	var result string
	if q.Default != nil {
		result = fmt.Sprintf("%v", q.Default)
	}

	title := q.Name
	if q.Help != "" {
		title = q.Help
	}

	input := huh.NewInput().
		Title(title).
		Placeholder(q.Placeholder).
		Value(&result)

	if err := input.Run(); err != nil {
		return nil, err
	}

	return ParseAnswer(q, result)
}

func (p *TerminalPrompter) askSecret(q QuestionDef) (any, error) {
	var result string
	if q.Default != nil {
		result = fmt.Sprintf("%v", q.Default)
	}

	title := q.Name
	if q.Help != "" {
		title = q.Help
	}

	input := huh.NewInput().
		Title(title).
		EchoMode(huh.EchoModePassword).
		Value(&result)

	if err := input.Run(); err != nil {
		return nil, err
	}
	return result, nil
}

func (p *TerminalPrompter) askChoice(q QuestionDef, currentAnswers map[string]any) (any, error) {
	choices := resolveChoices(q)
	if len(choices) == 0 {
		return p.askText(q)
	}

	title := q.Name
	if q.Help != "" {
		title = q.Help
	}

	if q.Multiselect {
		return p.askMultiSelect(title, choices, q)
	}

	options := make([]huh.Option[string], 0, len(choices))
	for _, ch := range choices {
		options = append(options, huh.NewOption(ch.Label, ch.Value))
	}

	var result string
	if q.Default != nil {
		result = fmt.Sprintf("%v", q.Default)
	}

	err := huh.NewSelect[string]().
		Title(title).
		Options(options...).
		Value(&result).
		Run()
	if err != nil {
		return nil, err
	}

	return ParseAnswer(q, result)
}

func (p *TerminalPrompter) askMultiSelect(title string, choices []choiceEntry, q QuestionDef) (any, error) {
	options := make([]huh.Option[string], 0, len(choices))
	for _, ch := range choices {
		options = append(options, huh.NewOption(ch.Label, ch.Value))
	}

	var results []string
	err := huh.NewMultiSelect[string]().
		Title(title).
		Options(options...).
		Value(&results).
		Run()
	if err != nil {
		return nil, err
	}

	// Convert string results to the appropriate types.
	parsed := make([]any, 0, len(results))
	for _, r := range results {
		v, parseErr := ParseAnswer(q, r)
		if parseErr != nil {
			return nil, parseErr
		}
		parsed = append(parsed, v)
	}
	return parsed, nil
}

type choiceEntry struct {
	Label string
	Value string
}

func resolveChoices(q QuestionDef) []choiceEntry {
	switch v := q.Choices.(type) {
	case []any:
		entries := make([]choiceEntry, 0, len(v))
		for _, item := range v {
			s := fmt.Sprintf("%v", item)
			entries = append(entries, choiceEntry{Label: s, Value: s})
		}
		return entries
	case map[string]any:
		entries := make([]choiceEntry, 0, len(v))
		for label, val := range v {
			entries = append(entries, choiceEntry{Label: label, Value: fmt.Sprintf("%v", val)})
		}
		return entries
	case string:
		// Jinja-rendered choices — split by newlines.
		lines := strings.Split(v, "\n")
		entries := make([]choiceEntry, 0, len(lines))
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				entries = append(entries, choiceEntry{Label: line, Value: line})
			}
		}
		return entries
	}
	return nil
}

func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
