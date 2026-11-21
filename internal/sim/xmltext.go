package sim

import (
	"encoding/xml"
	"strconv"
	"strings"
)

// xnode is a parsed XML element, for XMLTEXT.
type xnode struct {
	name  string
	attrs map[string]string
	text  strings.Builder
	kids  []*xnode
}

func parseXNode(s string) *xnode {
	dec := xml.NewDecoder(strings.NewReader(s))
	root := &xnode{name: "#root"}
	stack := []*xnode{root}
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			n := &xnode{name: t.Name.Local, attrs: map[string]string{}}
			for _, a := range t.Attr {
				n.attrs[a.Name.Local] = a.Value
			}
			top.kids = append(top.kids, n)
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			top.text.Write(t)
		}
	}
	return root
}

// xmlText evaluates vMix's XMLTEXT paths: steps of name, name[n] (1-based)
// or name[@attr='v'], and a final @attr. E.g. vmix/inputs/input[2]/@title.
func xmlText(doc, path string) (string, bool) {
	cur := []*xnode{parseXNode(doc)}
	steps := strings.Split(strings.Trim(strings.TrimSpace(path), "/"), "/")
	for i, st := range steps {
		if strings.HasPrefix(st, "@") {
			if i != len(steps)-1 || len(cur) == 0 {
				return "", false
			}
			v, ok := cur[0].attrs[st[1:]]
			return v, ok
		}
		name, pred, _ := strings.Cut(st, "[")
		pred = strings.TrimSuffix(pred, "]")
		var next []*xnode
		for _, c := range cur {
			for _, k := range c.kids {
				if k.name == name {
					next = append(next, k)
				}
			}
		}
		if pred != "" {
			if n, err := strconv.Atoi(pred); err == nil {
				if n < 1 || n > len(next) {
					return "", false
				}
				next = next[n-1 : n]
			} else if a, v, ok := strings.Cut(strings.TrimPrefix(pred, "@"), "="); ok {
				v = strings.Trim(v, `'"`)
				var keep []*xnode
				for _, k := range next {
					if k.attrs[a] == v {
						keep = append(keep, k)
					}
				}
				next = keep
			}
		}
		if len(next) == 0 {
			return "", false
		}
		cur = next
	}
	return strings.TrimSpace(cur[0].text.String()), true
}
