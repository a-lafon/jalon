package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// SetThresholds returns data, a config file's content or nil when there is none,
// with context.advise_tokens and context.advise_step_tokens set to the values
// that are not nil. It edits the text where the YAML node tree places each key,
// so the other keys, the comments and the layout stay as written, and appends a
// context section when there is none. The result must pass Load's validation,
// whose error names the invalid field; path only names the file in the errors.
func SetThresholds(path string, data []byte, adviseTokens, adviseStepTokens *int) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	root, err := rootMapping(&doc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	type threshold struct{ key, value string }
	var set []threshold
	for _, t := range []struct {
		key   string
		value *int
	}{{"advise_tokens", adviseTokens}, {"advise_step_tokens", adviseStepTokens}} {
		if t.value != nil {
			set = append(set, threshold{t.key, strconv.Itoa(*t.value)})
		}
	}

	text := string(data)
	lines := strings.SplitAfter(text, "\n")
	var missing []string // "key: value" entries the section lacks
	key, section := lookup(root, "context")
	switch {
	case section == nil:
		for _, t := range set {
			missing = append(missing, t.key+": "+t.value)
		}
		if text != "" {
			text = strings.TrimSuffix(text, "\n") + "\n\n"
		}
		text += "context:\n" + entries(missing, "  ")
	case section.Kind == yaml.MappingNode:
		for _, t := range set {
			_, value := lookup(section, t.key)
			if value == nil {
				missing = append(missing, t.key+": "+t.value)
				continue
			}
			if !replaceScalar(lines, value, t.value) {
				return nil, fmt.Errorf("%s: cannot edit context.%s in place: set it by hand", path, t.key)
			}
		}
		if len(missing) > 0 {
			if section.Style&yaml.FlowStyle != 0 {
				return nil, fmt.Errorf("%s: cannot add to a context section written as {...}: set the thresholds by hand", path)
			}
			lastKey, lastValue := section.Content[len(section.Content)-2], section.Content[len(section.Content)-1]
			lines = insertAfter(lines, lastValue.Line, entries(missing, strings.Repeat(" ", lastKey.Column-1)))
		}
		text = strings.Join(lines, "")
	case section.Kind == yaml.ScalarNode && section.Tag == "!!null" && section.Value == "":
		// A context key with nothing under it.
		for _, t := range set {
			missing = append(missing, t.key+": "+t.value)
		}
		lines = insertAfter(lines, key.Line, entries(missing, strings.Repeat(" ", key.Column+1)))
		text = strings.Join(lines, "")
	default:
		return nil, fmt.Errorf("%s: context is not a mapping", path)
	}

	next := []byte(text)
	cfg, err := Parse(path, next)
	if err != nil {
		return nil, err
	}
	if adviseTokens != nil && cfg.Context.AdviseTokens != *adviseTokens ||
		adviseStepTokens != nil && cfg.Context.AdviseStepTokens != *adviseStepTokens {
		return nil, fmt.Errorf("%s: the edit did not set the thresholds: set them by hand", path)
	}
	return next, nil
}

// ContextSection returns the context section of a config file's content as
// YAML, or nothing when there is none.
func ContextSection(data []byte) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", err
	}
	root, err := rootMapping(&doc)
	if err != nil {
		return "", err
	}
	_, section := lookup(root, "context")
	if section == nil {
		return "", nil
	}
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(&yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{{Kind: yaml.ScalarNode, Value: "context"}, section}}); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// rootMapping returns the document's top-level mapping, or nil for a document
// with no node, such as an empty file or one holding only comments.
func rootMapping(doc *yaml.Node) (*yaml.Node, error) {
	if len(doc.Content) == 0 {
		return nil, nil
	}
	if root := doc.Content[0]; root.Kind == yaml.MappingNode {
		return root, nil
	}
	return nil, errors.New("the top level is not a mapping")
}

// lookup returns the key and value nodes of key in the mapping m, or nils.
func lookup(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if m == nil {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

// replaceScalar replaces, in lines, the text of the plain scalar n with value.
// It reports false when n is not a plain scalar found where the parser put it.
func replaceScalar(lines []string, n *yaml.Node, value string) bool {
	if n.Kind != yaml.ScalarNode || n.Style != 0 || n.Line < 1 || n.Line > len(lines) {
		return false
	}
	line := lines[n.Line-1]
	start := runeOffset(line, n.Column-1) // the parser counts columns in characters
	if start < 0 || !strings.HasPrefix(line[start:], n.Value) {
		return false
	}
	lines[n.Line-1] = line[:start] + value + line[start+len(n.Value):]
	return true
}

// runeOffset returns the byte offset of the nth character of s, or -1 when s is
// shorter.
func runeOffset(s string, n int) int {
	for i := range s {
		if n == 0 {
			return i
		}
		n--
	}
	if n == 0 {
		return len(s)
	}
	return -1
}

// insertAfter inserts text after line n, 1-based, of lines.
func insertAfter(lines []string, n int, text string) []string {
	if !strings.HasSuffix(lines[n-1], "\n") {
		lines[n-1] += "\n"
	}
	return append(lines[:n], append([]string{text}, lines[n:]...)...)
}

// entries renders "key: value" entries as lines indented by indent.
func entries(kv []string, indent string) string {
	var b strings.Builder
	for _, e := range kv {
		b.WriteString(indent + e + "\n")
	}
	return b.String()
}
