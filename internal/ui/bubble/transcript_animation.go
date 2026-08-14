package bubble

import (
	"strings"
	"time"
)

// transcriptAnimationMode controls the visual granularity of a new assistant
// line. The transcript itself is always buffered as complete text; this value
// only changes how the already-rendered text is displayed.
type transcriptAnimationMode string

const (
	transcriptAnimationModeLine transcriptAnimationMode = "line"
	transcriptAnimationModeChar transcriptAnimationMode = "char"
)

// transcriptRenderEffect is applied after Markdown and ANSI rendering.
type transcriptRenderEffect string

const (
	transcriptRenderEffectNormal transcriptRenderEffect = "normal"
	transcriptRenderEffectNoise  transcriptRenderEffect = "noise"
	transcriptRenderEffectReveal transcriptRenderEffect = "reveal"
)

type transcriptAnimationLine struct {
	ID        uint64
	StartedAt time.Time
}

const transcriptAnimationDuration = time.Second

// animateStyledTranscriptText applies a render-only effect to complete,
// already-styled transcript text. It deliberately returns rendered unchanged
// for normal and at completion, making the canonical Markdown/transcript text
// independent of animation.
func animateStyledTranscriptText(rendered string, mode transcriptAnimationMode, effect transcriptRenderEffect, line transcriptAnimationLine, now time.Time, duration time.Duration) string {
	if rendered == "" || effect == transcriptRenderEffectNormal || duration <= 0 || !now.Before(line.StartedAt.Add(duration)) {
		return rendered
	}
	if mode != transcriptAnimationModeChar {
		mode = transcriptAnimationModeLine
	}

	progress := float64(now.Sub(line.StartedAt)) / float64(duration)
	if progress < 0 {
		progress = 0
	}
	if progress >= 1 {
		return rendered
	}

	parts := strings.SplitAfter(rendered, "\n")
	var out strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		newline := ""
		content := part
		if strings.HasSuffix(content, "\n") {
			content = strings.TrimSuffix(content, "\n")
			newline = "\n"
		}
		out.WriteString(animateStyledTranscriptLine(content, mode, effect, line.ID, progress))
		out.WriteString(newline)
	}
	return out.String()
}

func animateStyledTranscriptLine(rendered string, mode transcriptAnimationMode, effect transcriptRenderEffect, lineID uint64, progress float64) string {
	parsed := parseStyledCellLine(rendered)
	visible := make([]int, 0, len(parsed.atoms))
	for index, atom := range parsed.atoms {
		if atom.cellEnd > atom.cellStart {
			visible = append(visible, index)
		}
	}
	if len(visible) == 0 {
		return rendered
	}

	revealed := 0
	if mode == transcriptAnimationModeLine {
		if progress >= 1 {
			return rendered
		}
	} else {
		revealed = int(progress * float64(len(visible)))
		if revealed > len(visible) {
			revealed = len(visible)
		}
	}

	visiblePosition := make(map[int]int, len(visible))
	for position, index := range visible {
		visiblePosition[index] = position
	}

	var out strings.Builder
	for index, atom := range parsed.atoms {
		if atom.control {
			out.WriteString(atom.text)
			continue
		}
		if atom.cellEnd <= atom.cellStart {
			// Combining marks and zero-width printable atoms belong to the
			// preceding display atom. Keep them with it, never emit them alone.
			if atom.ownerStart >= 0 {
				out.WriteString(atom.text)
			}
			continue
		}

		position := visiblePosition[index]
		isRevealed := mode == transcriptAnimationModeChar && position < revealed
		if isRevealed {
			out.WriteString(atom.text)
			continue
		}
		if effect == transcriptRenderEffectNoise {
			out.WriteString(transcriptNoiseCells(lineID, position, atom.cellEnd-atom.cellStart, int(progress*12)))
		} else {
			out.WriteString(strings.Repeat(" ", atom.cellEnd-atom.cellStart))
		}
	}
	return out.String()
}

var transcriptNoiseGlyphs = []string{"·", "∙", "⋅", "░", "▒", "*", "+", "~", "?", "#"}

// transcriptNoiseCells returns only one-cell glyphs. Wide display atoms are
// represented by the corresponding number of cells, so the transform cannot
// change line geometry. The frame bucket makes the noise move while its output
// remains deterministic for the same line, position, and time bucket.
func transcriptNoiseCells(lineID uint64, position, width, frame int) string {
	if width <= 0 {
		return ""
	}
	var out strings.Builder
	for cell := 0; cell < width; cell++ {
		hash := transcriptNoiseHash(lineID, position, cell, frame)
		out.WriteString(transcriptNoiseGlyphs[int(hash%uint64(len(transcriptNoiseGlyphs)))])
	}
	return out.String()
}

func transcriptNoiseHash(lineID uint64, position, cell, frame int) uint64 {
	x := lineID + uint64(position+1)*0x9e3779b97f4a7c15
	x += uint64(cell+1) * 0xbf58476d1ce4e5b9
	x += uint64(frame+1) * 0x94d049bb133111eb
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func clampTranscriptAnimationProgress(now, started time.Time, duration time.Duration) float64 {
	if duration <= 0 || !now.After(started) {
		return 0
	}
	progress := float64(now.Sub(started)) / float64(duration)
	if progress > 1 {
		return 1
	}
	return progress
}

// Keep the helper available to integration code that uses the shared duration
// while allowing tests to use a shorter deterministic interval.
func transcriptAnimationProgress(now time.Time, line transcriptAnimationLine, duration time.Duration) float64 {
	return clampTranscriptAnimationProgress(now, line.StartedAt, duration)
}
