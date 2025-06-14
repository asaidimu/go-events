package pkg

import "testing"

func TestGreeting(t *testing.T) {
	expected := "Hello, World!"
	if got := Greeting("World"); got != expected {
		t.Errorf("Greeting() = %q, want %q", got, expected)
	}
}
