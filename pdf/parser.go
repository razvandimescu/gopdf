package pdf

import (
	"fmt"
)

// Parser reads PDF objects from a Lexer.
type Parser struct {
	lex *Lexer
}

func NewParser(data []byte) *Parser {
	return &Parser{lex: NewLexer(data)}
}

func (p *Parser) Lexer() *Lexer { return p.lex }

// ParseObject reads the next complete PDF object (value) from the stream.
// It handles indirect references (N G R) by lookahead.
func (p *Parser) ParseObject() (any, error) {
	tok, err := p.lex.NextToken()
	if err != nil {
		return nil, err
	}
	return p.parseFromToken(tok)
}

func (p *Parser) parseFromToken(tok Token) (any, error) {
	switch tok.Type {
	case TEOF:
		return nil, fmt.Errorf("unexpected EOF")

	case TNumber:
		// Could be start of indirect reference: num gen R
		if tok.IsInt {
			savedPos := p.lex.Pos()
			tok2, err := p.lex.NextToken()
			if err == nil && tok2.Type == TNumber && tok2.IsInt {
				tok3, err := p.lex.NextToken()
				if err == nil && tok3.Type == TKeyword && tok3.Str == "R" {
					return Ref{Num: tok.Int, Gen: tok2.Int}, nil
				}
			}
			p.lex.SetPos(savedPos)
		}
		if tok.IsInt {
			return tok.Int, nil
		}
		return tok.Num, nil

	case TString:
		return tok.Str, nil

	case THexString:
		return tok.Str, nil

	case TName:
		return Name(tok.Str), nil

	case TBool:
		return tok.Str == "true", nil

	case TNull:
		return nil, nil

	case TArrayStart:
		return p.parseArray()

	case TDictStart:
		return p.parseDict()

	case TKeyword:
		// Return keywords as-is for content stream parsing.
		return tok.Str, nil

	default:
		return nil, fmt.Errorf("unexpected token type %d: %q", tok.Type, tok.Str)
	}
}

func (p *Parser) parseArray() (Array, error) {
	var arr Array
	for {
		tok, err := p.lex.NextToken()
		if err != nil {
			return nil, err
		}
		if tok.Type == TArrayEnd {
			return arr, nil
		}
		if tok.Type == TEOF {
			return nil, fmt.Errorf("unterminated array")
		}
		obj, err := p.parseFromToken(tok)
		if err != nil {
			return nil, err
		}
		arr = append(arr, obj)
	}
}

func (p *Parser) parseDict() (Dict, error) {
	d := make(Dict)
	for {
		tok, err := p.lex.NextToken()
		if err != nil {
			return nil, err
		}
		if tok.Type == TDictEnd {
			return d, nil
		}
		if tok.Type == TEOF {
			return nil, fmt.Errorf("unterminated dict")
		}
		if tok.Type != TName {
			return nil, fmt.Errorf("expected name key in dict, got %d: %q", tok.Type, tok.Str)
		}
		key := Name(tok.Str)
		val, err := p.ParseObject()
		if err != nil {
			return nil, fmt.Errorf("parsing dict value for /%s: %w", key, err)
		}
		d[key] = val
	}
}
