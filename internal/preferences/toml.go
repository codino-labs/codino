package preferences

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The config file is a small subset of TOML, read and edited line by line so
// that `codino set` changes only the value it sets and keeps the user's
// comments and layout: comments, blank lines, [table] headers and
// `key = value` lines, where a key is bare, "quoted" or 'literal' (dotted
// keys too), and a value is a one-line string, an integer or a boolean.
// Anything else (arrays, floats, dates, inline tables, multi-line strings) is
// valid TOML that no Preference needs, and is reported as unsupported.

// lineKind is what one line of the file is.
type lineKind int

const (
	trivia   lineKind = iota // blank or a comment
	header                   // [table]
	keyValue                 // key = value
)

type valueKind int

const (
	stringValue valueKind = iota
	integerValue
	boolValue
)

// value is a TOML value of a key = value line.
type value struct {
	kind valueKind
	text string // stringValue
	num  int64  // integerValue
	flag bool   // boolValue
}

// line is one parsed line of the file.
type line struct {
	text string // as in the file, without its newline
	kind lineKind
	// path is the table a header opens, or the full key (table included) of
	// a key = value line.
	path []string
	// The value's bytes in text, for a key = value line.
	valueStart, valueEnd int
	value                value
}

// document is the file, line by line.
type document struct {
	lines []line
}

// lineError is a problem on one line of the file.
func lineError(n int, format string, args ...any) error {
	return fmt.Errorf("line %d: %s", n, fmt.Sprintf(format, args...))
}

// parseDocument splits the file into lines and parses each. It checks the
// TOML subset only; what the keys mean is the Preferences' business.
func parseDocument(data string) (document, error) {
	if !utf8.ValidString(data) {
		return document{}, errors.New("the file is not UTF-8")
	}
	var doc document
	var table []string
	for i, text := range strings.Split(data, "\n") {
		l, err := parseLine(strings.TrimSuffix(text, "\r"), table)
		if err != nil {
			return document{}, lineError(i+1, "%v", err)
		}
		l.text = text
		if l.kind == header {
			table = l.path
		}
		doc.lines = append(doc.lines, l)
	}
	return doc, nil
}

// cursor reads one line.
type cursor struct {
	s   string
	pos int
}

func (c *cursor) skipSpace() {
	for c.pos < len(c.s) && (c.s[c.pos] == ' ' || c.s[c.pos] == '\t') {
		c.pos++
	}
}

func (c *cursor) done() bool { return c.pos >= len(c.s) }

func (c *cursor) peek() byte {
	if c.done() {
		return 0
	}
	return c.s[c.pos]
}

// endOfLine accepts what may follow a header or value: spaces and a comment.
func (c *cursor) endOfLine() error {
	c.skipSpace()
	if c.done() || c.peek() == '#' {
		return nil
	}
	return fmt.Errorf("unexpected %q after the value", c.s[c.pos:])
}

func parseLine(text string, table []string) (line, error) {
	c := &cursor{s: text}
	c.skipSpace()
	if c.done() || c.peek() == '#' {
		return line{kind: trivia}, nil
	}
	if c.peek() == '[' {
		c.pos++
		if c.peek() == '[' {
			return line{}, errors.New("arrays of tables are not supported")
		}
		c.skipSpace()
		path, err := c.key()
		if err != nil {
			return line{}, err
		}
		c.skipSpace()
		if c.peek() != ']' {
			return line{}, errors.New("a table header ends with ]")
		}
		c.pos++
		if err := c.endOfLine(); err != nil {
			return line{}, err
		}
		return line{kind: header, path: path}, nil
	}
	key, err := c.key()
	if err != nil {
		return line{}, err
	}
	c.skipSpace()
	if c.peek() != '=' {
		return line{}, errors.New("want key = value")
	}
	c.pos++
	c.skipSpace()
	start := c.pos
	v, err := c.value()
	if err != nil {
		return line{}, err
	}
	end := c.pos
	if err := c.endOfLine(); err != nil {
		return line{}, err
	}
	path := append(append([]string(nil), table...), key...)
	return line{kind: keyValue, path: path, valueStart: start, valueEnd: end, value: v}, nil
}

