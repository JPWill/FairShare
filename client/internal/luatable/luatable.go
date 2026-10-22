// Package luatable parses the Lua table literals that World of Warcraft writes to
// SavedVariables files. It is deliberately tolerant: it understands nested tables,
// strings with Lua escapes, numbers, booleans, nil, bracketed and bare keys,
// positional entries, and both comment forms. It does not execute Lua.
package luatable

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Value is one of: string, float64, bool, nil, *Table.
type Value interface{}

// Table is a parsed Lua table. String keys and numeric keys are kept separately;
// positional entries (no explicit key) are numbered from 1 as Lua does.
type Table struct {
	str map[string]Value
	num map[float64]Value
}

func newTable() *Table {
	return &Table{str: map[string]Value{}, num: map[float64]Value{}}
}

// Get returns the value under a string key, or nil.
func (t *Table) Get(key string) Value {
	if t == nil {
		return nil
	}
	return t.str[key]
}

// GetTable returns the sub-table under a string key, or nil if absent or not a table.
func (t *Table) GetTable(key string) *Table {
	sub, _ := t.Get(key).(*Table)
	return sub
}

// GetString returns the string under key, or "" if absent or not a string.
func (t *Table) GetString(key string) string {
	s, _ := t.Get(key).(string)
	return s
}

// GetNumber returns the number under key and whether it was present as a number.
func (t *Table) GetNumber(key string) (float64, bool) {
	n, ok := t.Get(key).(float64)
	return n, ok
}

// Index returns the value at numeric index i (1-based), or nil.
func (t *Table) Index(i int) Value {
	if t == nil {
		return nil
	}
	return t.num[float64(i)]
}

// Len returns the largest numeric key that is a positive integer, like Lua's # for
// sequences without holes.
func (t *Table) Len() int {
	if t == nil {
		return 0
	}
	n := 0
	for k := range t.num {
		if k == float64(int(k)) && int(k) > n {
			n = int(k)
		}
	}
	return n
}

