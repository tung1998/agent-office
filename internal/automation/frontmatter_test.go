package automation

import "testing"

func TestFrontmatterBlockScalar(t *testing.T) {
	md := "---\nname: scroll-craft\ndescription: >\n  Build premium pages.\n  Use for \"scroll\".\n\nlicense: MIT\nnotes: |-\n  a\n  b\nsource: \"x\"\n---\nbody"
	fm, body := Frontmatter(md)
	want := map[string]string{"name": "scroll-craft", "description": `Build premium pages. Use for "scroll".`, "license": "MIT", "notes": "a\nb", "source": "x"}
	for k, v := range want {
		if fm[k] != v {
			t.Errorf("%s = %q, want %q", k, fm[k], v)
		}
	}
	if body != "body" {
		t.Errorf("body = %q", body)
	}
}
