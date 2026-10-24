// SPDX-License-Identifier: Apache-2.0

package assets

import (
	"bytes"
	"encoding/xml"
	"io"
	"regexp"
	"strings"
)

// catalog holds GameData XML entries of some classes (CModel, CActorUnit, ...)
// with the game's inheritance rules: an entry inherits from its parent
// (templates are marked default="1"), then from its class's default entry;
// later files override fields of earlier ones; ##token## placeholders take
// values from <?token?> declarations in templates, attributes on children,
// and ##id## from the entry being resolved.
type catalog struct {
	entries map[string]*entry // class + "/" + id; "" id is the class default
}

type entry struct {
	class, id, parent string
	isDefault         bool // a template (default="1")
	tokens            map[string]string
	// Fields of child elements: "Model" for <Model value=.../>, and
	// "DeathArray[Normal].ModelLink" for attributes of indexed elements.
	fields map[string]string
	// Every value of fields that repeat without an index, in order
	// (<RequiredAnims value=.../> twice).
	lists map[string][]string
	// Terms of <On ... Send="Create"/> events: the events that create the
	// actor itself.
	creates []string
}

func newCatalog() *catalog { return &catalog{entries: map[string]*entry{}} }

var reservedAttrs = map[string]bool{"id": true, "parent": true, "default": true}

// add reads one GameData file, keeping entries whose class passes keep.
func (c *catalog) add(data []byte, keep func(class string) bool) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	// Files declare us-ascii or utf-8; both read fine as UTF-8.
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	var cur *entry
	depth := 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch {
			case depth == 2 && keep(t.Name.Local):
				var id, parent string
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "id":
						id = a.Value
					case "parent":
						parent = a.Value
					}
				}
				isDefault := false
				for _, a := range t.Attr {
					if a.Name.Local == "default" && a.Value == "1" {
						isDefault = true
					}
				}
				key := t.Name.Local + "/" + id
				cur = c.entries[key]
				if cur == nil {
					cur = &entry{class: t.Name.Local, id: id, tokens: map[string]string{}, fields: map[string]string{}, lists: map[string][]string{}}
					c.entries[key] = cur
				}
				if parent != "" {
					cur.parent = parent
				}
				cur.isDefault = cur.isDefault || isDefault
				for _, a := range t.Attr {
					if !reservedAttrs[a.Name.Local] {
						cur.tokens[a.Name.Local] = a.Value
					}
				}
			case depth == 3 && cur != nil:
				name, index := t.Name.Local, ""
				for _, a := range t.Attr {
					if a.Name.Local == "index" {
						index = a.Value
					}
				}
				if index != "" {
					name += "[" + index + "]"
				}
				if name == "On" {
					var terms, send string
					for _, a := range t.Attr {
						switch a.Name.Local {
						case "Terms":
							terms = a.Value
						case "Send":
							send = a.Value
						}
					}
					if send == "Create" {
						cur.creates = append(cur.creates, terms)
					}
					continue
				}
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "index":
					case "value":
						cur.fields[name] = a.Value
						if index == "" {
							cur.lists[name] = append(cur.lists[name], a.Value)
						}
					default:
						cur.fields[name+"."+a.Name.Local] = a.Value
					}
				}
			}
		case xml.EndElement:
			if depth == 2 {
				cur = nil
			}
			depth--
		case xml.ProcInst:
			if cur != nil && t.Target == "token" {
				if m := tokenRe.FindSubmatch(t.Inst); m != nil {
					if _, set := cur.tokens[string(m[1])]; !set {
						cur.tokens[string(m[1])] = string(m[2])
					}
				}
			}
		}
	}
}

var tokenRe = regexp.MustCompile(`id="([^"]*)"\s+value="([^"]*)"`)

// chain is the entry and its ancestors, nearest first.
func (c *catalog) chain(class, id string) []*entry {
	var out []*entry
	seen := map[string]bool{}
	e := c.entries[class+"/"+id]
	for e != nil && !seen[e.id] && len(out) < 32 {
		seen[e.id] = true
		out = append(out, e)
		if e.parent == "" {
			break
		}
		e = c.entries[class+"/"+e.parent]
	}
	if len(out) == 0 {
		return nil
	}
	if def := c.entries[class+"/"]; def != nil && !seen[""] {
		out = append(out, def)
	}
	return out
}

// field resolves a field of an entry with its tokens substituted; ok is
// false when the entry does not exist or nothing in its chain sets the field.
func (c *catalog) field(class, id, name string) (string, bool) {
	ch := c.chain(class, id)
	if len(ch) == 0 {
		return "", false
	}
	val, found := "", false
	for _, e := range ch {
		if v, ok := e.fields[name]; ok {
			val, found = v, true
			break
		}
	}
	if !found {
		return "", false
	}
	return subst(ch, id, val), true
}

// list resolves a repeated field: the values the nearest entry in the chain
// declares, tokens substituted.
func (c *catalog) list(class, id, name string) []string {
	ch := c.chain(class, id)
	for _, e := range ch {
		if vs := e.lists[name]; len(vs) > 0 {
			out := make([]string, len(vs))
			for i, v := range vs {
				out[i] = subst(ch, id, v)
			}
			return out
		}
	}
	return nil
}

// subst replaces ##token## placeholders with the values a chain defines.
func subst(ch []*entry, id, val string) string {
	tokens := map[string]string{}
	for i := len(ch) - 1; i >= 0; i-- { // nearest wins
		for k, v := range ch[i].tokens {
			tokens[k] = v
		}
	}
	tokens["id"] = id
	for i := 0; i < 4 && strings.Contains(val, "##"); i++ {
		val = placeholderRe.ReplaceAllStringFunc(val, func(s string) string {
			if v, ok := tokens[s[2:len(s)-2]]; ok {
				return v
			}
			return s
		})
	}
	return val
}

// createdBy lists the unit types whose birth, creation or morph creates an
// actor, from its own and its templates' events.
func (c *catalog) createdBy(class, id string) []string {
	ch := c.chain(class, id)
	var out []string
	for _, e := range ch {
		for _, terms := range e.creates {
			for _, term := range strings.Split(subst(ch, id, terms), ";") {
				term = strings.TrimSpace(term)
				for _, p := range []string{"UnitBirth.", "UnitCreation.", "MorphTo "} {
					if rest, ok := strings.CutPrefix(term, p); ok {
						name, _, _ := strings.Cut(rest, ".")
						if safeName.MatchString(name) {
							out = append(out, name)
						}
					}
				}
			}
		}
	}
	return out
}

var placeholderRe = regexp.MustCompile(`##[A-Za-z0-9_]+##`)

func (c *catalog) has(class, id string) bool { return c.entries[class+"/"+id] != nil }