// StringKeys returns the table's string keys in sorted order.
func (t *Table) StringKeys() []string {
	if t == nil {
		return nil
	}
	keys := make([]string, 0, len(t.str))
	for k := range t.str {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Strings returns the positional entries 1..Len() that are strings, in order.
func (t *Table) Strings() []string {
	n := t.Len()
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		if s, ok := t.Index(i).(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Parse reads a SavedVariables file body: zero or more `Name = value` assignments.
// Returns the globals by name.
func Parse(src string) (map[string]Value, error) {
	p := &parser{src: src}
	globals := map[string]Value{}
	for {
		p.skipSpace()
		if p.eof() {
			return globals, nil
		}
		name, ok := p.readName()
		if !ok {
			return nil, p.errorf("expected a global name")
		}
		p.skipSpace()
		if !p.accept('=') {
			return nil, p.errorf("expected '=' after %s", name)
		}
		v, err := p.readValue()
		if err != nil {
			return nil, err
		}
		globals[name] = v
		p.skipSpace()
		p.accept(';')
	}
}

type parser struct {
	src  string
	pos  int
	line int
}

func (p *parser) errorf(format string, args ...interface{}) error {
	return fmt.Errorf("luatable: line %d: %s", p.line+1, fmt.Sprintf(format, args...))
}

func (p *parser) eof() bool { return p.pos >= len(p.src) }

func (p *parser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.src[p.pos]
}

func (p *parser) accept(c byte) bool {
	if p.peek() == c {
		p.pos++
		return true
	}
	return false
}

// skipSpace skips whitespace and both comment forms.
func (p *parser) skipSpace() {
	for !p.eof() {
		c := p.src[p.pos]
		switch {
		case c == '\n':
			p.line++
			p.pos++
		case c == ' ' || c == '\t' || c == '\r':
			p.pos++
		case c == '-' && strings.HasPrefix(p.src[p.pos:], "--"):
			p.pos += 2
			if strings.HasPrefix(p.src[p.pos:], "[[") {
				end := strings.Index(p.src[p.pos+2:], "]]")
				if end < 0 {
					p.pos = len(p.src)
					return
				}
				p.line += strings.Count(p.src[p.pos:p.pos+2+end], "\n")
				p.pos += 2 + end + 2
			} else {
				for !p.eof() && p.src[p.pos] != '\n' {
					p.pos++
				}
			}
		default:
			return
		}
	}
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isNameChar(c byte) bool {
	return isNameStart(c) || (c >= '0' && c <= '9')
}

func (p *parser) readName() (string, bool) {
	if !isNameStart(p.peek()) {
		return "", false
	}
	start := p.pos
	for !p.eof() && isNameChar(p.src[p.pos]) {
		p.pos++
	}
	return p.src[start:p.pos], true
}

func (p *parser) readValue() (Value, error) {
	p.skipSpace()
	switch c := p.peek(); {
	case c == '{':
		return p.readTable()
	case c == '"' || c == '\'':
		return p.readString()
	case c == '[' && strings.HasPrefix(p.src[p.pos:], "[["):
		return p.readLongString()
	case c == '-' || (c >= '0' && c <= '9') || c == '.':
		return p.readNumber()
	case isNameStart(c):
		name, _ := p.readName()
		switch name {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "nil":
			return nil, nil
		}
		return nil, p.errorf("unexpected identifier %q", name)
	}
	return nil, p.errorf("unexpected character %q", string(p.peek()))
}

func (p *parser) readTable() (Value, error) {
	if !p.accept('{') {
		return nil, p.errorf("expected '{'")
	}
	t := newTable()
	next := 1.0
	for {
		p.skipSpace()
		if p.accept('}') {
			return t, nil
		}
		if p.eof() {
			return nil, p.errorf("unterminated table")
		}
		var key Value
		hasKey := false
		if p.peek() == '[' && !strings.HasPrefix(p.src[p.pos:], "[[") {
			p.pos++
			k, err := p.readValue()
			if err != nil {
				return nil, err
			}
			p.skipSpace()
			if !p.accept(']') {
				return nil, p.errorf("expected ']' after key")
			}
			p.skipSpace()
			if !p.accept('=') {
				return nil, p.errorf("expected '=' after key")
			}
			key, hasKey = k, true
		} else if isNameStart(p.peek()) {
			// Either a bare key (name = value) or a bare value (true/false/nil).
			save, saveLine := p.pos, p.line
			name, _ := p.readName()
			p.skipSpace()
			if p.accept('=') {
				key, hasKey = name, true
			} else {
				p.pos, p.line = save, saveLine
			}
		}
		v, err := p.readValue()
		if err != nil {
			return nil, err
		}
		if hasKey {
			switch k := key.(type) {
			case string:
				t.str[k] = v
			case float64:
				t.num[k] = v
			default:
				return nil, p.errorf("unsupported key type %T", key)
			}
		} else {
			t.num[next] = v
			next++
		}
		p.skipSpace()
		if !p.accept(',') && !p.accept(';') {
			p.skipSpace()
			if !p.accept('}') {
				return nil, p.errorf("expected ',' or '}' in table")
			}
			return t, nil
		}
	}
}

func (p *parser) readString() (Value, error) {
	quote := p.src[p.pos]
	p.pos++
	var b strings.Builder
	for {
		if p.eof() {
			return nil, p.errorf("unterminated string")
		}
		c := p.src[p.pos]
		switch {
		case c == quote:
			p.pos++
			return b.String(), nil
		case c == '\\':
			p.pos++
			if p.eof() {
				return nil, p.errorf("unterminated escape")
			}
			e := p.src[p.pos]
			switch e {
			case 'n':
				b.WriteByte('\n')
				p.pos++
			case 't':
				b.WriteByte('\t')
				p.pos++
			case 'r':
				b.WriteByte('\r')
				p.pos++
			case 'a':
				b.WriteByte('\a')
				p.pos++
			case 'b':
				b.WriteByte('\b')
				p.pos++
			case 'f':
				b.WriteByte('\f')
				p.pos++
			case 'v':
				b.WriteByte('\v')
				p.pos++
			case '\\', '"', '\'':
				b.WriteByte(e)
				p.pos++
			case '\n':
				b.WriteByte('\n')
				p.line++
				p.pos++
			default:
				if e >= '0' && e <= '9' {
					// \ddd decimal escape, up to three digits.
					n, digits := 0, 0
					for digits < 3 && !p.eof() && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
						n = n*10 + int(p.src[p.pos]-'0')
						p.pos++
						digits++
					}
					if n > 255 {
						return nil, p.errorf("escape \\%d out of range", n)
					}
					b.WriteByte(byte(n))
				} else {
					return nil, p.errorf("unknown escape \\%c", e)
				}
			}
		case c == '\n':
			return nil, p.errorf("newline in string")
		default:
			b.WriteByte(c)
			p.pos++
		}
	}
}

func (p *parser) readLongString() (Value, error) {
	// [[ ... ]] with an optional leading newline that Lua drops.
	p.pos += 2
	end := strings.Index(p.src[p.pos:], "]]")
	if end < 0 {
		return nil, p.errorf("unterminated long string")
	}
	s := p.src[p.pos : p.pos+end]
	p.line += strings.Count(s, "\n")
	p.pos += end + 2
	s = strings.TrimPrefix(s, "\r\n")
	s = strings.TrimPrefix(s, "\n")
	return s, nil
}

func (p *parser) readNumber() (Value, error) {
	start := p.pos
	if p.peek() == '-' {
		p.pos++
	}
	if strings.HasPrefix(p.src[p.pos:], "0x") || strings.HasPrefix(p.src[p.pos:], "0X") {
		p.pos += 2
		for !p.eof() && strings.IndexByte("0123456789abcdefABCDEF", p.src[p.pos]) >= 0 {
			p.pos++
		}
		n, err := strconv.ParseInt(strings.TrimPrefix(p.src[start:p.pos], "-"), 0, 64)
		if err != nil {
			return nil, p.errorf("bad hex number %q", p.src[start:p.pos])
		}
		if p.src[start] == '-' {
			n = -n
		}
		return float64(n), nil
	}
	for !p.eof() && strings.IndexByte("0123456789.eE+-", p.src[p.pos]) >= 0 {
		// Stop at a '-' or '+' that is not an exponent sign.
		c := p.src[p.pos]
		if (c == '-' || c == '+') && p.pos > start {
			prev := p.src[p.pos-1]
			if prev != 'e' && prev != 'E' {
				break
			}
		}
		p.pos++
	}
	text := p.src[start:p.pos]
	n, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil, p.errorf("bad number %q", text)
	}
	return n, nil
}
