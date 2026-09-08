// Package nodeedit is a bubbletea form for editing a Chef/Cinc node. It
// presents the node's name, environment, policy, and run list as plain
// human-readable fields and its editable attribute bags (normal, default,
// override) in the shared JSON editor — so the common parts of a node never
// look like raw JSON. The automatic bag is not editable: ohai recomputes it
// on every cinc run, so it is preserved untouched rather than shown. The
// pure node<->form conversions live here; the interactive Model lives in
// model.go.
package nodeedit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	cinc "github.com/cinc-project/cinc-api"
)

// attrBags is the object presented in the attributes JSON editor: the
// editable node attribute precedence levels, in Chef's conventional order.
// automatic is deliberately absent — it is recomputed by ohai every run, so
// it is preserved from the original node rather than edited (see buildNode).
type attrBags struct {
	Normal   cinc.Attributes `json:"normal"`
	Default  cinc.Attributes `json:"default"`
	Override cinc.Attributes `json:"override"`
}

// runListBullet is the prefix shown before each run-list entry: a small
// indent so the items nest visually under the "Run list" heading, then the
// "- " bullet. parseRunList strips it back off.
const runListBullet = "    - "

// runListText renders a node's run list one bulleted entry per line for
// editing, e.g. "    - recipe[nginx]". An empty run list renders as empty so
// a fresh node shows a blank field rather than a stray bullet.
func runListText(n *cinc.Node) string {
	if len(n.RunList) == 0 {
		return ""
	}
	lines := make([]string, len(n.RunList))
	for i, e := range n.RunList {
		lines[i] = runListBullet + e
	}
	return strings.Join(lines, "\n")
}

// parseRunList turns edited run-list text back into entries: it strips the
// leading "- " bullet (tolerating extra indentation and a bare bullet),
// trims each line, and drops blanks.
func parseRunList(text string) []string {
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		e := strings.TrimSpace(line)
		e = strings.TrimPrefix(e, "-")
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// attributesSeed is the JSON shown in the attributes editor: all four bags,
// in Chef's order, each defaulting to an empty object so every bag is
// visible. The JSON editor re-formats it, so the seed only needs valid,
// correctly-ordered JSON.
func attributesSeed(n *cinc.Node) ([]byte, error) {
	bags := []struct {
		key string
		val cinc.Attributes
	}{
		{"normal", n.Normal},
		{"default", n.Default},
		{"override", n.Override},
	}
	var b bytes.Buffer
	b.WriteByte('{')
	for i, bag := range bags {
		if i > 0 {
			b.WriteByte(',')
		}
		v := map[string]any(bag.val)
		if v == nil {
			v = map[string]any{}
		}
		vb, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "%q:%s", bag.key, vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// buildNode assembles the edited form back into a node. The name is supplied
// by the caller (the read-only heading when editing, the typed field when
// creating). The computed automatic attributes are carried from the original
// when editing and empty when creating (orig is nil). attrsJSON must contain
// only the editable bags (normal, default, override); any other top-level
// key — including automatic — is rejected so a stray key is surfaced rather
// than silently dropped.
func buildNode(orig *cinc.Node, name, env, policyName, policyGroup string, runList []string, attrsJSON []byte) (*cinc.Node, error) {
	var bags attrBags
	dec := json.NewDecoder(bytes.NewReader(attrsJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&bags); err != nil {
		return nil, err
	}
	var automatic cinc.Attributes
	if orig != nil {
		automatic = orig.Automatic
	}
	return &cinc.Node{
		Name:        name,
		Environment: env,
		RunList:     runList,
		Normal:      bags.Normal,
		Default:     bags.Default,
		Override:    bags.Override,
		Automatic:   automatic,
		PolicyName:  policyName,
		PolicyGroup: policyGroup,
	}, nil
}

// nodeUnchanged reports whether two nodes are equivalent for edit purposes,
// treating nil and empty collections alike. The name is ignored since it is
// not editable.
func nodeUnchanged(a, b *cinc.Node) bool {
	return bytes.Equal(nodeSignature(a), nodeSignature(b))
}

func nodeSignature(n *cinc.Node) []byte {
	sig, _ := json.Marshal(map[string]any{
		"environment":  n.Environment,
		"policy_name":  n.PolicyName,
		"policy_group": n.PolicyGroup,
		"run_list":     orEmptySlice(n.RunList),
		"normal":       orEmptyMap(n.Normal),
		"default":      orEmptyMap(n.Default),
		"override":     orEmptyMap(n.Override),
		"automatic":    orEmptyMap(n.Automatic),
	})
	return sig
}

func orEmptyMap(a cinc.Attributes) map[string]any {
	if a == nil {
		return map[string]any{}
	}
	return a
}

func orEmptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
