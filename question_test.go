package copier

import "testing"

func TestAnswersMap_Precedence(t *testing.T) {
	a := NewAnswersMap()
	a.UserDefaults["name"] = "from-defaults"
	a.Last["name"] = "from-last"
	a.Init["name"] = "from-init"
	a.User["name"] = "from-user"

	v, ok := a.Get("name")
	if !ok {
		t.Fatal("expected value")
	}
	if v != "from-user" {
		t.Fatalf("expected from-user, got %v", v)
	}
}

func TestAnswersMap_Fallthrough(t *testing.T) {
	a := NewAnswersMap()
	a.Last["name"] = "from-last"

	v, ok := a.Get("name")
	if !ok || v != "from-last" {
		t.Fatalf("expected from-last, got %v", v)
	}
}

func TestAnswersMap_Remembered(t *testing.T) {
	a := NewAnswersMap()
	a.User["name"] = "test"
	a.User["_internal"] = "should-skip"
	a.Hidden["secret"] = true
	a.User["secret"] = "s3cret"

	r := a.Remembered()
	if _, ok := r["_internal"]; ok {
		t.Fatal("internal keys should be excluded")
	}
	if _, ok := r["secret"]; ok {
		t.Fatal("hidden keys should be excluded")
	}
	if r["name"] != "test" {
		t.Fatalf("expected test, got %v", r["name"])
	}
}

func TestParseBool(t *testing.T) {
	tests := []struct {
		input any
		want  bool
	}{
		{true, true},
		{false, false},
		{"yes", true},
		{"no", false},
		{"true", true},
		{"false", false},
		{"on", true},
		{"off", false},
		{"1", true},
		{"0", false},
		{1, true},
		{0, false},
	}
	for _, tt := range tests {
		got, err := parseBool(tt.input)
		if err != nil {
			t.Fatalf("parseBool(%v) error: %v", tt.input, err)
		}
		if got != tt.want {
			t.Fatalf("parseBool(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		input any
		want  int64
	}{
		{42, 42},
		{int64(99), 99},
		{3.14, 3},
		{"123", 123},
	}
	for _, tt := range tests {
		got, err := parseInt(tt.input)
		if err != nil {
			t.Fatalf("parseInt(%v) error: %v", tt.input, err)
		}
		if got != tt.want {
			t.Fatalf("parseInt(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestResolveDefault(t *testing.T) {
	q := QuestionDef{Name: "color", Default: "blue"}
	a := NewAnswersMap()
	a.Init["color"] = "red"

	got := ResolveDefault(q, a, nil)
	if got != "red" {
		t.Fatalf("expected red (from init), got %v", got)
	}

	a2 := NewAnswersMap()
	got2 := ResolveDefault(q, a2, nil)
	if got2 != "blue" {
		t.Fatalf("expected blue (from question default), got %v", got2)
	}
}