// key reads a key: bare, quoted or literal parts joined by dots.
func (c *cursor) key() ([]string, error) {
	var parts []string
	for {
		c.skipSpace()
		var part string
		switch ch := c.peek(); {
		case ch == '"':
			s, err := c.basicString()
			if err != nil {
				return nil, err
			}
			part = s
		case ch == '\'':
			s, err := c.literalString()
			if err != nil {
				return nil, err
			}
			part = s
		default:
			start := c.pos
			for !c.done() && isBare(c.peek()) {
				c.pos++
			}
			if c.pos == start {
				return nil, errors.New("want a key")
			}
			part = c.s[start:c.pos]
		}
		parts = append(parts, part)
		c.skipSpace()
		if c.peek() != '.' {
			return parts, nil
		}
		c.pos++
	}
}

func isBare(ch byte) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-'
}

func (c *cursor) value() (value, error) {
	switch ch := c.peek(); {
	case ch == 0:
		return value{}, errors.New("want a value after =")
	case strings.HasPrefix(c.s[c.pos:], `"""`), strings.HasPrefix(c.s[c.pos:], `'''`):
		return value{}, errors.New("multi-line strings are not supported")
	case ch == '"':
		s, err := c.basicString()
		return value{kind: stringValue, text: s}, err
	case ch == '\'':
		s, err := c.literalString()
		return value{kind: stringValue, text: s}, err
	case ch == '[', ch == '{':
		return value{}, errors.New("arrays and inline tables are not supported: give a string, an integer or true/false")
	}
	start := c.pos
	for !c.done() && c.peek() != ' ' && c.peek() != '\t' && c.peek() != '#' {
		c.pos++
	}
	word := c.s[start:c.pos]
	switch word {
	case "true":
		return value{kind: boolValue, flag: true}, nil
	case "false":
		return value{kind: boolValue, flag: false}, nil
	}
	if n, ok := parseInteger(word); ok {
		return value{kind: integerValue, num: n}, nil
	}
	return value{}, fmt.Errorf("unsupported value %q: give a \"string\", an integer or true/false", word)
}

// parseInteger reads a TOML decimal integer: an optional sign, digits, and
// underscores between digits.
func parseInteger(word string) (int64, bool) {
	digits := strings.TrimLeft(word, "+-")
	if len(word)-len(digits) > 1 || digits == "" || digits[0] == '_' || digits[len(digits)-1] == '_' ||
		strings.Contains(digits, "__") || (len(digits) > 1 && digits[0] == '0') {
		return 0, false
	}
	for _, ch := range digits {
		if (ch < '0' || ch > '9') && ch != '_' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(word, "_", ""), 10, 64)
	return n, err == nil
}

// basicString reads a "string" with TOML's escapes.
func (c *cursor) basicString() (string, error) {
	c.pos++ // the opening quote
	var b strings.Builder
	for {
		if c.done() {
			return "", errors.New("unterminated string")
		}
		ch := c.s[c.pos]
		c.pos++
		switch {
		case ch == '"':
			return b.String(), nil
		case ch == '\\':
			if c.done() {
				return "", errors.New("unterminated string")
			}
			esc := c.s[c.pos]
			c.pos++
			switch esc {
			case 'b':
				b.WriteByte('\b')
			case 't':
				b.WriteByte('\t')
			case 'n':
				b.WriteByte('\n')
			case 'f':
				b.WriteByte('\f')
			case 'r':
				b.WriteByte('\r')
			case 'e':
				b.WriteByte(0x1b)
			case '"', '\\':
				b.WriteByte(esc)
			case 'u', 'U':
				n := 4
				if esc == 'U' {
					n = 8
				}
				if c.pos+n > len(c.s) {
					return "", errors.New("short unicode escape")
				}
				r, err := strconv.ParseUint(c.s[c.pos:c.pos+n], 16, 32)
				if err != nil || !utf8.ValidRune(rune(r)) {
					return "", fmt.Errorf("bad unicode escape \\%c%s", esc, c.s[c.pos:c.pos+n])
				}
				c.pos += n
				b.WriteRune(rune(r))
			default:
				return "", fmt.Errorf("unknown escape \\%c", esc)
			}
		case ch < 0x20 && ch != '\t', ch == 0x7f:
			return "", errors.New("control character in a string")
		default:
			b.WriteByte(ch)
		}
	}
}

