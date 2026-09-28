package helper

import (
	"fmt"
	"strings"
	"unicode"
)

// ExtractKeysInOrder parses a GraphQL-style query string and returns the
// leaf response keys, in order.
//
// Two rules beyond a plain field listing:
//  1. The root field (e.g. "list") is unwrapped and dropped — it isn't
//     emitted as a key, and its children become the top-level keys.
//  2. Any other field with a nested selection set (a "container", e.g.
//     "outlet") is likewise not emitted on its own — instead, each of
//     its children is emitted as "container.child" (dot notation).
//
// Only leaf (scalar) fields ever end up in the returned slice.
func ExtractKeysInOrder(query string) ([]string, error) {
	stripped := stripArguments(query)
	tokens := tokenize(stripped)
	p := &StringParser{tokens: tokens}

	// Skip to the first '{' that opens the top-level selection set.
	if err := p.expect("{"); err != nil {
		return nil, err
	}

	var keys []string
	if err := p.parseSelectionSet("", true, &keys); err != nil {
		return nil, err
	}
	return keys, nil
}

// stripArguments removes every "(...)" argument block — including any
// nested parens, braces, or quoted strings inside it — leaving only the
// bare selection-set structure ({ field { field, field } , ... }) behind.
// This is what lets the tokenizer/StringParser ignore orderBy/where/etc.
func stripArguments(s string) string {
	var b strings.Builder
	depth := 0
	inString := false

	for i := 0; i < len(s); i++ {
		c := s[i]

		if inString {
			if depth == 0 {
				b.WriteByte(' ')
			}
			if c == '"' && (i == 0 || s[i-1] != '\\') {
				inString = false
			}
			continue
		}

		switch c {
		case '"':
			if depth == 0 {
				b.WriteByte(c)
			}
			inString = true
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}

// tokenize splits the argument-stripped query into '{', '}', ':', ','
// and identifier tokens.
func tokenize(s string) []string {
	var tokens []string
	var cur strings.Builder

	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}

	for _, r := range s {
		switch {
		case r == '{' || r == '}' || r == ':' || r == ',':
			flush()
			tokens = append(tokens, string(r))
		case unicode.IsSpace(r):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return tokens
}

type StringParser struct {
	tokens []string
	pos    int
}

func (p *StringParser) peek() string {
	if p.pos >= len(p.tokens) {
		return ""
	}
	return p.tokens[p.pos]
}

func (p *StringParser) next() string {
	t := p.peek()
	p.pos++
	return t
}

func (p *StringParser) expect(tok string) error {
	if p.peek() != tok {
		return fmt.Errorf("expected %q at token %d, got %q", tok, p.pos, p.peek())
	}
	p.pos++
	return nil
}

// parseSelectionSet reads fields until the matching '}'.
//
// prefix is prepended (as "prefix.") to every leaf key emitted at this
// level. root is true only for the outermost selection set — a
// container field found there is unwrapped with no prefix at all
// (rule 1); a container found at any deeper level gets dot-prefixed
// instead of being emitted itself (rule 2).
func (p *StringParser) parseSelectionSet(prefix string, root bool, keys *[]string) error {
	for {
		tok := p.peek()

		if tok == "}" {
			p.pos++
			return nil
		}
		if tok == "," {
			p.pos++
			continue
		}
		if tok == "" {
			return fmt.Errorf("unexpected end of input inside selection set")
		}

		name := p.next() // field name, or alias if followed by ':'
		key := name

		if p.peek() == ":" {
			p.pos++  // consume ':'
			p.next() // consume the real field name (alias wins as the key)
		}

		if p.peek() == "{" {
			p.pos++
			var childPrefix string
			if root {
				childPrefix = "" // drop the root field entirely, no prefix
			} else {
				childPrefix = prefix + key + "."
			}
			if err := p.parseSelectionSet(childPrefix, false, keys); err != nil {
				return err
			}
			continue
		}

		// Leaf field: emit with whatever prefix this level carries.
		*keys = append(*keys, prefix+key)
	}
}

// Example usage:
//
//	query := `{list:fieldVisits(orderBy:{createdAt:DESC},where:{projectId:{equalTo:"6a3ccb3293a7ee7d637157b1"}}){ outlet { name, phone, region, district, ward, category, tradeType, retailerType, pictureUrl, }, productAvailability, brandAwareness, notes, photoUrl, region, district, ward, latitude, longitude, fieldOfficer { name, phone }, timestamp: createdAt }}`
//
//	keys, err := ExtractKeysInOrder(query)
//	if err != nil {
//		// handle err
//	}
//	// keys == []string{
//	//   "outlet.name", "outlet.phone", "outlet.region", "outlet.district",
//	//   "outlet.ward", "outlet.category", "outlet.tradeType", "outlet.retailerType",
//	//   "outlet.pictureUrl", "productAvailability", "brandAwareness", "notes",
//	//   "photoUrl", "region", "district", "ward", "latitude", "longitude",
//	//   "fieldOfficer.name", "fieldOfficer.phone", "timestamp",
//	// }
