package copier

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/flosch/pongo2/v6"
	"github.com/fyltr/copier-go/internal/pathutil"
)

// Copier renders configuration and source files (YAML, TOML, code), not HTML, so — like
// Python Copier / Jinja2 — template variable output must NOT be HTML-escaped. pongo2
// defaults autoescape ON, which turns a rendered `"` into `&quot;` (and `<`, `>`, `&`),
// corrupting non-HTML output (e.g. a quoted YAML value renders unparseable). Disable it
// globally to match Jinja2 semantics; templates that genuinely emit HTML can re-enable it
// with `{% autoescape on %}`.
func init() {
	pongo2.SetAutoescape(false)
	_ = pongo2.RegisterTag("yield", tagYieldParser)
}

// Envops configures template delimiters (mirrors Jinja2's Environment options).
type Envops struct {
	BlockStartString    string `yaml:"block_start_string"`
	BlockEndString      string `yaml:"block_end_string"`
	VariableStartString string `yaml:"variable_start_string"`
	VariableEndString   string `yaml:"variable_end_string"`
	CommentStartString  string `yaml:"comment_start_string"`
	CommentEndString    string `yaml:"comment_end_string"`
	Undefined           string `yaml:"undefined"`
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

func fillEnvopsDefaults(eo Envops) Envops {
	d := DefaultEnvops()
	if eo.BlockStartString == "" {
		eo.BlockStartString = d.BlockStartString
	}
	if eo.BlockEndString == "" {
		eo.BlockEndString = d.BlockEndString
	}
	if eo.VariableStartString == "" {
		eo.VariableStartString = d.VariableStartString
	}
	if eo.VariableEndString == "" {
		eo.VariableEndString = d.VariableEndString
	}
	if eo.CommentStartString == "" {
		eo.CommentStartString = d.CommentStartString
	}
	if eo.CommentEndString == "" {
		eo.CommentEndString = d.CommentEndString
	}
	return eo
}

// yieldStateKey is the render-context key carrying the yield state of one render.
const yieldStateKey = "__copier_yield__"

// yieldState records the `{% yield %}` tag found while rendering a path part.
type yieldState struct {
	Name     string
	Iterable []any
	set      bool
}

// Renderer handles Jinja2-compatible template rendering using pongo2.
//
// `{% include %}` lookups are restricted to the template root: paths that
// resolve (through `..` or symlinks) outside of it are rejected.
type Renderer struct {
	baseCtx         map[string]any
	tplSet          *pongo2.TemplateSet
	loader          *sandboxLoader
	envops          Envops
	strictUndefined bool
	root            string
}

// renderError carries pongo2's detailed message while unwrapping to the
// original cause, so callers can use errors.Is.
type renderError struct {
	msg   string
	cause error
}

func (e *renderError) Error() string { return e.msg }
func (e *renderError) Unwrap() error { return e.cause }

// wrapRenderError converts a pongo2 error into an unwrappable error.
func (r *Renderer) wrapRenderError(prefix string, err error) error {
	cause := err
	var perr *pongo2.Error
	if errors.As(err, &perr) {
		if r.loader != nil && r.loader.lastErr != nil {
			cause = r.loader.lastErr
		} else if perr.OrigError != nil {
			cause = perr.OrigError
		}
	}
	return &renderError{msg: prefix + ": " + err.Error(), cause: cause}
}

// sandboxLoader is a pongo2 loader confined to the template root, or to the
// wider include root set with WithIncludeRoot. Relative names always resolve
// against the template root.
type sandboxLoader struct {
	base    string // Template root, which relative names resolve against.
	root    string // Sandbox boundary: the template root or the include root.
	lastErr error  // Last forbidden-path error, surfaced when pongo2 swallows it.
}

func (l *sandboxLoader) Abs(base, name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	if base != "" {
		return filepath.Join(filepath.Dir(base), name)
	}
	return name
}

func (l *sandboxLoader) Get(name string) (io.Reader, error) {
	if l.base == "" {
		return nil, fmt.Errorf("%w: template includes are not available without a template root", ErrForbiddenPath)
	}
	target := name
	if !filepath.IsAbs(target) {
		target = filepath.Join(l.base, filepath.FromSlash(name))
	}
	ok, err := pathutil.IsWithin(l.root, target)
	if err != nil {
		return nil, err
	}
	if !ok {
		boundary := "template root"
		if l.root != l.base {
			boundary = "include root"
		}
		l.lastErr = fmt.Errorf("%w: %s is outside the %s", ErrForbiddenPath, name, boundary)
		return nil, l.lastErr
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

// NewRenderer creates a Renderer with the given base context, template root
// directory (used for `{% include %}`), and optional custom delimiters.
func NewRenderer(baseCtx map[string]any, templateRoot string, envops ...Envops) *Renderer {
	root := templateRoot
	if root != "" {
		if resolved, err := pathutil.Resolve(root); err == nil {
			root = resolved
		}
	}
	loader := &sandboxLoader{base: root, root: root}
	tplSet := pongo2.NewSet("copier", loader)
	tplSet.Debug = false

	ctx := make(map[string]any, len(baseCtx))
	for k, v := range baseCtx {
		ctx[k] = v
	}

	eo := DefaultEnvops()
	if len(envops) > 0 {
		eo = envops[0]
	}
	eo = fillEnvopsDefaults(eo)

	return &Renderer{
		baseCtx:         ctx,
		tplSet:          tplSet,
		loader:          loader,
		envops:          eo,
		strictUndefined: eo.Undefined == "jinja2.StrictUndefined",
		root:            root,
	}
}

// setIncludeRoot widens the include sandbox to dir, which must contain the
// template root. Relative include names still resolve against the template root.
func (r *Renderer) setIncludeRoot(dir string) error {
	if r.loader.base == "" {
		return fmt.Errorf("%w: an include root requires a template root", ErrForbiddenPath)
	}
	root, err := pathutil.Resolve(dir)
	if err != nil {
		return err
	}
	ok, err := pathutil.IsWithin(root, r.loader.base)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: include root %s does not contain the template root %s", ErrForbiddenPath, dir, r.loader.base)
	}
	r.loader.root = root
	return nil
}

// RenderString renders a template string with the given extra context.
// If custom envops are configured, delimiters are translated before parsing.
func (r *Renderer) RenderString(template string, extra map[string]any) (string, error) {
	out, _, err := r.render(template, extra)
	return out, err
}

// RenderStringYield renders a template string and also reports the yield tag
// state, when the template used `{% yield %}`.
func (r *Renderer) RenderStringYield(template string, extra map[string]any) (string, *yieldState, error) {
	return r.render(template, extra)
}

func (r *Renderer) render(template string, extra map[string]any) (string, *yieldState, error) {
	template, rawBlocks := r.protectRawBlocks(template)
	template = aliasLoopVariables(stripJinjaComments(r.toStandard(template)))
	ctx := r.mergedContext(extra)
	state := &yieldState{}
	ctx[yieldStateKey] = state
	if err := r.checkUndefined(template, ctx); err != nil {
		return "", nil, err
	}
	r.loader.lastErr = nil
	tpl, err := r.tplSet.FromString(template)
	if err != nil {
		return "", nil, r.wrapRenderError("parsing template", err)
	}
	out, err := tpl.Execute(ctx)
	if err != nil {
		return "", nil, r.wrapRenderError("executing template", err)
	}
	return restoreRawBlocks(r.fromStandard(out), rawBlocks), state, nil
}

// rawPlaceholder wraps the index of a protected `{% raw %}` block.
const (
	rawPlaceholderStart = "\U000F0100"
	rawPlaceholderEnd   = "\U000F0101"
)

// protectRawBlocks replaces `{% raw %}...{% endraw %}` blocks (using the
// configured block delimiters) with placeholders so their content is emitted
// verbatim, like Jinja2 does. pongo2 has no raw tag of its own.
func (r *Renderer) protectRawBlocks(s string) (string, []string) {
	bs, be := regexp.QuoteMeta(r.envops.BlockStartString), regexp.QuoteMeta(r.envops.BlockEndString)
	if !strings.Contains(s, r.envops.BlockStartString) {
		return s, nil
	}
	re := regexp.MustCompile(`(?s)` + bs + `(-?)\s*raw\s*(-?)` + be + `(.*?)` + bs + `(-?)\s*endraw\s*(-?)` + be)
	matches := re.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return s, nil
	}
	var b strings.Builder
	var blocks []string
	pos := 0
	trimNext := false
	for _, m := range matches {
		before := s[pos:m[0]]
		if trimNext {
			before = strings.TrimLeft(before, " \t\r\n")
		}
		if s[m[2]:m[3]] == "-" {
			before = strings.TrimRight(before, " \t\r\n")
		}
		content := s[m[6]:m[7]]
		if s[m[4]:m[5]] == "-" {
			content = strings.TrimLeft(content, " \t\r\n")
		}
		if s[m[8]:m[9]] == "-" {
			content = strings.TrimRight(content, " \t\r\n")
		}
		trimNext = s[m[10]:m[11]] == "-"
		b.WriteString(before)
		fmt.Fprintf(&b, "%s%d%s", rawPlaceholderStart, len(blocks), rawPlaceholderEnd)
		blocks = append(blocks, content)
		pos = m[1]
	}
	rest := s[pos:]
	if trimNext {
		rest = strings.TrimLeft(rest, " \t\r\n")
	}
	b.WriteString(rest)
	return b.String(), blocks
}

func restoreRawBlocks(s string, blocks []string) string {
	for i, content := range blocks {
		s = strings.ReplaceAll(s, fmt.Sprintf("%s%d%s", rawPlaceholderStart, i, rawPlaceholderEnd), content)
	}
	return s
}

var (
	tagExprRe   = regexp.MustCompile(`(?s)\{\{.*?\}\}|\{%.*?%\}`)
	loopAliasRe = regexp.MustCompile(`\bloop\.(index0|index|first|last|revindex0|revindex)\b`)
	loopAliases = map[string]string{
		"index": "forloop.Counter", "index0": "forloop.Counter0", "first": "forloop.First",
		"last": "forloop.Last", "revindex": "forloop.Revcounter", "revindex0": "forloop.Revcounter0",
	}
)

// aliasLoopVariables maps Jinja's `loop.*` variables inside expressions to
// pongo2's `forloop.*` equivalents.
func aliasLoopVariables(s string) string {
	if !strings.Contains(s, "loop.") {
		return s
	}
	return tagExprRe.ReplaceAllStringFunc(s, func(expr string) string {
		return loopAliasRe.ReplaceAllStringFunc(expr, func(m string) string {
			return loopAliases[strings.TrimPrefix(m, "loop.")]
		})
	})
}

// RenderFile renders a template file to the destination path.
func (r *Renderer) RenderFile(srcPath, dstPath string, extra map[string]any) error {
	content, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("reading template %s: %w", srcPath, err)
	}
	result, state, err := r.render(string(content), extra)
	if err != nil {
		return fmt.Errorf("rendering template %s: %w", srcPath, err)
	}
	if state.set {
		return fmt.Errorf("%w: %s", ErrYieldInFile, srcPath)
	}

	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if err := os.WriteFile(dstPath, []byte(result), mode); err != nil {
		return err
	}
	return os.Chmod(dstPath, mode)
}

