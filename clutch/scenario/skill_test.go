package scenario

import "testing"

func TestTheEmbeddedSkillLoads(t *testing.T) {
	skill, err := mottoSkill()
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != "clutch-motto" {
		t.Errorf("name = %q", skill.Name)
	}
}

func TestReplyHasPhrase(t *testing.T) {
	for _, reply := range []string{motto, "hold the line shift the load", `The motto: "Hold the line — shift the load!"`} {
		if err := replyHasPhrase(reply, motto, "the motto"); err != nil {
			t.Error(err)
		}
	}
	if err := replyHasPhrase("Hold the line.", motto, "the motto"); err == nil {
		t.Error("half the motto passed")
	}
}
