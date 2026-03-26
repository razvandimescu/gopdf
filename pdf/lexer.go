package pdf

import (
	"fmt"
	"strconv"
	"strings"
)

// TokenType identifies the kind of PDF token.
type TokenType int

const (
	TNumber TokenType = iota
	TString
	THexString
	TName
	TKeyword
	TBool
	TNull
	TArrayStart
	TArrayEnd
	TDictStart
	TDictEnd
	TEOF
)

// Token is a single lexical unit from a PDF byte stream.
type Token struct {
	Type TokenType
	Str  string  // raw string value
	Num  float64 // numeric value (for TNumber)
	Int  int     // integer value (for TNumber when integral)
	IsInt bool   // whether this number is integral
}

// Lexer tokenizes a PDF byte stream.
type Lexer struct {
	data []byte
	pos  int
}

func NewLexer(data []byte) *Lexer {
	return &Lexer{data: data}
}

func (l *Lexer) Pos() int     { return l.pos }
func (l *Lexer) SetPos(p int) { l.pos = p }
func (l *Lexer) AtEnd() bool  { return l.pos >= len(l.data) }

func (l *Lexer) peek() byte {
	if l.pos >= len(l.data) {
		return 0
	}
	return l.data[l.pos]
}

func (l *Lexer) read() byte {
	if l.pos >= len(l.data) {
		return 0
	}
	b := l.data[l.pos]
	l.pos++
	return b
}

func isWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\f' || b == 0
}

