package copier

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/flosch/pongo2/v6"
)

// Renderer handles Jinja2-compatible template rendering using pongo2.
type Renderer struct {
	baseCtx pongo2.Context
	tplSet  *pongo2.TemplateSet
	loader  pongo2.TemplateLoader
}

// NewRenderer creates a Renderer with the given base context and optional template directory.
func NewRenderer(baseCtx map[string]any, templateDir string) *Renderer {
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
	return &Renderer{baseCtx: ctx, tplSet: tplSet, loader: loader}
}

// RenderString renders a Jinja2 template string with the given extra context.
func (r *Renderer) RenderString(template string, extra map[string]any) (string, error) {
	tpl, err := r.tplSet.FromString(template)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}
	ctx := r.mergedContext(extra)
	out, err := tpl.Execute(ctx)
	if err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}
	return out, nil
}

// RenderFile renders a Jinja2 template file to the destination path.
func (r *Renderer) RenderFile(srcPath, dstPath string, extra map[string]any) error {
	content, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("reading template %s: %w", srcPath, err)
	}

	tpl, err := r.tplSet.FromString(string(content))
	if err != nil {
		return fmt.Errorf("parsing template %s: %w", srcPath, err)
	}

	ctx := r.mergedContext(extra)
	result, err := tpl.Execute(ctx)
	if err != nil {
		return fmt.Errorf("rendering template %s: %w", srcPath, err)
	}

	// Preserve source file permissions.
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}

	return os.WriteFile(dstPath, []byte(result), info.Mode().Perm())
}

// RenderPath renders a path string, expanding Jinja2 expressions in path segments.
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
