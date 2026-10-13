package bubble

import "testing"

// 注册表内一个语义一个字形，不允许两个字形表达同一语义、也不允许
// 同一字形被两个语义复用（避免 ▸ 同时表示折叠和别的）。
func TestGlyphRegistryUnique(t *testing.T) {
	seen := make(map[string]string, len(allGlyphs))
	for _, entry := range allGlyphs {
		if entry.glyph == "" {
			t.Fatalf("语义 %q 的字形为空", entry.semantic)
		}
		if prev, dup := seen[entry.glyph]; dup {
			t.Fatalf("字形 %q 同时登记给 %q 与 %q：一个语义必须一个字形", entry.glyph, prev, entry.semantic)
		}
		seen[entry.glyph] = entry.semantic
	}
}
