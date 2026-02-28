package copier

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/flosch/pongo2/v6"
)

// Envops configures template delimiters (mirrors Jinja2's Environment options).
type Envops struct {
	BlockStartString    string `yaml:"block_start_string"`
	BlockEndString      string `yaml:"block_end_string"`
	VariableStartString string `yaml:"variable_start_string"`
	VariableEndString   string `yaml:"variable_end_string"`
	CommentStartString  string `yaml:"comment_start_string"`
	CommentEndString    string `yaml:"comment_end_string"`
}

// DefaultEnvops returns pongo2/Jinja2 standard delimiters.
func DefaultEnvops() Envops {
	return Envops{
		BlockStartString:    "{%",
		BlockEndString:      "%}",
		VariableStartString: "{{",
		VariableEndString:   "}}",
		CommentStartString:  "{#",
		CommentEndString:    "#}",
	}
}

// isCustom reports whether the envops differ from pongo2 defaults.
func (e Envops) isCustom() bool {
	d := DefaultEnvops()
	return e.BlockStartString != d.BlockStartString ||
		e.BlockEndString != d.BlockEndString ||
		e.VariableStartString != d.VariableStartString ||
		e.VariableEndString != d.VariableEndString ||
		e.CommentStartString != d.CommentStartString ||
		e.CommentEndString != d.CommentEndString
}

// Renderer handles Jinja2-compatible template rendering using pongo2.
type Renderer struct {
	baseCtx pongo2.Context
	tplSet  *pongo2.TemplateSet
	envops  Envops
}

// NewRenderer creates a Renderer with the given base context, template directory,
// and optional custom delimiters.
func NewRenderer(baseCtx map[string]any, templateDir string, envops ...Envops) *Renderer {
	var loader pongo2.TemplateLoader
	if templateDir != "" {
		loader = pongo2.MustNewLocalFileSystemLoader(templateDir)
	} else {
		loader = pongo2.MustNewLocalFileSystemLoader("")
	}

	tplSet := pongo2.NewSet("copier", loader)
	tplSet.Debug = false

	ctx := make(pongo2.Context, len(baseCtx))
	for k, v := range baseCtx {
		ctx[k] = v
	}

	eo := DefaultEnvops()
	if len(envops) > 0 {
		eo = envops[0]
	}

	return &Renderer{baseCtx: ctx, tplSet: tplSet, envops: eo}
}

// RenderString renders a template string with the given extra context.
// If custom envops are configured, delimiters are translated before parsing.
func (r *Renderer) RenderString(template string, extra map[string]any) (string, error) {
	template = r.toStandard(template)
	tpl, err := r.tplSet.FromString(template)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}
	ctx := r.mergedContext(extra)
	out, err := tpl.Execute(ctx)
	if err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}
	return r.fromStandard(out), nil
}

// RenderFile renders a template file to the destination path.
func (r *Renderer) RenderFile(srcPath, dstPath string, extra map[string]any) error {
	content, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("reading template %s: %w", srcPath, err)
	}

	translated := r.toStandard(string(content))
	tpl, err := r.tplSet.FromString(translated)
	if err != nil {
		return fmt.Errorf("parsing template %s: %w", srcPath, err)
	}

	ctx := r.mergedContext(extra)
	result, err := tpl.Execute(ctx)
	if err != nil {
		return fmt.Errorf("rendering template %s: %w", srcPath, err)
	}

	// Restore any protected standard delimiters in the output.
	result = r.fromStandard(result)

	// Preserve source file permissions.
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}

	return os.WriteFile(dstPath, []byte(result), info.Mode().Perm())
}

// RenderPath renders a path string, expanding template expressions in path segments.
// Returns all expanded paths (multiple if yield tags are used).
func (r *Renderer) RenderPath(pathTemplate string, extra map[string]any) ([]string, error) {
	parts := strings.Split(pathTemplate, string(filepath.Separator))
	return r.renderPathParts(parts, extra)
}