// RenderedPath is one rendered destination path with the extra context that
// produced it (yield loop variables).
type RenderedPath struct {
	Path    string
	Context map[string]any
}

// RenderPath renders a path string, expanding template expressions in path
// segments. Returns all expanded paths (multiple if yield tags are used).
func (r *Renderer) RenderPath(pathTemplate string, extra map[string]any) ([]string, error) {
	rendered, err := r.RenderPathParts(strings.Split(filepath.ToSlash(pathTemplate), "/"), extra, nil)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rendered))
	for _, rp := range rendered {
		out = append(out, rp.Path)
	}
	return out, nil
}

// RenderPathParts renders path segments recursively. specialCase, when given,
// may short-circuit a rendered segment (used for the answers file path).
func (r *Renderer) RenderPathParts(parts []string, extra map[string]any, specialCase func(rendered string, renderedSoFar []string) (string, bool)) ([]RenderedPath, error) {
	return r.renderPathParts(parts, nil, extra, nil, specialCase)
}

func (r *Renderer) renderPathParts(parts, renderedParts []string, extra map[string]any, sourceParts []string, specialCase func(string, []string) (string, bool)) ([]RenderedPath, error) {
	if len(parts) == 0 {
		return []RenderedPath{{Path: filepath.Join(renderedParts...), Context: extra}}, nil
	}
	part, rest := parts[0], parts[1:]
	sourceParts = append(append([]string(nil), sourceParts...), part)
	sourcePath := path.Join(sourceParts...)

	rendered, state, err := r.render(part, extra)
	if err != nil {
		return nil, fmt.Errorf("error rendering template path %s: %w", sourcePath, err)
	}

	if state.set {
		var out []RenderedPath
		for _, value := range state.Iterable {
			newCtx := make(map[string]any, len(extra)+1)
			for k, v := range extra {
				newCtx[k] = v
			}
			newCtx[state.Name] = value
			renderedItem, _, err := r.render(part, newCtx)
			if err != nil {
				return nil, fmt.Errorf("error rendering template path %s: %w", sourcePath, err)
			}
			if specialCase != nil {
				if full, ok := specialCase(renderedItem, renderedParts); ok {
					out = append(out, RenderedPath{Path: full, Context: newCtx})
					continue
				}
			}
			if renderedItem == "" {
				continue
			}
			sub, err := r.renderPathParts(rest, append(append([]string(nil), renderedParts...), renderedItem), newCtx, sourceParts, specialCase)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
		}
		return out, nil
	}

	if specialCase != nil {
		if full, ok := specialCase(rendered, renderedParts); ok {
			return []RenderedPath{{Path: full, Context: extra}}, nil
		}
	}
	if rendered == "" {
		return nil, nil
	}
	return r.renderPathParts(rest, append(append([]string(nil), renderedParts...), rendered), extra, sourceParts, specialCase)
}

