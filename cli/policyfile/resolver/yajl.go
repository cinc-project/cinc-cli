package resolver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// This file reproduces the two JSON serializations chef applies to a
// Policyfile lock, so cinc's lock is byte-identical to `chef install`:
//
//   - yajlEncode mirrors FFI_Yajl::Encoder.encode(data, pretty: true), the
//     pretty printer chef writes the lock file with. Its quirks (2-space
//     indent, a blank line inside empty {}/[], no HTML or "/" escaping, raw
//     UTF-8, a trailing newline) are matched exactly.
//   - canonicalize mirrors PolicyfileLock#canonicalize, the sorted-key compact
//     form fed into the SHA256 that becomes the revision_id.
//
// Both operate on an order-preserving value model (jsonObject) so attribute key
// order — which chef takes from Ruby hash insertion order — survives.

// jsonObject is a JSON object that remembers key insertion order.
type jsonObject struct {
	keys []string
	vals map[string]any
}

func newJSONObject() *jsonObject {
	return &jsonObject{vals: map[string]any{}}
}

func (o *jsonObject) set(key string, val any) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = val
}

func (o *jsonObject) len() int { return len(o.keys) }

// parseOrdered decodes raw JSON into the order-preserving value model: objects
// become *jsonObject, arrays []any, numbers json.Number (so the exact numeric
// text Ruby emitted is preserved), and strings/bools/null their Go equivalents.
func parseOrdered(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := parseOrderedValue(dec)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func parseOrderedValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return parseOrderedFromToken(dec, tok)
}

func parseOrderedFromToken(dec *json.Decoder, tok json.Token) (any, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := newJSONObject()
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyTok.(string)
				if !ok {
					return nil, fmt.Errorf("cinc: non-string object key %v", keyTok)
				}
				val, err := parseOrderedValue(dec)
				if err != nil {
					return nil, err
				}
				obj.set(key, val)
			}
			if _, err := dec.Token(); err != nil { // consume '}'
				return nil, err
			}
			return obj, nil
		case '[':
			arr := []any{}
			for dec.More() {
				val, err := parseOrderedValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			if _, err := dec.Token(); err != nil { // consume ']'
				return nil, err
			}
			return arr, nil
		}
	}
	return tok, nil
}

// yajlEncode renders v in FFI_Yajl pretty form (including the trailing
// newline), matching the bytes `chef install` writes to Policyfile.lock.json.
func yajlEncode(v any) []byte {
	var b strings.Builder
	yajlWrite(&b, v, 0)
	b.WriteByte('\n')
	return []byte(b.String())
}

func yajlIndent(b *strings.Builder, depth int) {
	for range depth {
		b.WriteString("  ")
	}
}

func yajlWrite(b *strings.Builder, v any, depth int) {
	switch val := v.(type) {
	case *jsonObject:
		if val.len() == 0 {
			b.WriteString("{\n\n")
			yajlIndent(b, depth)
			b.WriteByte('}')
			return
		}
		b.WriteString("{\n")
		for i, key := range val.keys {
			yajlIndent(b, depth+1)
			yajlString(b, key)
			b.WriteString(": ")
			yajlWrite(b, val.vals[key], depth+1)
			if i < len(val.keys)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		yajlIndent(b, depth)
		b.WriteByte('}')
	case []any:
		if len(val) == 0 {
			b.WriteString("[\n\n")
			yajlIndent(b, depth)
			b.WriteByte(']')
			return
		}
		b.WriteString("[\n")
		for i, elem := range val {
			yajlIndent(b, depth+1)
			yajlWrite(b, elem, depth+1)
			if i < len(val)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		yajlIndent(b, depth)
		b.WriteByte(']')
	case []string:
		anys := make([]any, len(val))
		for i, s := range val {
			anys[i] = s
		}
		yajlWrite(b, anys, depth)
	default:
		yajlScalar(b, v)
	}
}

func yajlScalar(b *strings.Builder, v any) {
	switch val := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if val {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		yajlString(b, val)
	case json.Number:
		b.WriteString(val.String())
	case int:
		fmt.Fprintf(b, "%d", val)
	case int64:
		fmt.Fprintf(b, "%d", val)
	case float64:
		b.WriteString(formatFloat(val))
	default:
		// Fall back to compact JSON for anything unexpected.
		raw, _ := json.Marshal(val)
		b.Write(raw)
	}
}

// yajlString writes a JSON string literal with yajl's escaping: the mandatory
// JSON escapes only (quote, backslash, control characters). It does NOT escape
// "/", "<", ">", "&", and passes multibyte UTF-8 through untouched.
func yajlString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

func formatFloat(f float64) string {
	raw, _ := json.Marshal(f)
	return string(raw)
}

// canonicalize renders v in chef's canonical attribute form: objects with keys
// sorted lexically, no whitespace, used only to build the revision_id hash
// input. It mirrors PolicyfileLock#canonicalize_elements.
func canonicalize(v any) string {
	var b strings.Builder
	canonicalizeInto(&b, v)
	return b.String()
}

func canonicalizeInto(b *strings.Builder, v any) {
	switch val := v.(type) {
	case *jsonObject:
		keys := append([]string(nil), val.keys...)
		sort.Strings(keys)
		b.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('"')
			b.WriteString(key)
			b.WriteString(`":`)
			canonicalizeInto(b, val.vals[key])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, elem := range val {
			if i > 0 {
				b.WriteByte(',')
			}
			canonicalizeInto(b, elem)
		}
		b.WriteByte(']')
	case string:
		b.WriteByte('"')
		b.WriteString(val)
		b.WriteByte('"')
	case json.Number:
		b.WriteString(val.String())
	case bool:
		if val {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case nil:
		b.WriteString("null")
	default:
		raw, _ := json.Marshal(val)
		b.Write(raw)
	}
}
