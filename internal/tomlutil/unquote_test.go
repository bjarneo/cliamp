package tomlutil

import "testing"

func TestUnquoteDoubleQuoted(t *testing.T) {
	if got := Unquote(`"hello"`); got != "hello" {
		t.Fatalf("Unquote(%q) = %q, want %q", `"hello"`, got, "hello")
	}
}

func TestUnquoteEscapeSequences(t *testing.T) {
	if got := Unquote(`"line\nnewline"`); got != "line\nnewline" {
		t.Fatalf("Unquote(%q) = %q, want %q", `"line\nnewline"`, got, "line\nnewline")
	}
}

func TestUnquoteNoQuotes(t *testing.T) {
	if got := Unquote("bare"); got != "bare" {
		t.Fatalf("Unquote(%q) = %q, want %q", "bare", got, "bare")
	}
}

func TestUnquoteEmpty(t *testing.T) {
	if got := Unquote(""); got != "" {
		t.Fatalf("Unquote(%q) = %q, want %q", "", got, "")
	}
}

func TestUnquoteSingleChar(t *testing.T) {
	if got := Unquote("x"); got != "x" {
		t.Fatalf("Unquote(%q) = %q, want %q", "x", got, "x")
	}
}

func TestUnquoteEmptyQuoted(t *testing.T) {
	if got := Unquote(`""`); got != "" {
		t.Fatalf("Unquote(%q) = %q, want %q", `""`, got, "")
	}
}

func TestUnquoteUnicodeEscape(t *testing.T) {
	if got := Unquote(`"\u0041"`); got != "A" {
		t.Fatalf("Unquote(%q) = %q, want %q", `"\u0041"`, got, "A")
	}
}

func TestUnquoteMalformedFallback(t *testing.T) {
	// Invalid escape sequence — strconv.Unquote fails, naive strip is used.
	input := `"\z"`
	got := Unquote(input)
	if got != `\z` {
		t.Fatalf("Unquote(%q) = %q, want %q", input, got, `\z`)
	}
}
