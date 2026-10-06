package gameconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
)

// node is a JSON value that remembers the order of object keys.
type node struct {
	kind byte // o object, a array, s string, n number, b bool, z null
	keys []string
	vals []*node
	str  string
	raw  string // numbers and bools as written
}

func parseOrdered(data []byte) (*node, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	n, err := readNode(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("Daten nach dem Ende")
	}
	return n, nil
}

func readNode(dec *json.Decoder) (*node, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch v := t.(type) {
	case json.Delim:
		switch v {
		case '{':
			n := &node{kind: 'o'}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, _ := kt.(string)
				c, err := readNode(dec)
				if err != nil {
					return nil, err
				}
				n.keys = append(n.keys, k)
				n.vals = append(n.vals, c)
			}
			_, err := dec.Token()
			return n, err
		case '[':
			n := &node{kind: 'a'}
			for dec.More() {
				c, err := readNode(dec)
				if err != nil {
					return nil, err
				}
				n.vals = append(n.vals, c)
			}
			_, err := dec.Token()
			return n, err
		}
	case string:
		return &node{kind: 's', str: v}, nil
	case json.Number:
		return &node{kind: 'n', raw: v.String()}, nil
	case bool:
		return &node{kind: 'b', raw: strconv.FormatBool(v)}, nil
	case nil:
		return &node{kind: 'z'}, nil
	}
	return nil, errors.New("unerwartetes Zeichen")
}

func (n *node) find(path []string) *node {
	cur := n
	for _, p := range path {
		switch cur.kind {
		case 'o':
			var next *node
			for i, k := range cur.keys {
				if k == p {
					next = cur.vals[i]
				}
			}
			if next == nil {
				return nil
			}
			cur = next
		case 'a':
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(cur.vals) {
				return nil
			}
			cur = cur.vals[i]
		default:
			return nil
		}
	}
	return cur
}

func (n *node) write(b *bytes.Buffer, pre, ind string) {
	switch n.kind {
	case 'o':
		if len(n.keys) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, k := range n.keys {
			kb, _ := json.Marshal(k)
			b.WriteString(pre + ind)
			b.Write(kb)
			b.WriteString(": ")
			n.vals[i].write(b, pre+ind, ind)
			if i < len(n.keys)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(pre + "}")
	case 'a':
		if len(n.vals) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, v := range n.vals {
			b.WriteString(pre + ind)
			v.write(b, pre+ind, ind)
			if i < len(n.vals)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(pre + "]")
	case 's':
		var sb strings.Builder
		enc := json.NewEncoder(&sb)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(n.str)
		b.WriteString(strings.TrimSuffix(sb.String(), "\n"))
	case 'n', 'b':
		b.WriteString(n.raw)
	case 'z':
		b.WriteString("null")
	}
}
