package catalog_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/JaimeStill/spike-harness-driver/harness/catalog"
)

func skillMD(frontmatter string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("---\n" + frontmatter + "---\n\n# Body\n")}
}

func TestSkills(t *testing.T) {
	fsys := fstest.MapFS{
		"review/SKILL.md":     skillMD("name: review\ndescription: Reviews a diff.\n"),
		"review/checklist.md": {Data: []byte("- tests\n")},
		"deploy/SKILL.md":     skillMD("# A comment.\nname: deploy\ndescription: Deploys.\nmetadata:\n  owner: ops\n"),
		"notes/README.md":     {Data: []byte("Not a skill.\n")},
		"top-level-file.md":   {Data: []byte("Not a skill either.\n")},
	}
	skills, err := catalog.Skills(fsys)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range skills {
		names = append(names, s.Name)
	}
	if got, want := strings.Join(names, ","), "deploy,review"; got != want {
		t.Fatalf("names = %s, want %s", got, want)
	}
	// Each skill's FS is rooted at its own directory.
	data, err := fs.ReadFile(skills[1].FS, "checklist.md")
	if err != nil || string(data) != "- tests\n" {
		t.Fatalf("review/checklist.md = %q, %v", data, err)
	}
}

func TestSkillQuotedValues(t *testing.T) {
	fsys := fstest.MapFS{
		"SKILL.md": skillMD("name: 'quoted'\ndescription: \"Says: hello # not a comment\"\n"),
	}
	s, err := catalog.Skill(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "quoted" {
		t.Fatalf("name = %q, want quoted", s.Name)
	}
}

func TestSkillsInvalid(t *testing.T) {
	cases := []struct {
		name string
		fsys fstest.MapFS
		want string
	}{
		{
			"missing description",
			fstest.MapFS{"a/SKILL.md": skillMD("name: a\n")},
			"a/SKILL.md: frontmatter has no description",
		},
		{
			"name differs from directory",
			fstest.MapFS{"a/SKILL.md": skillMD("name: b\ndescription: B.\n")},
			`a/SKILL.md: name "b" differs`,
		},
		{
			"bad name characters",
			fstest.MapFS{"My_Skill/SKILL.md": skillMD("name: My_Skill\ndescription: Mine.\n")},
			`My_Skill/SKILL.md: name "My_Skill" isn't`,
		},
		{
			"no frontmatter",
			fstest.MapFS{"a/SKILL.md": {Data: []byte("# A\n")}},
			"a/SKILL.md: no frontmatter",
		},
		{
			"unclosed frontmatter",
			fstest.MapFS{"a/SKILL.md": {Data: []byte("---\nname: a\ndescription: A.\n")}},
			"a/SKILL.md: frontmatter isn't closed",
		},
		{
			"block scalar",
			fstest.MapFS{"a/SKILL.md": skillMD("name: a\ndescription: >\n  A.\n")},
			`a/SKILL.md: frontmatter key "description": block scalars`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := catalog.Skills(c.fsys)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

// SkillsDir and SkillDir keep each skill's absolute directory, which an adapter hands the
// harness as it is; a skill loaded from an fs.FS has none.
func TestSkillsDirKeepsTheDirectory(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"review", "deploy"} {
		fsys := fstest.MapFS{"SKILL.md": skillMD("name: " + name + "\ndescription: Does it.\n")}
		if err := os.CopyFS(filepath.Join(root, name), fsys); err != nil {
			t.Fatal(err)
		}
	}
	skills, err := catalog.SkillsDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range skills {
		if want := filepath.Join(root, s.Name); s.Dir != want {
			t.Errorf("%s: Dir = %q, want %q", s.Name, s.Dir, want)
		}
	}

	t.Chdir(root)
	s, err := catalog.SkillDir("review")
	if err != nil || s.Dir != filepath.Join(root, "review") || !filepath.IsAbs(s.Dir) {
		t.Fatalf("SkillDir = %+v, %v; want the absolute directory", s, err)
	}

	fromFS, err := catalog.Skills(os.DirFS(root))
	if err != nil || fromFS[0].Dir != "" {
		t.Fatalf("Skills over an FS = %+v, %v; want no Dir", fromFS, err)
	}
}
