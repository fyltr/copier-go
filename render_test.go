package copier

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRenderer_RenderString(t *testing.T) {
	r := NewRenderer(map[string]any{"name": "world"}, "")

	out, err := r.RenderString("Hello {{ name }}!", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Hello world!" {
		t.Fatalf("expected 'Hello world!', got %q", out)
	}
}

func TestRenderer_RenderString_WithExtra(t *testing.T) {
	r := NewRenderer(map[string]any{"base": "x"}, "")

	out, err := r.RenderString("{{ base }}-{{ extra }}", map[string]any{"extra": "y"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "x-y" {
		t.Fatalf("expected 'x-y', got %q", out)
	}
}

func TestRenderer_StrictUndefined(t *testing.T) {
	r := NewRenderer(map[string]any{}, "", Envops{Undefined: "jinja2.StrictUndefined"})

	_, err := r.RenderString("{{ missing }}", nil)
	if err == nil {
		t.Fatal("expected strict undefined error")
	}
}

func TestRenderer_RenderFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "template.txt.jinja")
	dst := filepath.Join(dir, "output.txt")

	mustWriteFile(t, src, []byte("Project: {{ project_name }}"), 0o644)

	r := NewRenderer(map[string]any{"project_name": "myapp"}, dir)
	err := r.RenderFile(src, dst, nil)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "Project: myapp" {
		t.Fatalf("expected 'Project: myapp', got %q", string(data))
	}
}

func TestIsBinary(t *testing.T) {
	dir := t.TempDir()

	text := filepath.Join(dir, "text.txt")
	mustWriteFile(t, text, []byte("hello world"), 0o644)
	bin, err := IsBinary(text)
	if err != nil {
		t.Fatal(err)
	}
	if bin {
		t.Error("text file should not be binary")
	}

	binFile := filepath.Join(dir, "binary.bin")
	mustWriteFile(t, binFile, []byte{0x00, 0x01, 0x02}, 0o644)
	bin, err = IsBinary(binFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bin {
		t.Error("file with null bytes should be binary")
	}
}

func TestIsTemplateSuffix(t *testing.T) {
	if !IsTemplateSuffix("file.txt.jinja", ".jinja") {
		t.Error("expected true for .jinja suffix")
	}
	if IsTemplateSuffix("file.txt", ".jinja") {
		t.Error("expected false without .jinja suffix")
	}
}

func TestStripTemplateSuffix(t *testing.T) {
	got := StripTemplateSuffix("file.txt.jinja", ".jinja")
	if got != "file.txt" {
		t.Fatalf("expected file.txt, got %s", got)
	}
}

func TestRenderer_RenderPath(t *testing.T) {
	r := NewRenderer(map[string]any{"dir": "src", "name": "main"}, "")
	paths, err := r.RenderPath("{{ dir }}/{{ name }}.go", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != filepath.Join("src", "main.go") {
		t.Fatalf("expected [src/main.go], got %v", paths)
	}
}
