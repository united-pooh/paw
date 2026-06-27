package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestSkill(t *testing.T, root, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, SkillFileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRegistryDiscoversSkillDirectories(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root, "design", `---
name: Visual Design
description: Improve UI clarity
---
# Design Skill
`)
	if err := os.WriteFile(filepath.Join(root, "loose.md"), []byte("# ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry([]string{root})
	skills := registry.Skills()
	if len(skills) != 1 {
		t.Fatalf("skills = %#v, want one skill", skills)
	}
	if skills[0].Name != "design" || skills[0].DisplayName != "Visual Design" {
		t.Fatalf("skill = %#v, want parsed name/display", skills[0])
	}
	if skills[0].Description != "Improve UI clarity" {
		t.Fatalf("description = %q", skills[0].Description)
	}
}

func TestRegistryMentionedSkillsParsesBareAndMarkdownReferences(t *testing.T) {
	root := t.TempDir()
	path := writeTestSkill(t, root, "design", "# Design\nUse layout discipline.")
	writeTestSkill(t, root, "backend", "# Backend\nUse server discipline.")

	registry := NewRegistry([]string{root})
	input := "use $design and [$backend](" + filepath.Join(root, "backend", SkillFileName) + ") plus [$design](" + path + ") again"
	skills := registry.MentionedSkills(input)
	if len(skills) != 2 {
		t.Fatalf("skills = %#v, want two unique skills", skills)
	}
	if skills[0].Name != "backend" && skills[1].Name != "backend" {
		t.Fatalf("skills = %#v, want backend from markdown path", skills)
	}
}

func TestInstructionContextIncludesSelectedSkillContent(t *testing.T) {
	root := t.TempDir()
	path := writeTestSkill(t, root, "design", `---
description: Improve UI clarity
---
Design body line.`)

	registry := NewRegistry([]string{root})
	context, loaded, errs := registry.InstructionContext("please use [$design](" + path + ")")
	if len(errs) != 0 {
		t.Fatalf("errs = %#v", errs)
	}
	if len(loaded) != 1 || loaded[0].Name != "design" {
		t.Fatalf("loaded = %#v, want design", loaded)
	}
	for _, want := range []string{"Selected skills for this turn", "Skill: design", "Improve UI clarity", "Design body line."} {
		if !strings.Contains(context, want) {
			t.Fatalf("context = %q, want %q", context, want)
		}
	}
}
