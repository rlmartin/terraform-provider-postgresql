package postgresql

import (
	"fmt"
	"strings"
)

// triggerSQLToken retains offsets so a WHEN expression can be extracted without
// mistaking parentheses or keywords inside strings, identifiers, or comments.
type triggerSQLToken struct {
	text       string
	kind       byte
	start, end int
}

func triggerSQLTokens(input string) ([]triggerSQLToken, error) {
	var tokens []triggerSQLToken
	for i := 0; i < len(input); {
		start := i
		c := input[i]
		if strings.ContainsRune(" \t\r\n\f", rune(c)) {
			i++
			continue
		}
		if strings.HasPrefix(input[i:], "--") {
			if end := strings.IndexByte(input[i:], '\n'); end >= 0 {
				i += end + 1
			} else {
				i = len(input)
			}
			continue
		}
		if strings.HasPrefix(input[i:], "/*") {
			i += 2
			depth := 1
			for i < len(input) && depth > 0 {
				switch {
				case strings.HasPrefix(input[i:], "/*"):
					depth++
					i += 2
				case strings.HasPrefix(input[i:], "*/"):
					depth--
					i += 2
				default:
					i++
				}
			}
			if depth != 0 {
				return nil, fmt.Errorf("unterminated SQL comment")
			}
			continue
		}
		if c == '$' {
			end := i + 1
			for end < len(input) && triggerIdentifierByte(input[end]) && input[end] != '$' && (end > i+1 || input[end] < '0' || input[end] > '9') {
				end++
			}
			if end < len(input) && input[end] == '$' {
				delimiter := input[i : end+1]
				close := strings.Index(input[end+1:], delimiter)
				if close < 0 {
					return nil, fmt.Errorf("unterminated dollar-quoted string")
				}
				i = end + 1 + close + len(delimiter)
				tokens = append(tokens, triggerSQLToken{input[start:i], 's', start, i})
				continue
			}
		}
		escapedString := (c == 'E' || c == 'e') && i+1 < len(input) && input[i+1] == '\''
		if c == '\'' || c == '"' || escapedString {
			quote := c
			if escapedString {
				i++
				quote = '\''
			}
			i++
			closed := false
			for i < len(input) {
				if escapedString && input[i] == '\\' && i+1 < len(input) {
					i += 2
					continue
				}
				if input[i] == quote {
					i++
					if i < len(input) && input[i] == quote {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated SQL quoted value")
			}
			kind := byte('s')
			if quote == '"' {
				kind = 'q'
			}
			tokens = append(tokens, triggerSQLToken{input[start:i], kind, start, i})
			continue
		}
		if triggerIdentifierByte(c) && (c < '0' || c > '9') {
			i++
			for i < len(input) && triggerIdentifierByte(input[i]) {
				i++
			}
			tokens = append(tokens, triggerSQLToken{input[start:i], 'i', start, i})
			continue
		}
		i++
		tokens = append(tokens, triggerSQLToken{input[start:i], 'p', start, i})
	}
	return tokens, nil
}

func triggerIdentifierByte(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 128
}

func (token triggerSQLToken) identifier() string {
	if token.kind == 'i' {
		return strings.ToLower(token.text)
	}
	if token.kind == 'q' {
		return strings.ReplaceAll(token.text[1:len(token.text)-1], `""`, `"`)
	}
	return ""
}

func validateTriggerCondition(condition, when, forEach string, events []string) error {
	if strings.TrimSpace(condition) == "" {
		return nil
	}
	if when == "INSTEAD OF" {
		return fmt.Errorf("condition is not supported for INSTEAD OF triggers")
	}
	tokens, err := triggerSQLTokens(condition)
	if err != nil {
		return fmt.Errorf("invalid condition: %w", err)
	}
	if len(tokens) == 0 {
		return fmt.Errorf("condition must contain a Boolean expression")
	}
	depth := 0
	for i, token := range tokens {
		switch token.text {
		case "(":
			depth++
		case ")":
			depth--
		case ";":
			return fmt.Errorf("condition must contain a single Boolean expression")
		}
		if depth < 0 {
			return fmt.Errorf("condition has unbalanced parentheses")
		}
		word := token.identifier()
		if token.kind == 'i' && (i == 0 || tokens[i-1].text != ".") && (word == "select" || word == "table" || word == "values") {
			return fmt.Errorf("condition cannot contain subqueries")
		}
		if word != "old" && word != "new" {
			continue
		}
		// A column, type name, or function call named old/new is not a row reference.
		if i > 0 && (tokens[i-1].identifier() == "as" || tokens[i-1].identifier() == "collate") ||
			i >= 2 && tokens[i-1].text == ":" && tokens[i-2].text == ":" {
			continue
		}
		if i > 0 && tokens[i-1].text == "." || i+1 < len(tokens) && tokens[i+1].text == "(" ||
			i+3 < len(tokens) && tokens[i+1].text == "." && tokens[i+3].text == "(" {
			continue
		}
		if forEach != "ROW" {
			return fmt.Errorf("condition cannot reference %s in a STATEMENT trigger", strings.ToUpper(word))
		}
		if word == "old" && sliceContainsStr(events, "INSERT") {
			return fmt.Errorf("condition cannot reference OLD when event includes INSERT")
		}
		if word == "new" && sliceContainsStr(events, "DELETE") {
			return fmt.Errorf("condition cannot reference NEW when event includes DELETE")
		}
		if word == "new" && when == "BEFORE" {
			j := i + 1
			for j < len(tokens) && tokens[j].text == ")" {
				j++
			}
			if j+1 < len(tokens) && tokens[j].text == "." && sliceContainsStr([]string{"ctid", "oid", "xmin", "cmin", "xmax", "cmax", "tableoid"}, tokens[j+1].identifier()) {
				return fmt.Errorf("condition cannot reference NEW system columns in a BEFORE trigger")
			}
		}
	}
	if depth != 0 {
		return fmt.Errorf("condition has unbalanced parentheses")
	}
	return nil
}

func triggerConditionFromDefinition(definition string) (string, error) {
	tokens, err := triggerSQLTokens(definition)
	if err != nil {
		return "", err
	}
	for i, token := range tokens {
		if token.kind != 'i' || token.identifier() != "when" {
			continue
		}
		if i+1 >= len(tokens) || tokens[i+1].text != "(" {
			return "", fmt.Errorf("invalid WHEN clause in trigger definition")
		}
		depth := 0
		for _, part := range tokens[i+1:] {
			if part.text == "(" {
				depth++
			}
			if part.text == ")" {
				depth--
				if depth == 0 {
					return strings.TrimSpace(definition[tokens[i+1].end:part.start]), nil
				}
			}
		}
		return "", fmt.Errorf("unterminated WHEN clause in trigger definition")
	}
	return "", nil
}

// Compare tokens, preserving parentheses and literal contents. Only outer
// parentheses, whitespace, comments, and unquoted identifier case are ignored.
func triggerConditionsEqual(a, b string) bool {
	normalize := func(input string) ([]triggerSQLToken, error) {
		tokens, err := triggerSQLTokens(input)
		if err != nil {
			return nil, err
		}
		for len(tokens) >= 2 && tokens[0].text == "(" && tokens[len(tokens)-1].text == ")" {
			depth, outer := 0, true
			for i, token := range tokens {
				if token.text == "(" {
					depth++
				}
				if token.text == ")" {
					depth--
				}
				if depth == 0 && i != len(tokens)-1 {
					outer = false
					break
				}
			}
			if !outer {
				break
			}
			tokens = tokens[1 : len(tokens)-1]
		}
		return tokens, nil
	}
	left, err := normalize(a)
	if err != nil {
		return a == b
	}
	right, err := normalize(b)
	if err != nil || len(left) != len(right) {
		return a == b
	}
	for i := range left {
		if left[i].kind == 'i' && right[i].kind == 'i' {
			if left[i].identifier() != right[i].identifier() {
				return false
			}
		} else if left[i].text != right[i].text || left[i].kind != right[i].kind {
			return false
		}
	}
	return true
}
