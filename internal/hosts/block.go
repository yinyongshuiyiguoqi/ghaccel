package hosts

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

const (
	canonicalBegin = "# >>> ghaccel managed block"
	canonicalEnd   = "# <<< ghaccel managed block"
)

var ErrMalformedBlock = errors.New("malformed ghaccel block")

type Markers struct {
	Begin string
	End   string
}

type span struct {
	start int
	end   int // exclusive, includes the line ending
}

func stripBOM(b []byte) ([]byte, []byte) {
	if bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}) {
		return b[:3], b[3:]
	}
	return nil, b
}

func detectEOL(b []byte) string {
	if bytes.Contains(b, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

func splitLines(b []byte) []span {
	var out []span
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			out = append(out, span{start: start, end: i + 1})
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, span{start: start, end: len(b)})
	}
	return out
}

func lineText(b []byte, s span) string {
	return strings.TrimRight(string(b[s.start:s.end]), "\r\n")
}

func isBegin(line string, m Markers) bool {
	t := strings.TrimSpace(line)
	return t == strings.TrimSpace(m.Begin) || strings.HasPrefix(t, canonicalBegin)
}

func isEnd(line string, m Markers) bool {
	t := strings.TrimSpace(line)
	return t == strings.TrimSpace(m.End) || strings.HasPrefix(t, canonicalEnd)
}

func findBlock(b []byte, m Markers) (begin, end int, found bool, err error) {
	spans := splitLines(b)
	begins, ends := []int{}, []int{}
	for i, s := range spans {
		line := lineText(b, s)
		if isBegin(line, m) {
			begins = append(begins, i)
		}
		if isEnd(line, m) {
			ends = append(ends, i)
		}
	}
	if len(begins) == 0 && len(ends) == 0 {
		return 0, 0, false, nil
	}
	if len(begins) != 1 || len(ends) != 1 || begins[0] > ends[0] {
		return 0, 0, false, fmt.Errorf("%w: duplicate or orphan markers", ErrMalformedBlock)
	}
	return begins[0], ends[0], true, nil
}

// Splice replaces or appends the managed block. Bytes outside the block are
// preserved exactly. Domains already mapped by the user outside the block are
// skipped (default) so we never fight an existing hosts entry.
func Splice(original []byte, mappings map[string]string, m Markers, conflictPolicy string) ([]byte, []string, error) {
	bom, body := stripBOM(original)
	spans := splitLines(body)
	begin, end, found, err := findBlock(body, m)
	if err != nil {
		return nil, nil, err
	}

	work := make(map[string]string, len(mappings))
	for k, v := range mappings {
		work[strings.ToLower(k)] = v
	}
	skipped := []string{}
	if conflictPolicy != "warn" {
		existing := existingHosts(body, spans, begin, end, found)
		for domain := range work {
			if existing[domain] {
				skipped = append(skipped, domain)
				delete(work, domain)
			}
		}
	}
	sort.Strings(skipped)

	eol := detectEOL(body)
	block := renderBlock(work, m, eol)
	if !found && len(block) == 0 {
		return original, skipped, nil
	}

	var out []byte
	switch {
	case found:
		out = make([]byte, 0, len(body)+len(block))
		out = append(out, body[:spans[begin].start]...)
		out = append(out, block...)
		out = append(out, body[spans[end].end:]...)
	default:
		out = make([]byte, 0, len(body)+len(block)+2)
		out = append(out, body...)
		if len(body) > 0 {
			trimmed := bytes.TrimRight(body, "\r\n")
			if len(trimmed) == 0 {
				// File is only line endings; start clean.
				out = out[:0]
			} else {
				trailing := len(body) - len(trimmed)
				switch {
				case trailing == 0:
					out = append(out, eol...)
					out = append(out, eol...)
				case trailing == 1:
					out = append(out, eol...)
				}
			}
		}
		out = append(out, block...)
	}
	if bom != nil {
		out = append(append([]byte{}, bom...), out...)
	}
	return out, skipped, nil
}

func renderBlock(mappings map[string]string, m Markers, eol string) []byte {
	if len(mappings) == 0 {
		return nil
	}
	names := make([]string, 0, len(mappings))
	for d := range mappings {
		names = append(names, d)
	}
	sort.Strings(names)
	var b bytes.Buffer
	b.WriteString(m.Begin)
	b.WriteString(eol)
	for _, d := range names {
		b.WriteString(mappings[d])
		b.WriteByte(' ')
		b.WriteString(d)
		b.WriteString(eol)
	}
	b.WriteString(m.End)
	b.WriteString(eol)
	return b.Bytes()
}

func existingHosts(body []byte, spans []span, begin, end int, found bool) map[string]bool {
	out := make(map[string]bool)
	for i, s := range spans {
		if found && i >= begin && i <= end {
			continue
		}
		line := strings.TrimSpace(lineText(body, s))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if _, err := netip.ParseAddr(fields[0]); err != nil {
			continue
		}
		for _, h := range fields[1:] {
			if strings.HasPrefix(h, "#") {
				break
			}
			out[strings.ToLower(h)] = true
		}
	}
	return out
}

// Remove deletes the managed block and returns whether anything changed.
func Remove(original []byte, m Markers) ([]byte, bool, error) {
	bom, body := stripBOM(original)
	spans := splitLines(body)
	begin, end, found, err := findBlock(body, m)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return original, false, nil
	}

	start := spans[begin].start
	stop := spans[end].end
	// If we appended the block after a separator blank line, remove it too.
	if begin > 0 {
		prev := spans[begin-1]
		if strings.TrimSpace(lineText(body, prev)) == "" && stop == len(body) {
			start = prev.start
		}
	}
	out := make([]byte, 0, len(body))
	out = append(out, body[:start]...)
	out = append(out, body[stop:]...)
	if bom != nil {
		out = append(append([]byte{}, bom...), out...)
	}
	return out, true, nil
}

// Mappings returns the entries inside the managed block.
func Mappings(original []byte, m Markers) (map[string]string, bool, error) {
	_, body := stripBOM(original)
	spans := splitLines(body)
	begin, end, found, err := findBlock(body, m)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	out := make(map[string]string)
	for i := begin + 1; i < end; i++ {
		line := strings.TrimSpace(lineText(body, spans[i]))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if _, err := netip.ParseAddr(fields[0]); err != nil {
			continue
		}
		out[strings.ToLower(fields[1])] = fields[0]
	}
	return out, true, nil
}