var jinjaCommentRe = regexp.MustCompile(`(?s)\{#(-?)(.*?)(-?)#\}`)

// stripJinjaComments removes `{# ... #}` comments the way Jinja2 does,
// including multi-line ones (which pongo2's lexer rejects) and the `{#-` /
// `-#}` whitespace control markers. Raw blocks are protected beforehand.
func stripJinjaComments(s string) string {
	if !strings.Contains(s, "{#") {
		return s
	}
	matches := jinjaCommentRe.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return s
	}
	var b strings.Builder
	pos := 0
	trimNext := false
	for _, m := range matches {
		before := s[pos:m[0]]
		if trimNext {
			before = strings.TrimLeft(before, " \t\r\n")
		}
		if s[m[2]:m[3]] == "-" {
			before = strings.TrimRight(before, " \t\r\n")
		}
		b.WriteString(before)
		trimNext = s[m[6]:m[7]] == "-"
		pos = m[1]
	}
	rest := s[pos:]
	if trimNext {
		rest = strings.TrimLeft(rest, " \t\r\n")
	}
	b.WriteString(rest)
	return b.String()
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

// identifierRe matches the context keys pongo2 accepts; other keys (e.g.
// `my-var`) cannot be referenced from templates and are left out.
var identifierRe = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// mergedContext returns a new context combining the base context with extras.
func (r *Renderer) mergedContext(extra map[string]any) pongo2.Context {
	ctx := make(pongo2.Context, len(r.baseCtx)+len(extra)+1)
	for k, v := range r.baseCtx {
		if identifierRe.MatchString(k) {
			ctx[k] = v
		}
	}
	for k, v := range extra {
		if identifierRe.MatchString(k) {
			ctx[k] = v
		}
	}
	return ctx
}

