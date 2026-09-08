package agy

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nxxxsooo/another/internal/util"
)

// annotationsDir holds one protobuf-text file per conversation, named after the
// conversation id. Antigravity moved the title people see out of
// conversation_summaries and into these files: conversations created by current
// builds get an annotation and no summary row at all, so this is the store a
// rename has to reach. Rows written by older builds are still on disk, which is
// why reads prefer the annotation and fall back to the table rather than
// replacing it.
const annotationsDir = "annotations"

func (p *Provider) annotationPath(id string) string {
	return filepath.Join(p.root, annotationsDir, id+".pbtxt")
}

// annotationTitle reads the native title, returning "" when the conversation
// has no annotation or the file carries no title field. Only an unreadable file
// is an error: a shape this build does not recognize means the caller falls
// back to the summary row or the transcript, which is what it did before
// annotations existed.
func (p *Provider) annotationTitle(id string) (string, error) {
	raw, err := os.ReadFile(p.annotationPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	start, end, ok := findAnnotationTitle(raw)
	if !ok {
		return "", nil
	}
	return strings.TrimSpace(unquoteTextProto(raw[start:end])), nil
}

// writeAnnotationTitle replaces the title inside the conversation's annotation
// and keeps every other byte of the file. The file belongs to Antigravity, so a
// field this build has never seen has to survive a rename rather than be
// rewritten away.
func (p *Provider) writeAnnotationTitle(id, title string) error {
	path := p.annotationPath(id)
	mode := os.FileMode(0o600)
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		// Keep whatever permissions Antigravity gave the file it owns.
		if st, statErr := os.Stat(path); statErr == nil {
			mode = st.Mode().Perm()
		}
	case os.IsNotExist(err):
		raw = nil
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
	default:
		return err
	}
	return util.WriteFileAtomic(path, setAnnotationTitle(raw, title), mode)
}

