package textutil

import "testing"

func TestEnsureSuffix(t *testing.T) {
	if got := EnsureSuffix("hello", "\n"); got != "hello\n" {
		t.Fatalf("expected append, got %q", got)
	}
	if got := EnsureSuffix("hello\n", "\n"); got != "hello\n" {
		t.Fatalf("expected no-op, got %q", got)
	}
}

func TestToBool(t *testing.T) {
	tests := []struct {
		input string
		want  bool
		ok    bool
	}{
		{"true", true, true},
		{"True", true, true},
		{"yes", true, true},
		{"YES", true, true},
		{"on", true, true},
		{"1", true, true},
		{"false", false, true},
		{"no", false, true},
		{"off", false, true},
		{"0", false, true},
		{"", false, true},
		{"maybe", false, false},
	}
	for _, tt := range tests {
		val, ok := ToBool(tt.input)
		if ok != tt.ok || val != tt.want {
			t.Errorf("ToBool(%q) = (%v, %v), want (%v, %v)", tt.input, val, ok, tt.want, tt.ok)
		}
	}
}

func TestIsBlank(t *testing.T) {
	if !IsBlank("") {
		t.Error("empty should be blank")
	}
	if !IsBlank("   \t\n") {
		t.Error("whitespace should be blank")
	}
	if IsBlank("hello") {
		t.Error("text should not be blank")
	}
}