func (r *Renderer) renderPathParts(parts []string, extra map[string]any) ([]string, error) {
	if len(parts) == 0 {
		return []string{""}, nil
	}

	rendered, err := r.RenderString(parts[0], extra)
	if err != nil {
		return nil, err
	}

	if rendered == "" {
		return nil, nil // path segment rendered to empty → skip
	}

	rest, err := r.renderPathParts(parts[1:], extra)
	if err != nil {
		return nil, err
	}

	result := make([]string, 0, len(rest))
	for _, suffix := range rest {
		if suffix == "" {
			result = append(result, rendered)
		} else {
			result = append(result, filepath.Join(rendered, suffix))
		}
	}
	return result, nil
}

// Unicode Private Use Area placeholders for protecting standard delimiters
// when custom envops are in use.
const (
	phBlockStart = "\U000F0001"
	phBlockEnd   = "\U000F0002"
	phVarStart   = "\U000F0003"
	phVarEnd     = "\U000F0004"
	phComStart   = "\U000F0005"
	phComEnd     = "\U000F0006"
)

// toStandard translates a template string from custom delimiters to pongo2 standard.
// Standard delimiters already present in the source (e.g. Django tags) are
// replaced with PUA placeholders so pongo2 does not interpret them.
func (r *Renderer) toStandard(s string) string {
	if !r.envops.isCustom() {
		return s
	}
	std := DefaultEnvops()

	// Step 1: Protect existing standard delimiters (they are NOT copier tags).
	s = strings.ReplaceAll(s, std.BlockStartString, phBlockStart)
	s = strings.ReplaceAll(s, std.BlockEndString, phBlockEnd)
	s = strings.ReplaceAll(s, std.VariableStartString, phVarStart)
	s = strings.ReplaceAll(s, std.VariableEndString, phVarEnd)
	s = strings.ReplaceAll(s, std.CommentStartString, phComStart)
	s = strings.ReplaceAll(s, std.CommentEndString, phComEnd)

	// Step 2: Convert custom delimiters to standard pongo2 ones.
	s = strings.ReplaceAll(s, r.envops.BlockStartString, std.BlockStartString)
	s = strings.ReplaceAll(s, r.envops.BlockEndString, std.BlockEndString)
	s = strings.ReplaceAll(s, r.envops.VariableStartString, std.VariableStartString)
	s = strings.ReplaceAll(s, r.envops.VariableEndString, std.VariableEndString)
	s = strings.ReplaceAll(s, r.envops.CommentStartString, std.CommentStartString)
	s = strings.ReplaceAll(s, r.envops.CommentEndString, std.CommentEndString)

	return s
}

// fromStandard restores placeholders back to the original standard delimiters
// in the rendered output. These are Django/framework tags that should appear
// literally in the generated files.
func (r *Renderer) fromStandard(s string) string {
	if !r.envops.isCustom() {
		return s
	}
	std := DefaultEnvops()

	s = strings.ReplaceAll(s, phBlockStart, std.BlockStartString)
	s = strings.ReplaceAll(s, phBlockEnd, std.BlockEndString)
	s = strings.ReplaceAll(s, phVarStart, std.VariableStartString)
	s = strings.ReplaceAll(s, phVarEnd, std.VariableEndString)
	s = strings.ReplaceAll(s, phComStart, std.CommentStartString)
	s = strings.ReplaceAll(s, phComEnd, std.CommentEndString)

	return s
}

// mergedContext returns a new context combining the base context with extras.
func (r *Renderer) mergedContext(extra map[string]any) pongo2.Context {
	ctx := make(pongo2.Context, len(r.baseCtx)+len(extra))
	for k, v := range r.baseCtx {
		ctx[k] = v
	}
	for k, v := range extra {
		ctx[k] = v
	}
	return ctx
}

// IsBinary performs a simple heuristic to detect binary files by checking
// the first 8KB for null bytes.
func IsBinary(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	buf := make([]byte, 8192)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}
	for _, b := range buf[:n] {
		if b == 0 {
			return true, nil
		}
	}
	return false, nil
}

// IsTemplateSuffix reports whether the file path ends with the template suffix.
func IsTemplateSuffix(path, suffix string) bool {
	return strings.HasSuffix(path, suffix)
}

// StripTemplateSuffix removes the template suffix from a path.
func StripTemplateSuffix(path, suffix string) string {
	return strings.TrimSuffix(path, suffix)
}
