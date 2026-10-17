package hookjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
)

// Object is a JSON object that keeps its keys in order and its values as they
// were written, so editing settings does not reorder or rewrite the user's file.
type Object []member

type member struct {
	key   string
	value json.RawMessage
}

// ParseObject reads a JSON object; empty input is an empty object.
func ParseObject(raw []byte) (Object, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return Object{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil {
		return nil, err
	} else if tok != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	o := Object{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o = append(o, member{key: tok.(string), value: v})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON object")
	}
	return o, nil
}

// Get is the value of key, and false when key is missing.
func (o Object) Get(key string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.key == key {
			return m.value, true
		}
	}
	return nil, false
}

// Str is the value of key when it is a string, else "".
func (o Object) Str(key string) string {
	var s string
	if raw, ok := o.Get(key); ok && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// Set replaces the value of key in place, or appends key.
func (o *Object) Set(key string, v json.RawMessage) {
	for i, m := range *o {
		if m.key == key {
			(*o)[i].value = v
			return
		}
	}
	*o = append(*o, member{key: key, value: v})
}

// Remove removes key.
func (o *Object) Remove(key string) {
	*o = slices.DeleteFunc(*o, func(m member) bool { return m.key == key })
}

// Marshal writes the object back, its values as they were read or set.
func (o Object) Marshal() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(JSONString(m.key))
		b.WriteByte(':')
		b.Write(m.value)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func marshalArray(items []json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(item)
	}
	b.WriteByte(']')
	return b.Bytes()
}

// JSONString encodes s as a JSON string, leaving <, > and & as they are.
func JSONString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// Format indents the settings with two spaces, as Claude Code writes them.
func Format(o Object) ([]byte, error) {
	var b bytes.Buffer
	if err := json.Indent(&b, o.Marshal(), "", "  "); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}