var (
	variableTagRe  = regexp.MustCompile(`(?s)\{\{\s*(?:not\s+)?([A-Za-z_][A-Za-z0-9_]*)`)
	conditionTagRe = regexp.MustCompile(`(?s)\{%-?\s*(?:if|elif)\s+(?:not\s+)?([A-Za-z_][A-Za-z0-9_]*)`)
	boundNamesRes  = []*regexp.Regexp{
		regexp.MustCompile(`(?s)\{%-?\s*for\s+([A-Za-z_][A-Za-z0-9_]*(?:\s*,\s*[A-Za-z_][A-Za-z0-9_]*)*)\s+in\b`),
		regexp.MustCompile(`(?s)\{%-?\s*set\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexp.MustCompile(`(?s)\{%-?\s*yield\s+([A-Za-z_][A-Za-z0-9_]*)\s+from\b`),
		regexp.MustCompile(`(?s)\{%-?\s*macro\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexp.MustCompile(`(?s)\{%-?\s*with\s+(.*?)-?%\}`),
	}
	pongoLiterals = map[string]bool{
		"true": true, "false": true, "nil": true, "none": true, "None": true, "True": true, "False": true,
		"loop": true, "forloop": true, "not": true, "and": true, "or": true, "in": true,
	}
)

// checkUndefined approximates Jinja's StrictUndefined: any top-level variable
// referenced in an expression must exist in the context.
func (r *Renderer) checkUndefined(template string, ctx pongo2.Context) error {
	if !r.strictUndefined {
		return nil
	}
	bound := make(map[string]bool)
	for _, re := range boundNamesRes {
		for _, m := range re.FindAllStringSubmatch(template, -1) {
			for _, part := range strings.FieldsFunc(m[1], func(r rune) bool { return r == ',' || r == ' ' || r == '=' || r == '\t' || r == '\n' }) {
				bound[part] = true
			}
		}
	}
	check := func(name string) error {
		if pongoLiterals[name] || bound[name] {
			return nil
		}
		if _, ok := ctx[name]; !ok {
			return fmt.Errorf("'%s' is undefined", name)
		}
		return nil
	}
	for _, match := range variableTagRe.FindAllStringSubmatch(template, -1) {
		if err := check(match[1]); err != nil {
			return err
		}
	}
	for _, match := range conditionTagRe.FindAllStringSubmatch(template, -1) {
		if err := check(match[1]); err != nil {
			return err
		}
	}
	return nil
}

// --- yield tag ------------------------------------------------------------

type tagYieldNode struct {
	name     string
	iterable pongo2.IEvaluator
	wrapper  *pongo2.NodeWrapper
}

func (node *tagYieldNode) Execute(ctx *pongo2.ExecutionContext, writer pongo2.TemplateWriter) *pongo2.Error {
	state, _ := ctx.Public[yieldStateKey].(*yieldState)
	if state == nil {
		return ctx.Error("yield tag is not available in this context", nil)
	}
	if state.set {
		return ctx.OrigError(fmt.Errorf("%w: a yield tag with the name %q already exists", ErrMultipleYields, state.Name), nil)
	}
	val, err := node.iterable.Evaluate(ctx)
	if err != nil {
		return err
	}
	state.set = true
	state.Name = node.name
	state.Iterable = iterableValues(val.Interface())
	child := pongo2.NewChildExecutionContext(ctx)
	return node.wrapper.Execute(child, writer)
}

func iterableValues(v any) []any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = rv.Index(i).Interface()
		}
		return out
	case reflect.Map:
		keys := rv.MapKeys()
		strs := make([]string, 0, len(keys))
		byStr := make(map[string]any, len(keys))
		for _, k := range keys {
			s := fmt.Sprintf("%v", k.Interface())
			strs = append(strs, s)
			byStr[s] = k.Interface()
		}
		sort.Strings(strs)
		out := make([]any, 0, len(strs))
		for _, s := range strs {
			out = append(out, byStr[s])
		}
		return out
	case reflect.String:
		s := rv.String()
		out := make([]any, 0, len(s))
		for _, r := range s {
			out = append(out, string(r))
		}
		return out
	}
	return []any{v}
}

