package text

import "github.com/charmbracelet/x/ansi"

// PreviousGraphemeBoundary returns the byte index before the grapheme cluster
// at pos, or -1 when pos is already at the start.
func PreviousGraphemeBoundary(s string, pos int) int {
	if pos <= 0 {
		return -1
	}
	if pos > len(s) {
		pos = len(s)
	}
	previous, offset := 0, 0
	for offset < pos {
		cluster, _ := ansi.FirstGraphemeCluster(s[offset:], ansi.GraphemeWidth)
		next := offset + len(cluster)
		if next >= pos {
			break
		}
		previous = next
		offset = next
	}
	return previous
}

// NextGraphemeBoundary returns the byte index after the grapheme cluster at
// pos, or pos when already at the end.
func NextGraphemeBoundary(s string, pos int) int {
	if pos >= len(s) {
		return pos
	}
	cluster, _ := ansi.FirstGraphemeCluster(s[pos:], ansi.GraphemeWidth)
	return pos + len(cluster)
}