// literalString reads a 'string': no escapes.
func (c *cursor) literalString() (string, error) {
	c.pos++
	end := strings.IndexByte(c.s[c.pos:], '\'')
	if end < 0 {
		return "", errors.New("unterminated string")
	}
	s := c.s[c.pos : c.pos+end]
	c.pos += end + 1
	return s, nil
}

// quote renders s as a TOML basic string.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// formatKey renders one key part: bare when it can be, quoted otherwise.
func formatKey(part string) string {
	if part == "" {
		return quote(part)
	}
	for i := 0; i < len(part); i++ {
		if !isBare(part[i]) {
			return quote(part)
		}
	}
	return part
}

// format renders a value as TOML.
func (v value) format() string {
	switch v.kind {
	case integerValue:
		return strconv.FormatInt(v.num, 10)
	case boolValue:
		return strconv.FormatBool(v.flag)
	default:
		return quote(v.text)
	}
}

// text joins the lines back into the file.
func (d document) text() string {
	texts := make([]string, len(d.lines))
	for i, l := range d.lines {
		texts[i] = l.text
	}
	return strings.Join(texts, "\n")
}

// newline is the line ending the file uses.
func (d document) newline() string {
	if len(d.lines) > 0 && strings.HasSuffix(d.lines[0].text, "\r") {
		return "\r"
	}
	return ""
}

// find returns the index of the key = value line for the key at path (the
// same when same(a, b)), or -1.
func (d document) find(path []string, same func(a, b []string) bool) int {
	for i, l := range d.lines {
		if l.kind == keyValue && same(l.path, path) {
			return i
		}
	}
	return -1
}

// replaceValue sets the value of line i, keeping the rest of its text.
func (d *document) replaceValue(i int, v value) {
	l := d.lines[i]
	l.text = l.text[:l.valueStart] + v.format() + l.text[l.valueEnd:]
	l.valueEnd = l.valueStart + len(v.format())
	l.value = v
	d.lines[i] = l
}

func (d *document) remove(i int) {
	d.lines = append(d.lines[:i], d.lines[i+1:]...)
}

// insert puts texts as new lines before index i.
func (d *document) insert(i int, texts ...string) {
	added := make([]line, len(texts))
	for j, t := range texts {
		added[j] = line{text: t + d.newline()}
	}
	d.lines = append(d.lines[:i], append(added, d.lines[i:]...)...)
}

// end is where lines go at the end of the file: before the empty "line" a
// final newline leaves.
func (d document) end() int {
	if n := len(d.lines); n > 0 && strings.TrimSuffix(d.lines[n-1].text, "\r") == "" {
		return n - 1
	}
	return len(d.lines)
}

// add puts a new `key = value` line for path where it belongs: a top-level
// key after the last top-level one; a key of a table after the last line of
// its table, or in a new table at the end of the file.
func (d *document) add(path []string, v value) {
	isHeader := func(l line, table []string) bool {
		return l.kind == header && slices.Equal(l.path, table)
	}
	if len(path) == 1 {
		kv := formatKey(path[0]) + " = " + v.format()
		last, firstHeader := -1, -1
		for i, l := range d.lines {
			if l.kind == header && firstHeader < 0 {
				firstHeader = i
			}
			if l.kind == keyValue && firstHeader < 0 {
				last = i
			}
		}
		switch {
		case last >= 0:
			d.insert(last+1, kv)
		case firstHeader >= 0:
			d.insert(firstHeader, kv, "")
		default:
			d.insert(d.end(), kv)
		}
		return
	}
	table, key := path[:len(path)-1], path[len(path)-1]
	kv := formatKey(key) + " = " + v.format()
	for i, l := range d.lines {
		if !isHeader(l, table) {
			continue
		}
		last := i
		for j := i + 1; j < len(d.lines) && d.lines[j].kind != header; j++ {
			if d.lines[j].kind == keyValue {
				last = j
			}
		}
		d.insert(last+1, kv)
		return
	}
	names := make([]string, len(table))
	for i, part := range table {
		names[i] = formatKey(part)
	}
	at := d.end()
	texts := []string{"[" + strings.Join(names, ".") + "]", kv}
	if at > 0 && strings.TrimSpace(d.lines[at-1].text) != "" {
		texts = append([]string{""}, texts...)
	}
	d.insert(at, texts...)
}
