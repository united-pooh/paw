package bubble

import "testing"

func TestMarkdownListItemsUseDistinctMarkers(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		marker string
		text   string
	}{
		{name: "unordered", line: "- item", marker: "●", text: "item"},
		{name: "compact unchecked", line: "-[] item", marker: "○", text: "item"},
		{name: "compact checked", line: "-[x] item", marker: "✓", text: "item"},
		{name: "standard unchecked", line: "- [ ] item", marker: "○", text: "item"},
		{name: "standard checked", line: "- [x] item", marker: "✓", text: "item"},
		{name: "ordered checked", line: "1. [X] item", marker: "✓", text: "item"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marker, text, ok := markdownListItem(tt.line)
			if !ok {
				t.Fatalf("markdownListItem(%q) was not recognized", tt.line)
			}
			if marker != tt.marker {
				t.Fatalf("markdownListItem(%q) marker = %q, want %q", tt.line, marker, tt.marker)
			}
			if text != tt.text {
				t.Fatalf("markdownListItem(%q) text = %q, want %q", tt.line, text, tt.text)
			}
		})
	}
}
