package nodeedit

import (
	"strings"
	"testing"

	cinc "github.com/tas50/cinc-api"
)

func TestRunListRoundTrip(t *testing.T) {
	n := &cinc.Node{RunList: []string{"recipe[nginx]", "role[base]"}}
	if got := runListText(n); got != "    - recipe[nginx]\n    - role[base]" {
		t.Errorf("runListText = %q, want each item indented and bulleted", got)
	}
	if got := runListText(&cinc.Node{}); got != "" {
		t.Errorf("runListText of an empty run list = %q, want empty", got)
	}
	// Parsing tolerates the bullet prefix, stray indentation, a bare bullet,
	// and undashed lines alike.
	got := parseRunList("- recipe[nginx]\n  - role[base]  \n- \n\nrecipe[bare]\n")
	want := []string{"recipe[nginx]", "role[base]", "recipe[bare]"}
	if len(got) != len(want) {
		t.Fatalf("parseRunList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parseRunList[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAttributesSeedShowsEditableBagsInOrderAndExcludesAutomatic(t *testing.T) {
	n := &cinc.Node{Normal: cinc.Attributes{"port": 5432}}
	seed, err := attributesSeed(n)
	if err != nil {
		t.Fatalf("attributesSeed: %v", err)
	}
	s := string(seed)
	iNormal := strings.Index(s, "normal")
	iDefault := strings.Index(s, "default")
	iOverride := strings.Index(s, "override")
	ordered := iNormal >= 0 && iNormal < iDefault && iDefault < iOverride
	if !ordered {
		t.Errorf("editable bags out of order in seed:\n%s", s)
	}
	if strings.Contains(s, "automatic") {
		t.Errorf("automatic must not appear in the editor seed:\n%s", s)
	}
	if !strings.Contains(s, "5432") {
		t.Errorf("normal attribute missing from seed:\n%s", s)
	}
}

func TestBuildNodeAssemblesFieldsAndPreservesAutomatic(t *testing.T) {
	// automatic is not in the editor JSON; it must be carried from the
	// original node untouched.
	orig := &cinc.Node{Name: "db01", Automatic: cinc.Attributes{"platform": "ubuntu"}}
	attrs := []byte(`{"normal":{"role":"db"},"default":{},"override":{}}`)
	n, err := buildNode(orig, orig.Name, "prod", "base", "prodgrp", []string{"recipe[x]"}, attrs)
	if err != nil {
		t.Fatalf("buildNode: %v", err)
	}
	if n.Name != "db01" {
		t.Errorf("name = %q, want db01 (carried from original)", n.Name)
	}
	if n.Environment != "prod" || n.PolicyName != "base" || n.PolicyGroup != "prodgrp" {
		t.Errorf("fields = %+v", n)
	}
	if len(n.RunList) != 1 || n.RunList[0] != "recipe[x]" {
		t.Errorf("run_list = %v", n.RunList)
	}
	if n.Normal["role"] != "db" {
		t.Errorf("normal not assembled: %v", n.Normal)
	}
	if n.Automatic["platform"] != "ubuntu" {
		t.Errorf("automatic not preserved from original: %v", n.Automatic)
	}
}

func TestBuildNodeRejectsUnknownAttributeBag(t *testing.T) {
	orig := &cinc.Node{Name: "db01"}
	attrs := []byte(`{"normal":{},"default":{},"override":{},"bogus":{}}`)
	if _, err := buildNode(orig, orig.Name, "", "", "", nil, attrs); err == nil {
		t.Error("expected an error for an unknown attribute bag")
	}
}

// automatic is no longer an editable bag, so a user who types it back into
// the editor is rejected like any other unknown key.
func TestBuildNodeRejectsAutomaticBag(t *testing.T) {
	orig := &cinc.Node{Name: "db01"}
	attrs := []byte(`{"normal":{},"default":{},"override":{},"automatic":{}}`)
	if _, err := buildNode(orig, orig.Name, "", "", "", nil, attrs); err == nil {
		t.Error("automatic should be rejected as an unknown bag in the editor")
	}
}

func TestNodeUnchangedSignature(t *testing.T) {
	orig := &cinc.Node{Name: "db01", Environment: "prod"}
	same := &cinc.Node{Name: "db01", Environment: "prod", RunList: []string{}, Normal: cinc.Attributes{}}
	if !nodeUnchanged(orig, same) {
		t.Error("nil and empty collections should compare as unchanged")
	}
	changed := &cinc.Node{Name: "db01", Environment: "staging"}
	if nodeUnchanged(orig, changed) {
		t.Error("an environment change should compare as changed")
	}
}
