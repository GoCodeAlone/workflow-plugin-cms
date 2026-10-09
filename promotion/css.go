package promotion

import "strings"

// This is deliberately a restricted CSS boundary, not a general stylesheet
// parser. Escapes and alternative string-image functions are unsupported and
// refused instead of silently missing a browser-resolved resource reference.
func verifyCSSReferences(source string, check func(string) error) error {
	space := func(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' }
	ident := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c >= 128
	}
	// Preserve strings while replacing comments with whitespace. A backslash
	// anywhere outside a comment is outside the supported grammar.
	var clean strings.Builder
	var quote byte
	for i := 0; i < len(source); i++ {
		c := source[i]
		if quote == 0 && c == '/' && i+1 < len(source) && source[i+1] == '*' {
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				return ErrBundle
			}
			i += end + 3
			clean.WriteByte(' ')
			continue
		}
		if c == '\\' || c == 0 {
			return ErrBundle
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			} else if c == '\r' || c == '\n' || c == '\f' {
				return ErrBundle
			}
		} else if c == '\'' || c == '"' {
			quote = c
		}
		clean.WriteByte(c)
	}
	if quote != 0 {
		return ErrBundle
	}
	css := clean.String()
	skip := func(i int) int {
		for i < len(css) && space(css[i]) {
			i++
		}
		return i
	}
	quoted := func(i int) (string, int, error) {
		q := css[i]
		start := i + 1
		i = start
		for i < len(css) && css[i] != q {
			i++
		}
		if i == len(css) {
			return "", i, ErrBundle
		}
		return css[start:i], i + 1, nil
	}
	for i := 0; i < len(css); {
		if css[i] == '\'' || css[i] == '"' {
			_, next, err := quoted(i)
			if err != nil {
				return err
			}
			i = next
			continue
		}
		at := css[i] == '@'
		if at {
			i++
			if i == len(css) {
				return ErrBundle
			}
		}
		if !ident(css[i]) {
			i++
			continue
		}
		start := i
		for i < len(css) && ident(css[i]) {
			i++
		}
		name := strings.ToLower(css[start:i])
		next := skip(i)
		if at && name == "import" {
			if next == len(css) {
				return ErrBundle
			}
			if css[next] == '\'' || css[next] == '"' {
				ref, end, err := quoted(next)
				if err != nil {
					return err
				}
				if err = check(strings.TrimSpace(ref)); err != nil {
					return err
				}
				i = end
				continue
			}
			// Only a URL function may supply the other supported import form.
			if !strings.HasPrefix(strings.ToLower(css[next:]), "url(") {
				return ErrBundle
			}
			i = next
			continue
		}
		if next >= len(css) || css[next] != '(' {
			continue
		}
		switch name {
		case "image", "image-set", "-webkit-image-set", "src":
			return ErrBundle
		case "url":
			j := skip(next + 1)
			if j == len(css) {
				return ErrBundle
			}
			var ref string
			if css[j] == '\'' || css[j] == '"' {
				var err error
				ref, j, err = quoted(j)
				if err != nil {
					return err
				}
				j = skip(j)
			} else {
				start := j
				for j < len(css) && css[j] != ')' {
					j++
				}
				ref = strings.TrimSpace(css[start:j])
				if strings.ContainsAny(ref, "('\" \t\r\n\f") {
					return ErrBundle
				}
			}
			if j == len(css) || css[j] != ')' {
				return ErrBundle
			}
			if err := check(strings.TrimSpace(ref)); err != nil {
				return err
			}
			i = j + 1
		}
	}
	return nil
}
