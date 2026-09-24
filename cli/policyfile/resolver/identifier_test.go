package resolver

import (
	"os"
	"path/filepath"
	"testing"
)

// TestComputeIdentifierGoldenValues pins cinc's cookbook identifier computation
// to the exact content/dotted-decimal identifiers real `chef install` produced
// for the testdata cookbooks (captured from the committed goldens). These are
// the bytes a Chef Infra Server addresses a cookbook artifact by, so they must
// match chef exactly.
func TestComputeIdentifierGoldenValues(t *testing.T) {
	cases := []struct {
		dir           string
		content       string
		dottedDecimal string
	}{
		{
			dir:           "testdata/transitive/cookbooks/alpha",
			content:       "3817872381e3cbf5d8bcbc16bced7e5e5438344b",
			dottedDecimal: "15788467879535563.69199674415168749.138943604995147",
		},
		{
			dir:           "testdata/transitive/cookbooks/beta",
			content:       "484874ca84fbec8d44006eb2a1840781c331292f",
			dottedDecimal: "20345864774286316.39762740364091780.8253906954543",
		},
		{
			dir:           "testdata/transitive/cookbooks/gamma",
			content:       "194c08b2a394d35a12d38482a060c2cdf63df9b3",
			dottedDecimal: "7120474658280659.25353447574511712.214189855340979",
		},
		{
			// chefignore must exclude *.bak / *.tmp, matching chef.
			dir:     "testdata/chefignore_cookbook/cookbooks/widget",
			content: "e80db6497ca7c1baab2f722b0372f6bc57e8bbba",
		},
	}

	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			id, err := ComputeIdentifier(tc.dir, "")
			if err != nil {
				t.Fatal(err)
			}
			if id.Content != tc.content {
				t.Errorf("content identifier = %q, want %q", id.Content, tc.content)
			}
			if tc.dottedDecimal != "" && id.DottedDecimal != tc.dottedDecimal {
				t.Errorf("dotted-decimal identifier = %q, want %q", id.DottedDecimal, tc.dottedDecimal)
			}
		})
	}
}

// TestIdentifierFollowsSymlinksInsideTheCookbook checks that a symlink to a
// file inside the cookbook counts as that file, as Chef's loader reads it:
// the cookbook's identifier matches the same cookbook with a plain copy.
func TestIdentifierFollowsSymlinksInsideTheCookbook(t *testing.T) {
	write := func(dir string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, "recipes"), 0o755); err != nil {
			t.Fatal(err)
		}
		for rel, body := range map[string]string{
			"metadata.rb":        "name 'widget'\nversion '1.0.0'\n",
			"recipes/default.rb": "log 'hi'\n",
		} {
			if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	linked, copied := filepath.Join(t.TempDir(), "widget"), filepath.Join(t.TempDir(), "widget")
	write(linked)
	write(copied)
	if err := os.Symlink("default.rb", filepath.Join(linked, "recipes", "alias.rb")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copied, "recipes", "alias.rb"), []byte("log 'hi'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ComputeIdentifier(linked, "")
	if err != nil {
		t.Fatal(err)
	}
	want, err := ComputeIdentifier(copied, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("identifier with a symlink = %+v, want %+v (as with a copy)", got, want)
	}
}