// removeOwnAnnotation deletes the annotation a migration wrote, and only that:
// the file is recognized by still holding exactly the bytes another produced
// for title. Anything else means Antigravity has since written its own
// annotation for a conversation that outlived the migration, and that file is
// not this write's to remove.
func (p *Provider) removeOwnAnnotation(id, title string) error {
	path := p.annotationPath(id)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !bytes.Equal(data, setAnnotationTitle(nil, title)) {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// setAnnotationTitle returns raw with its title field set to title, appending
// the field when the file has none.
func setAnnotationTitle(raw []byte, title string) []byte {
	quoted := quoteTextProto(title)
	if start, end, ok := findAnnotationTitle(raw); ok {
		out := make([]byte, 0, len(raw)-(end-start)+len(quoted))
		out = append(out, raw[:start]...)
		out = append(out, quoted...)
		return append(out, raw[end:]...)
	}
	field := "title:" + quoted
	trimmed := bytes.TrimRight(raw, " \t\r\n")
	if len(bytes.TrimSpace(trimmed)) == 0 {
		return []byte(field)
	}
	out := make([]byte, 0, len(trimmed)+1+len(field))
	out = append(out, trimmed...)
	out = append(out, '\n')
	return append(out, field...)
}

// findAnnotationTitle locates the value of the top-level title field and
// returns the byte range covering its string literals. Nested messages are
// skipped so a title belonging to some future sub-message is never mistaken for
// the conversation's own.
func findAnnotationTitle(raw []byte) (start, end int, ok bool) {
	depth := 0
	for i := 0; i < len(raw); {
		switch c := raw[i]; {
		case c == '"' || c == '\'':
			i = skipQuoted(raw, i)
		case c == '#':
			for i < len(raw) && raw[i] != '\n' {
				i++
			}
		case c == '{' || c == '<' || c == '[':
			depth++
			i++
		case c == '}' || c == '>' || c == ']':
			depth--
			i++
		case isIdentByte(c):
			nameStart := i
			for i < len(raw) && isIdentByte(raw[i]) {
				i++
			}
			name := string(raw[nameStart:i])
			value := skipSpace(raw, i)
			if value >= len(raw) || raw[value] != ':' {
				continue
			}
			value = skipSpace(raw, value+1)
			if depth != 0 || name != "title" || value >= len(raw) || raw[value] != '"' {
				i = value
				continue
			}
			return value, skipQuotedRun(raw, value), true
		default:
			i++
		}
	}
	return 0, 0, false
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '.' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func skipSpace(raw []byte, i int) int {
	for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t' || raw[i] == '\n' || raw[i] == '\r') {
		i++
	}
	return i
}

// skipQuoted returns the index just past the string literal starting at i.
func skipQuoted(raw []byte, i int) int {
	quote := raw[i]
	for i++; i < len(raw); i++ {
		switch raw[i] {
		case '\\':
			i++
		case quote:
			return i + 1
		}
	}
	return len(raw)
}

// skipQuotedRun returns the index just past a run of adjacent string literals.
// Protobuf text concatenates them, so a title split across two literals has to
// be replaced as a whole or the leftover half would survive the rename.
func skipQuotedRun(raw []byte, i int) int {
	end := skipQuoted(raw, i)
	for {
		next := skipSpace(raw, end)
		if next >= len(raw) || raw[next] != '"' {
			return end
		}
		end = skipQuoted(raw, next)
	}
}

// quoteTextProto renders one protobuf-text string literal. UTF-8 is written
// through unescaped, which is the form Antigravity's own annotations are in.
func quoteTextProto(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\%03o`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// unquoteTextProto decodes a run of string literals. An escape this decoder
// does not know is kept verbatim rather than dropped, so an unfamiliar title
// still displays as something recognizable.
func unquoteTextProto(raw []byte) string {
	var b strings.Builder
	inString := false
	for i := 0; i < len(raw); {
		c := raw[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			i++
			continue
		}
		switch c {
		case '"':
			inString = false
			i++
		case '\\':
			i = decodeEscape(raw, i, &b)
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// decodeEscape writes the escape sequence starting at the backslash at i and
// returns the index just past it.
func decodeEscape(raw []byte, i int, b *strings.Builder) int {
	if i+1 >= len(raw) {
		b.WriteByte('\\')
		return i + 1
	}
	switch c := raw[i+1]; c {
	case 'n':
		b.WriteByte('\n')
	case 'r':
		b.WriteByte('\r')
	case 't':
		b.WriteByte('\t')
	case 'a':
		b.WriteByte('\a')
	case 'b':
		b.WriteByte('\b')
	case 'f':
		b.WriteByte('\f')
	case 'v':
		b.WriteByte('\v')
	case '\\', '\'', '"', '?':
		b.WriteByte(c)
	case 'x', 'X':
		return decodeNumericEscape(raw, i+2, 16, 2, b, i)
	case 'u':
		return decodeRuneEscape(raw, i+2, 4, b, i)
	case 'U':
		return decodeRuneEscape(raw, i+2, 8, b, i)
	default:
		if c >= '0' && c <= '7' {
			return decodeNumericEscape(raw, i+1, 8, 3, b, i)
		}
		b.WriteByte('\\')
		b.WriteByte(c)
	}
	return i + 2
}

// decodeNumericEscape writes one byte given in base, consuming at most max
// digits. start is where the digits begin and esc is the backslash, so an
// escape with no digits can be emitted verbatim.
func decodeNumericEscape(raw []byte, start, base, max int, b *strings.Builder, esc int) int {
	end := start
	for end < len(raw) && end-start < max && isDigitInBase(raw[end], base) {
		end++
	}
	if end == start {
		b.WriteByte('\\')
		return esc + 1
	}
	value, err := strconv.ParseUint(string(raw[start:end]), base, 32)
	if err != nil {
		b.WriteByte('\\')
		return esc + 1
	}
	b.WriteByte(byte(value))
	return end
}

// decodeRuneEscape writes the code point of a \u or \U escape.
func decodeRuneEscape(raw []byte, start, digits int, b *strings.Builder, esc int) int {
	if start+digits > len(raw) {
		b.WriteByte('\\')
		return esc + 1
	}
	value, err := strconv.ParseUint(string(raw[start:start+digits]), 16, 32)
	if err != nil {
		b.WriteByte('\\')
		return esc + 1
	}
	b.WriteRune(rune(value))
	return start + digits
}

func isDigitInBase(c byte, base int) bool {
	if base == 8 {
		return c >= '0' && c <= '7'
	}
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
