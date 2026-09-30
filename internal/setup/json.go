package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// Object is a JSON object that keeps its key order, so editing settings.json doesn't reshuffle it.
type Object struct {
	keys []string
	vals map[string]json.RawMessage
}

// NewObject is an empty ordered object.
func NewObject() *Object { return &Object{vals: map[string]json.RawMessage{}} }

// ParseObject reads a JSON object, keeping its key order.
func ParseObject(b []byte) (*Object, error) {
	o := NewObject()
	if len(bytes.TrimSpace(b)) == 0 {
		return o, nil
	}

	dec := json.NewDecoder(bytes.NewReader(b))

	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}

	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}

	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}

		k, ok := tok.(string)
		if !ok {
			return nil, errors.New("bad object key")
		}

		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}

		o.Set(k, raw)
	}

	if _, err := dec.Token(); err != nil {
		return nil, err
	}

	if dec.More() {
		return nil, errors.New("trailing data after object")
	}

	return o, nil
}

// Has reports whether k is set.
func (o *Object) Has(k string) bool { _, ok := o.vals[k]; return ok }

// Raw is k's value as raw JSON.
func (o *Object) Raw(k string) json.RawMessage { return o.vals[k] }

// Set stores raw JSON under k, keeping k's position if it exists.
func (o *Object) Set(k string, raw json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}

	o.vals[k] = raw
}

// SetValue marshals v and stores it under k.
func (o *Object) SetValue(k string, v any) {
	o.Set(k, mustMarshal(v))
}

// Delete removes k.
func (o *Object) Delete(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}

	delete(o.vals, k)

	for i, kk := range o.keys {
		if kk == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// Len is the number of keys.
func (o *Object) Len() int { return len(o.keys) }

// Child parses a nested object value, or returns an empty one.
func (o *Object) Child(k string) *Object {
	if c, err := ParseObject(o.vals[k]); err == nil {
		return c
	}

	return NewObject()
}

// String is k's value as a string, or "".
func (o *Object) String(k string) string {
	var s string

	_ = json.Unmarshal(o.vals[k], &s) // not a string → ""

	return s
}

// MarshalJSON writes the object compactly, in key order.
func (o *Object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')

	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}

		b.Write(mustMarshal(k))
		b.WriteByte(':')
		b.Write(o.vals[k])
	}

	b.WriteByte('}')

	return b.Bytes(), nil
}

// Pretty is the object indented two spaces, like Claude Code writes it.
func (o *Object) Pretty() []byte {
	raw, _ := o.MarshalJSON()

	var out bytes.Buffer
	if json.Indent(&out, raw, "", "  ") != nil {
		return raw
	}

	out.WriteByte('\n')

	return out.Bytes()
}

func mustMarshal(v any) json.RawMessage {
	var b bytes.Buffer

	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)

	if enc.Encode(v) != nil {
		return json.RawMessage("null")
	}

	return json.RawMessage(bytes.TrimRight(b.Bytes(), "\n"))
}

// ShellQuote quotes s for a POSIX shell, leaving plain words alone.
func ShellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-") == "" {
		return s
	}

	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ShellSplit splits a command line the way a POSIX shell would (quotes and backslashes only).
func ShellSplit(s string) []string {
	var (
		out []string
		cur strings.Builder
	)

	in, quote, esc := false, rune(0), false

	for _, r := range s {
		switch {
		case esc:
			cur.WriteRune(r)

			esc, in = false, true
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch r {
			case '"':
				quote = 0
			case '\\':
				esc = true
			default:
				cur.WriteRune(r)
			}
		case r == '\\':
			esc, in = true, true
		case r == '\'' || r == '"':
			quote, in = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if in {
				out = append(out, cur.String())
				cur.Reset()

				in = false
			}
		default:
			cur.WriteRune(r)

			in = true
		}
	}

	if in {
		out = append(out, cur.String())
	}

	return out
}
