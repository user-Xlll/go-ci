// Package replacereader provides a reader that transparent replace text in input.
package replacereader

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

// OnReplace - callback function that called on replace.
type RelaceEvent func(lineNum int, oldValue, newValue string, rule *Rule)

// Rule - replace regular expression and new value template.
type Rule struct {
	Re       *regexp.Regexp
	Template string
}

type replaceReader struct {
	*bufio.Scanner
	rules      []Rule
	buf        []byte
	lineNumber int
	onReplace  RelaceEvent
}

type match struct {
	ixs []int
	*Rule
}

var ErrReplaceConflict = errors.New("replace conflict")

func (s *replaceReader) replaceAll(line string) (string, error) {
	var matches []match

	for _, rule := range s.rules {
		for _, ixs := range rule.Re.FindAllStringSubmatchIndex(line, -1) {
			matches = append(matches, match{ixs: ixs, Rule: &rule})
		}
	}

	switch len(matches) {
	case 0:
		return line, nil
	case 1:
	default:
		slices.SortFunc(matches, func(a, b match) int {
			return a.ixs[0] - b.ixs[0]
		})

		for i1, m1 := range matches {
			for i2, m2 := range matches {
				if i1 != i2 && m1.ixs[0] >= m2.ixs[0] && m1.ixs[0] <= m2.ixs[len(m2.ixs)-1] {
					return "", fmt.Errorf("%w: string %s, re1: %v, re2: %v", ErrReplaceConflict, line, m1.Re, m2.Re)
				}
			}
		}
	}

	var (
		res      strings.Builder
		delta    int
		tailFrom int
	)

	for _, m := range matches {
		res.WriteString(line[tailFrom:m.ixs[0]])
		newValue := string(m.Re.ExpandString(nil, m.Template, line, m.ixs))
		n, _ := res.WriteString(newValue)
		delta += n - (m.ixs[len(m.ixs)-1] - m.ixs[0])
		tailFrom = m.ixs[len(m.ixs)-1]
		if s.onReplace != nil {
			s.onReplace(
				s.lineNumber,
				line[m.ixs[0]:m.ixs[len(m.ixs)-1]],
				newValue,
				m.Rule,
			)
		}

	}

	res.WriteString(line[tailFrom:])

	return res.String(), nil
}

func (s *replaceReader) readFromBuffer(dst *[]byte) int {
	n := copy(*dst, s.buf)

	if n > 0 {
		s.buf = s.buf[n:]
	}

	*dst = (*dst)[n:]

	return n
}

func (s *replaceReader) Read(dst []byte) (int, error) {
	var n int
	for {
		n += s.readFromBuffer(&dst)

		if len(dst) == 0 {
			return n, nil
		}

		if s.Scan() {
			s.lineNumber++
			line := s.Text()
			newLine, err := s.replaceAll(line)
			if err != nil {
				return n, err
			}

			if s.lineNumber > 1 {
				s.buf = make([]byte, 0, len(newLine)+1)
				s.buf = append(s.buf, '\n')
				s.buf = append(s.buf, newLine...)
			} else {
				s.buf = []byte(newLine)
			}

			n += s.readFromBuffer(&dst)
		} else {

			if err := s.Err(); err != nil {
				return n, err
			}

			if n > 0 {
				return n, nil
			}

			return 0, io.EOF
		}
	}
}

// NewReplaceReader - returns new reader that transparent replace text in input.
func NewReplaceReader(raw io.Reader, rules []Rule, onReplace RelaceEvent) io.Reader {
	return &replaceReader{
		Scanner:   bufio.NewScanner(raw),
		rules:     rules,
		onReplace: onReplace,
	}
}