func tagYieldParser(doc *pongo2.Parser, start *pongo2.Token, arguments *pongo2.Parser) (pongo2.INodeTag, *pongo2.Error) {
	node := &tagYieldNode{}
	nameToken := arguments.MatchType(pongo2.TokenIdentifier)
	if nameToken == nil {
		return nil, arguments.Error("Expected an identifier after 'yield'.", nil)
	}
	node.name = nameToken.Val
	if arguments.Match(pongo2.TokenIdentifier, "from") == nil && arguments.Match(pongo2.TokenKeyword, "from") == nil {
		return nil, arguments.Error("Expected 'from' after the yield variable name.", nil)
	}
	iterable, err := arguments.ParseExpression()
	if err != nil {
		return nil, err
	}
	node.iterable = iterable
	if arguments.Remaining() > 0 {
		return nil, arguments.Error("Malformed 'yield'-tag arguments.", nil)
	}
	wrapper, endargs, err := doc.WrapUntilTag("endyield")
	if err != nil {
		return nil, err
	}
	if endargs.Count() > 0 {
		return nil, endargs.Error("Arguments not allowed here.", nil)
	}
	node.wrapper = wrapper
	return node, nil
}

// pathJoin joins path segments with forward slashes (the `pathjoin` global).
func pathJoin(parts ...string) string {
	return path.Join(parts...)
}

// IsBinary performs a simple heuristic to detect binary files by checking
// the first 8KB for null bytes.
func IsBinary(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, 8192)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}
	return bytes.IndexByte(buf[:n], 0) >= 0, nil
}

// IsTemplateSuffix reports whether the file path ends with the template suffix.
func IsTemplateSuffix(path, suffix string) bool {
	return strings.HasSuffix(path, suffix)
}

// StripTemplateSuffix removes the template suffix from a path.
func StripTemplateSuffix(path, suffix string) string {
	return strings.TrimSuffix(path, suffix)
}