func isDelimiter(b byte) bool {
	return b == '(' || b == ')' || b == '<' || b == '>' ||
		b == '[' || b == ']' || b == '{' || b == '}' ||
		b == '/' || b == '%'
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func (l *Lexer) skipWhitespaceAndComments() {
	for l.pos < len(l.data) {
		b := l.data[l.pos]
		if isWhitespace(b) {
			l.pos++
			continue
		}
		if b == '%' {
			// Skip comment to end of line.
			for l.pos < len(l.data) && l.data[l.pos] != '\r' && l.data[l.pos] != '\n' {
				l.pos++
			}
			continue
		}
		break
	}
}

// NextToken returns the next token from the stream.
func (l *Lexer) NextToken() (Token, error) {
	l.skipWhitespaceAndComments()
	if l.AtEnd() {
		return Token{Type: TEOF}, nil
	}

	b := l.peek()

	switch {
	case b == '[':
		l.pos++
		return Token{Type: TArrayStart, Str: "["}, nil

	case b == ']':
		l.pos++
		return Token{Type: TArrayEnd, Str: "]"}, nil

	case b == '<':
		l.pos++
		if l.pos < len(l.data) && l.data[l.pos] == '<' {
			l.pos++
			return Token{Type: TDictStart, Str: "<<"}, nil
		}
		return l.readHexString()

	case b == '>':
		l.pos++
		if l.pos < len(l.data) && l.data[l.pos] == '>' {
			l.pos++
			return Token{Type: TDictEnd, Str: ">>"}, nil
		}
		return Token{}, fmt.Errorf("unexpected '>' at pos %d", l.pos-1)

	case b == '(':
		return l.readLiteralString()

	case b == '/':
		return l.readName()

	case b == '+' || b == '-' || b == '.' || isDigit(b):
		return l.readNumber()

	default:
		return l.readKeyword()
	}
}

func (l *Lexer) readLiteralString() (Token, error) {
	l.pos++ // skip (
	var buf strings.Builder
	depth := 1
	for l.pos < len(l.data) {
		b := l.read()
		switch b {
		case '(':
			depth++
			buf.WriteByte('(')
		case ')':
			depth--
			if depth == 0 {
				return Token{Type: TString, Str: buf.String()}, nil
			}
			buf.WriteByte(')')
		case '\\':
			if l.pos >= len(l.data) {
				break
			}
			esc := l.read()
			switch esc {
			case 'n':
				buf.WriteByte('\n')
			case 'r':
				buf.WriteByte('\r')
			case 't':
				buf.WriteByte('\t')
			case 'b':
				buf.WriteByte('\b')
			case 'f':
				buf.WriteByte('\f')
			case '(', ')', '\\':
				buf.WriteByte(esc)
			case '\r':
				// line continuation
				if l.pos < len(l.data) && l.data[l.pos] == '\n' {
					l.pos++
				}
			case '\n':
				// line continuation
			default:
				// Octal
				if esc >= '0' && esc <= '7' {
					oct := int(esc - '0')
					for i := 0; i < 2 && l.pos < len(l.data) && l.data[l.pos] >= '0' && l.data[l.pos] <= '7'; i++ {
						oct = oct*8 + int(l.read()-'0')
					}
					buf.WriteByte(byte(oct))
				} else {
					buf.WriteByte(esc)
				}
			}
		default:
			buf.WriteByte(b)
		}
	}
	return Token{}, fmt.Errorf("unterminated string")
}

func (l *Lexer) readHexString() (Token, error) {
	var hex strings.Builder
	for l.pos < len(l.data) {
		b := l.data[l.pos]
		if b == '>' {
			l.pos++
			s := hex.String()
			if len(s)%2 != 0 {
				s += "0"
			}
			// Decode hex to bytes.
			var buf []byte
			for i := 0; i+1 < len(s); i += 2 {
				val, err := strconv.ParseUint(s[i:i+2], 16, 8)
				if err != nil {
					return Token{}, fmt.Errorf("invalid hex in string: %w", err)
				}
				buf = append(buf, byte(val))
			}
			return Token{Type: THexString, Str: string(buf)}, nil
		}
		if isWhitespace(b) {
			l.pos++
			continue
		}
		if isHexDigit(b) {
			hex.WriteByte(b)
			l.pos++
			continue
		}
		return Token{}, fmt.Errorf("invalid hex digit '%c' at pos %d", b, l.pos)
	}
	return Token{}, fmt.Errorf("unterminated hex string")
}

func (l *Lexer) readName() (Token, error) {
	l.pos++ // skip /
	var buf strings.Builder
	for l.pos < len(l.data) {
		b := l.data[l.pos]
		if isWhitespace(b) || isDelimiter(b) {
			break
		}
		if b == '#' && l.pos+2 < len(l.data) {
			// Hex escape in name.
			val, err := strconv.ParseUint(string(l.data[l.pos+1:l.pos+3]), 16, 8)
			if err == nil {
				buf.WriteByte(byte(val))
				l.pos += 3
				continue
			}
		}
		buf.WriteByte(b)
		l.pos++
	}
	return Token{Type: TName, Str: buf.String()}, nil
}

func (l *Lexer) readNumber() (Token, error) {
	start := l.pos
	if l.data[l.pos] == '+' || l.data[l.pos] == '-' {
		l.pos++
	}
	hasDecimal := false
	for l.pos < len(l.data) {
		b := l.data[l.pos]
		if b == '.' && !hasDecimal {
			hasDecimal = true
			l.pos++
			continue
		}
		if isDigit(b) {
			l.pos++
			continue
		}
		break
	}
	s := string(l.data[start:l.pos])
	if !hasDecimal {
		n, err := strconv.Atoi(s)
		if err != nil {
			return Token{}, fmt.Errorf("invalid integer %q: %w", s, err)
		}
		return Token{Type: TNumber, Str: s, Num: float64(n), Int: n, IsInt: true}, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return Token{}, fmt.Errorf("invalid float %q: %w", s, err)
	}
	return Token{Type: TNumber, Str: s, Num: f, Int: int(f)}, nil
}

func (l *Lexer) readKeyword() (Token, error) {
	start := l.pos
	for l.pos < len(l.data) {
		b := l.data[l.pos]
		if isWhitespace(b) || isDelimiter(b) {
			break
		}
		l.pos++
	}
	s := string(l.data[start:l.pos])
	switch s {
	case "true":
		return Token{Type: TBool, Str: s}, nil
	case "false":
		return Token{Type: TBool, Str: s}, nil
	case "null":
		return Token{Type: TNull, Str: s}, nil
	default:
		return Token{Type: TKeyword, Str: s}, nil
	}
}
