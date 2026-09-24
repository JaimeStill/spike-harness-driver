package catalog

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// skillFile is the file that makes a directory a skill.
const skillFile = "SKILL.md"

// skillName is the Agent Skills rule for a skill's name, which Pi follows: lowercase letters
// and digits in hyphen-separated runs, with no leading, trailing, or doubled hyphen. The
// length limit, 64, is checked apart.
var skillName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Skill loads the skill whose directory is the root of fsys. The frontmatter of its SKILL.md
// must give a name and a description: Pi refuses a skill without a description, and a skill
// the harness refuses would fail the session later rather than here.
func Skill(fsys fs.FS) (harness.Skill, error) {
	name, err := readSkill(fsys)
	if err != nil {
		return harness.Skill{}, fmt.Errorf("catalog: %s: %w", skillFile, err)
	}
	return harness.Skill{Name: name, FS: fsys}, nil
}

// Skills loads every skill directly under the root of fsys, sorted by name. A subdirectory
// without a SKILL.md isn't a skill and is skipped. A skill's name must equal its directory's,
// as the Agent Skills layout requires, so no two skills share a name.
func Skills(fsys fs.FS) ([]harness.Skill, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	var skills []harness.Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		file := path.Join(e.Name(), skillFile)
		if _, err := fs.Stat(fsys, file); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("catalog: %w", err)
		}
		sub, err := fs.Sub(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("catalog: %s: %w", e.Name(), err)
		}
		name, err := readSkill(sub)
		if err != nil {
			return nil, fmt.Errorf("catalog: %s: %w", file, err)
		}
		if name != e.Name() {
			return nil, fmt.Errorf("catalog: %s: name %q differs from its directory's", file, name)
		}
		skills = append(skills, harness.Skill{Name: name, FS: sub})
	}
	// ReadDir returns the entries sorted by name, and each skill's name is its entry's, so the
	// skills are sorted already.
	return skills, nil
}

// SkillDir loads the skill whose directory on disk is dir, as Skill does over os.DirFS, and
// keeps the directory's absolute path in the skill's Dir.
func SkillDir(dir string) (harness.Skill, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return harness.Skill{}, fmt.Errorf("catalog: %w", err)
	}
	s, err := Skill(os.DirFS(abs))
	if err != nil {
		return harness.Skill{}, err
	}
	s.Dir = abs
	return s, nil
}

// SkillsDir loads every skill directly under the directory dir on disk, as Skills does over
// os.DirFS, and keeps each skill directory's absolute path in its Dir.
func SkillsDir(dir string) ([]harness.Skill, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	skills, err := Skills(os.DirFS(abs))
	if err != nil {
		return nil, err
	}
	for i := range skills {
		skills[i].Dir = filepath.Join(abs, skills[i].Name)
	}
	return skills, nil
}

// readSkill reads the SKILL.md at the root of fsys and returns the skill's validated name.
func readSkill(fsys fs.FS) (string, error) {
	data, err := fs.ReadFile(fsys, skillFile)
	if err != nil {
		return "", err
	}
	fields, err := frontmatter(data)
	if err != nil {
		return "", err
	}
	name, description := fields["name"], fields["description"]
	switch {
	case name == "":
		return "", errors.New("frontmatter has no name")
	case len(name) > 64 || !skillName.MatchString(name):
		return "", fmt.Errorf("name %q isn't 1 to 64 lowercase letters and digits in hyphen-separated runs", name)
	case description == "":
		return "", errors.New("frontmatter has no description")
	}
	return name, nil
}

// frontmatter parses the YAML frontmatter at the start of a SKILL.md: the lines between a
// first line of "---" and the next line of "---". It reads only the top-level "key: value"
// lines a skill's name and description take, with the value bare or in single or double
// quotes, and skips comments and indented lines. A full YAML parser would be the module's first
// dependency, for two keys. A block scalar ("|" or ">") for name or description is refused
// rather than misread, because its value is on the indented lines this parser skips; under any
// other key it is ignored.
func frontmatter(data []byte) (map[string]string, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	if !sc.Scan() || strings.TrimRight(sc.Text(), " \t\r") != "---" {
		return nil, errors.New("no frontmatter: the first line isn't ---")
	}
	fields := map[string]string{}
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t\r")
		if line == "---" {
			return fields, nil
		}
		if line == "" || line[0] == '#' || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		value, err := scalar(value)
		if err != nil {
			// Only name and description are read; any other key may take a form this parser
			// doesn't, such as a block scalar, without making the skill unreadable.
			if key == "name" || key == "description" {
				return nil, fmt.Errorf("frontmatter key %q: %w", key, err)
			}
			continue
		}
		fields[key] = value
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("frontmatter isn't closed by a --- line")
}

// scalar returns the value of a one-line YAML scalar: the text inside its quotes, or, bare,
// the text before any comment. Escapes inside double quotes are left as written.
func scalar(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	switch q := v[0]; q {
	case '"', '\'':
		end := strings.LastIndexByte(v, q)
		if end == 0 {
			return "", errors.New("quoted value isn't closed")
		}
		return v[1:end], nil
	case '|', '>':
		return "", errors.New("block scalars aren't supported; write the value on one line")
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v, nil
}
