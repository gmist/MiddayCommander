package overlay

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Place composites a box on top of a background string, centered.
// The background remains visible around the box.
func Place(bg string, boxContent string, bgWidth, bgHeight, boxWidth, boxHeight int) string {
	bgLines := strings.Split(bg, "\n")
	for len(bgLines) < bgHeight {
		bgLines = append(bgLines, strings.Repeat(" ", bgWidth))
	}

	fgLines := strings.Split(boxContent, "\n")

	xOff := (bgWidth - boxWidth) / 2
	yOff := (bgHeight - boxHeight) / 2
	if xOff < 0 {
		xOff = 0
	}
	if yOff < 0 {
		yOff = 0
	}

	for i, fgLine := range fgLines {
		bgIdx := yOff + i
		if bgIdx >= len(bgLines) {
			break
		}

		bgLine := bgLines[bgIdx]

		// ANSI-aware slicing: left portion of bg, then the fg box line, then right portion of bg
		left := ansi.Truncate(bgLine, xOff, "")
		right := ansi.Cut(bgLine, xOff+boxWidth, bgWidth)

		bgLines[bgIdx] = left + fgLine + right
	}

	return strings.Join(bgLines[:bgHeight], "\n")
}

// RenderBox draws a bordered box with title, content lines, and optional footer.
func RenderBox(title string, contentLines []string, footer string, width, height int, borderColor, bgColor, titleColor lipgloss.Color) string {
	borderStyle := lipgloss.NewStyle().Foreground(borderColor).Background(bgColor)
	titleStyle := lipgloss.NewStyle().Foreground(titleColor).Background(bgColor).Bold(true)
	fillStyle := lipgloss.NewStyle().Background(bgColor)

	innerWidth := width - 2

	var lines []string

	// Top border with title (width-aware so multibyte titles align correctly)
	titleStr := " " + title + " "
	if lipgloss.Width(titleStr) > innerWidth {
		titleStr = ansi.Truncate(titleStr, innerWidth, "")
	}
	padLen := innerWidth - lipgloss.Width(titleStr)
	if padLen < 0 {
		padLen = 0
	}
	top := borderStyle.Render("┌") + titleStyle.Render(titleStr) + borderStyle.Render(strings.Repeat("─", padLen)+"┐")
	lines = append(lines, top)

	// Content rows
	contentHeight := height - 2
	if footer != "" {
		contentHeight--
	}

	for i := 0; i < contentHeight; i++ {
		var row string
		if i < len(contentLines) {
			row = contentLines[i]
		} else {
			row = fillStyle.Render(strings.Repeat(" ", innerWidth))
		}
		rowWidth := lipgloss.Width(row)
		if rowWidth < innerWidth {
			row += fillStyle.Render(strings.Repeat(" ", innerWidth-rowWidth))
		}
		lines = append(lines, borderStyle.Render("│")+row+borderStyle.Render("│"))
	}

	// Footer
	if footer != "" {
		footerWidth := lipgloss.Width(footer)
		if footerWidth < innerWidth {
			footer += fillStyle.Render(strings.Repeat(" ", innerWidth-footerWidth))
		}
		lines = append(lines, borderStyle.Render("│")+footer+borderStyle.Render("│"))
	}

	// Bottom border
	bottom := borderStyle.Render("└" + strings.Repeat("─", innerWidth) + "┘")
	lines = append(lines, bottom)

	return strings.Join(lines, "\n")
}

// TruncateLeftEllipsis keeps the right-most cells of s, prefixing with an
// ellipsis if clipped. It never returns wider than width cells.
//
// The U+2026 glyph is intentional: left-truncation of paths has used it
// since the initial import, right-truncation keeps ASCII "..." (see
// PadOrTruncDots). Counted as one cell, matching the renderer
func TruncateLeftEllipsis(s string, width int) string {
	const ellipsis = "…" // U+2026 horizontal ellipsis - one cell wide
	if width < 1 {
		return ""
	}
	w := ansi.StringWidth(s)
	if w <= width {
		return s
	}
	if width == 1 {
		return ellipsis
	}

	n := w - (width - 1)
	tail := ansi.TruncateLeft(s, n, "")
	// If the cut intersects a wide grapheme, TruncateLeft keeps the whole
	// grapheme, so the result can be wider than the requested cell count.
	// Search only through the input's actual width; for a suffix that is
	// itself wider than the available space, use an empty tail instead.
	for ansi.StringWidth(tail) > width-1 && n < w {
		n++
		tail = ansi.TruncateLeft(s, n, "")
	}
	if ansi.StringWidth(tail) > width-1 {
		tail = ""
	}
	// A cluster boundary can also land short of the budget, leaving the
	// result one cell narrower. Left-pad the tail so the total is exact
	return ellipsis + strings.Repeat(" ", width-1-ansi.StringWidth(tail)) + tail
}

// PadOrTrunc pads s with trailing spaces to exactly width cells, truncating
// from the right when s is wider. The result is exactly width cells when
// width >= 1 and empty otherwise
func PadOrTrunc(s string, width int) string {
	if width < 1 {
		return ""
	}
	w := ansi.StringWidth(s)
	if w > width {
		out := ansi.Truncate(s, width, "")
		return out + strings.Repeat(" ", width-ansi.StringWidth(out))
	}
	return s + strings.Repeat(" ", width-w)
}

// PadOrTruncDots is PadOrTrunc, but clipped values end with "..." so the
// cut stays visible. The result is exactly width cells when width >= 1
func PadOrTruncDots(s string, width int) string {
	if width < 1 {
		return ""
	}
	w := ansi.StringWidth(s)
	if w > width {
		if width > 3 {
			out := ansi.Truncate(s, width-3, "") + "..."
			return out + strings.Repeat(" ", width-ansi.StringWidth(out))
		}
		out := ansi.Truncate(s, width, "")
		return out + strings.Repeat(" ", width-ansi.StringWidth(out))
	}
	return s + strings.Repeat(" ", width-w)
}

// PadLeft right-aligns s in width cells, clipping on the left edge when s is
// wider. The result is exactly width cells when width >= 1
func PadLeft(s string, width int) string {
	if width < 1 {
		return ""
	}
	w := ansi.StringWidth(s)
	if w >= width {
		out := ansi.Truncate(s, width, "")
		return strings.Repeat(" ", width-ansi.StringWidth(out)) + out
	}
	return strings.Repeat(" ", width-w) + s
}
