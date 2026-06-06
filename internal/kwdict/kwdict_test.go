package kwdict

import "testing"

func TestNum(t *testing.T) {
	// num wins when present.
	if got := Num(map[string]any{"num": float64(3), "no": float64(108984043)}); got != 3 {
		t.Errorf("Num = %d, want 3 (num present)", got)
	}
	// num absent, post number in `no`.
	if got := Num(map[string]any{"no": float64(108984043)}); got != 108984043 {
		t.Errorf("Num = %d, want 108984043 (from no)", got)
	}
	// Neither present.
	if got := Num(map[string]any{}); got != 0 {
		t.Errorf("Num = %d, want 0", got)
	}
}
