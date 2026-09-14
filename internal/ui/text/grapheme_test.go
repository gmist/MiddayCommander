package text

import (
	"testing"
)

func TestGraphemeBoundaries(t *testing.T) {
	input := "a👨‍👩‍👧‍👦日本"
	familyStart := len("a")
	japanStart := familyStart + len("👨‍👩‍👧‍👦")
	lastCJKStart := japanStart + len("日")

	tests := []struct {
		name string
		got  int
		want int
	}{
		{"previous from end", PreviousGraphemeBoundary(input, len(input)), lastCJKStart},
		{"previous from family end", PreviousGraphemeBoundary(input, japanStart), familyStart},
		{"previous from start", PreviousGraphemeBoundary(input, 0), -1},
		{"next from family", NextGraphemeBoundary(input, familyStart), japanStart},
		{"next from Japan", NextGraphemeBoundary(input, japanStart), lastCJKStart},
		{"next from end", NextGraphemeBoundary(input, len(input)), len(input)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %d, want %d", tt.got, tt.want)
			}
		})
	}
}
