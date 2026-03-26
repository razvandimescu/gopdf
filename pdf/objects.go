package pdf

import (
	"fmt"
)

// Name is a PDF name object (e.g., /Type).
type Name string

// Ref is an indirect object reference (e.g., 5 0 R).
type Ref struct{ Num, Gen int }

// Dict is a PDF dictionary.
type Dict map[Name]any

// Array is a PDF array.
type Array []any

// Stream is a PDF stream object (dictionary + raw bytes).
type Stream struct {
	Dict Dict
	Data []byte
}

// Helper accessors for Dict.

func (d Dict) Name(key Name) (Name, bool) {
	v, ok := d[key]
	if !ok {
		return "", false
	}
	n, ok := v.(Name)
	return n, ok
}

func (d Dict) Dict(key Name) (Dict, bool) {
	v, ok := d[key]
	if !ok {
		return nil, false
	}
	dd, ok := v.(Dict)
	return dd, ok
}

func (d Dict) Array(key Name) (Array, bool) {
	v, ok := d[key]
	if !ok {
		return nil, false
	}
	a, ok := v.(Array)
	return a, ok
}

func (d Dict) Ref(key Name) (Ref, bool) {
	v, ok := d[key]
	if !ok {
		return Ref{}, false
	}
	r, ok := v.(Ref)
	return r, ok
}

func (d Dict) Int(key Name) (int, bool) {
	v, ok := d[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	}
	return 0, false
}

func (d Dict) Float(key Name) (float64, bool) {
	v, ok := d[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

func (d Dict) String(key Name) (string, bool) {
	v, ok := d[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func (d Dict) Stream(key Name) (*Stream, bool) {
	v, ok := d[key]
	if !ok {
		return nil, false
	}
	s, ok := v.(*Stream)
	return s, ok
}

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}

func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}

func asString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case Name:
		return string(s)
	}
	return fmt.Sprintf("%v", v)
}

// matMul6 multiplies two 6-element affine matrices [a b c d e f].
// Represents: [a b 0; c d 0; e f 1] (PDF row-major convention).
func matMul6(a, b [6]float64) [6]float64 {
	return [6]float64{
		a[0]*b[0] + a[1]*b[2],
		a[0]*b[1] + a[1]*b[3],
		a[2]*b[0] + a[3]*b[2],
		a[2]*b[1] + a[3]*b[3],
		a[4]*b[0] + a[5]*b[2] + b[4],
		a[4]*b[1] + a[5]*b[3] + b[5],
	}
}

// translateMatrix returns a translation matrix for (tx, ty).
func translateMatrix(tx, ty float64) [6]float64 {
	return [6]float64{1, 0, 0, 1, tx, ty}
}
