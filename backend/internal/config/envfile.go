package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ReadEnvFile parses a dotenv-style file into a map. Supported syntax:
// blank lines, full-line comments ("#"), an optional "export " prefix,
// KEY=value, double-quoted values with \n \t \" \\ escapes, single-quoted raw
// values, and unquoted values whose trailing " # comment" is stripped.
// No variable expansion is performed. The file is read with the process
// permissions; the caller decides whether a missing file is an error.
func ReadEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from the operator's --env-file flag
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseEnv(f)
}

func parseEnv(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("env file line %d: expected KEY=value", lineNo)
		}
		key := strings.TrimSpace(line[:eq])
		if !envKeyRe.MatchString(key) {
			return nil, fmt.Errorf("env file line %d: invalid key %q", lineNo, key)
		}
		val, err := parseEnvValue(strings.TrimSpace(line[eq+1:]))
		if err != nil {
			return nil, fmt.Errorf("env file line %d: %w", lineNo, err)
		}
		out[key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func parseEnvValue(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	switch v[0] {
	case '"':
		var b strings.Builder
		for i := 1; i < len(v); i++ {
			c := v[i]
			switch c {
			case '\\':
				if i+1 >= len(v) {
					return "", fmt.Errorf("unterminated escape in quoted value")
				}
				i++
				switch v[i] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				case 'r':
					b.WriteByte('\r')
				case '"', '\\', '$':
					b.WriteByte(v[i])
				default:
					b.WriteByte('\\')
					b.WriteByte(v[i])
				}
			case '"':
				return b.String(), nil
			default:
				b.WriteByte(c)
			}
		}
		return "", fmt.Errorf("unterminated double-quoted value")
	case '\'':
		end := strings.IndexByte(v[1:], '\'')
		if end < 0 {
			return "", fmt.Errorf("unterminated single-quoted value")
		}
		return v[1 : 1+end], nil
	default:
		// "KEY=   # comment" → empty value (leading spaces were trimmed).
		if strings.HasPrefix(v, "#") {
			return "", nil
		}
		// Strip an inline comment: a '#' preceded by whitespace.
		if i := strings.Index(v, " #"); i >= 0 {
			v = v[:i]
		} else if i := strings.Index(v, "\t#"); i >= 0 {
			v = v[:i]
		}
		return strings.TrimSpace(v), nil
	}
}
