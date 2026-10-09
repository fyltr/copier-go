package copier

import (
	"errors"
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

func TestRenderer_DoesNotHTMLEscapeVariables(t *testing.T) {
	// Copier renders config/source (YAML, TOML, code), not HTML, so variable output must
	// not be HTML-escaped — pongo2 defaults autoescape on, Jinja2/Copier default it off. A
	// rendered `"` here would otherwise become `&quot;`, corrupting e.g. quoted YAML values.
	r := NewRenderer(map[string]any{"v": `"x" & <y>`}, "")

	out, err := r.RenderString("k: {{ v }}", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := `k: "x" & <y>`; out != want {
		t.Fatalf("expected %q (unescaped), got %q", want, out)
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

func TestRenderer_Filters(t *testing.T) {
	r := NewRenderer(map[string]any{
		"d":      map[string]any{"b": 2, "a": []any{1, "x"}},
		"items":  []any{3, 1, 2},
		"nested": []any{1, []any{2, []any{3}}},
		"s":      "  Hello  ",
	}, "")
	cases := map[string]string{
		`{{ d|to_json }}`:               `{"a":[1,"x"],"b":2}`,
		`{{ d|to_nice_yaml }}`:          "a:\n    - 1\n    - x\nb: 2\n",
		`{{ items|sort|join:"," }}`:     "1,2,3",
		`{{ items|max }}`:               "3",
		`{{ s|trim }}`:                  "Hello",
		`{{ "abc"|hash:"sha256" }}`:     "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		`{{ "aGk="|b64decode }}`:        "hi",
		`{{ "3"|int }}`:                 "3",
		`{{ "yes"|bool }}`:              "True",
		`{{ "a/b/c.txt"|basename }}`:    "c.txt",
		`{{ "a/b/c.txt"|dirname }}`:     "a/b",
		`{{ nested|flatten|join:"-" }}`: "1-2-3",
		`{{ d|dict2items|length }}`:     "2",
		`{{ "x y"|quote }}`:             "'x y'",
		`{{ pathjoin("a", "b/c") }}`:    "a/b/c",
	}
	for tpl, want := range cases {
		got, err := r.RenderString(tpl, map[string]any{"pathjoin": pathJoin})
		if err != nil {
			t.Errorf("%s: %v", tpl, err)
			continue
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", tpl, got, want)
		}
	}
	year, err := r.RenderString(`{{ "%Y-%m-%d"|strftime }}`, nil)
	if err != nil || len(year) != 10 {
		t.Errorf("strftime: %q %v", year, err)
	}
}

func TestRenderer_StrictUndefinedAllowsLoopVars(t *testing.T) {
	r := NewRenderer(map[string]any{"items": []any{"a", "b"}}, "", Envops{Undefined: "jinja2.StrictUndefined"})
	out, err := r.RenderString("{% for x in items %}{{ x }}{% endfor %}{% set y = 1 %}{{ y }}", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "ab1" {
		t.Fatalf("unexpected output %q", out)
	}
	if _, err := r.RenderString("{% if missing %}x{% endif %}", nil); err == nil {
		t.Fatal("expected strict undefined error in condition")
	}
}

func TestRenderer_YieldPathParts(t *testing.T) {
	r := NewRenderer(nil, "")
	paths, err := r.RenderPath("{% yield n from names %}{{ n }}{% endyield %}/file.txt", map[string]any{"names": []any{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != filepath.Join("a", "file.txt") || paths[1] != filepath.Join("b", "file.txt") {
		t.Fatalf("unexpected yield paths: %v", paths)
	}
	_, _, err = r.RenderStringYield("{% yield a from x %}{% endyield %}{% yield b from x %}{% endyield %}", map[string]any{"x": []any{1}})
	if !errors.Is(err, ErrMultipleYields) {
		t.Fatalf("expected ErrMultipleYields, got %v", err)
	}
}

func TestRenderer_SkipsNonIdentifierKeys(t *testing.T) {
	r := NewRenderer(map[string]any{"my-var": 1, "ok": "fine"}, "")
	out, err := r.RenderString("{{ ok }}", map[string]any{"other-key": true})
	if err != nil {
		t.Fatal(err)
	}
	if out != "fine" {
		t.Fatalf("unexpected output %q", out)
	}
}

func TestRenderer_MultilineComments(t *testing.T) {
	r := NewRenderer(map[string]any{"x": "v"}, "")
	tpl := "a\n{# first line\n   second line #}\nb {{ x }}{#- trimmed -#}   c\n{% raw %}{# kept #} ${{ x }} {% for %}{% endraw %}"
	out, err := r.RenderString(tpl, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "a\n\nb vc\n{# kept #} ${{ x }} {% for %}"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	// Raw blocks with custom delimiters keep their content verbatim.
	rc := NewRenderer(map[string]any{"x": "v"}, "", Envops{BlockStartString: "<%", BlockEndString: "%>", VariableStartString: "<<", VariableEndString: ">>"})
	out, err = rc.RenderString("<< x >>|<% raw %><< x >> {{ y }}<% endraw %>", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "v|<< x >> {{ y }}"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestRenderer_LoopAliases(t *testing.T) {
	r := NewRenderer(map[string]any{"items": []any{"a", "b", "c"}}, "")
	out, err := r.RenderString("{% for i in items %}{{ loop.index }}{{ i }}{% if loop.last %}!{% endif %}{% endfor %}", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "1a2b3c!" {
		t.Fatalf("unexpected output %q", out)
	}
}
