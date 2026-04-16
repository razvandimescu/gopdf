package pdf

import (
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Writer produces a PDF file by accumulating objects.
type Writer struct {
	buf     bytes.Buffer
	offsets map[int]int // object number → byte offset
	nextObj int
}

// NewWriter creates a Writer with a PDF header.
func NewWriter() *Writer {
	w := &Writer{
		offsets: make(map[int]int),
		nextObj: 1,
	}
	w.buf.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	return w
}

// Len returns the current size of the output buffer in bytes.
func (w *Writer) Len() int {
	return w.buf.Len()
}

type writerCheckpoint struct {
	bufLen  int
	nextObj int
}

func (w *Writer) checkpoint() writerCheckpoint {
	return writerCheckpoint{bufLen: w.buf.Len(), nextObj: w.nextObj}
}

func (w *Writer) restore(cp writerCheckpoint) {
	w.buf.Truncate(cp.bufLen)
	for k := range w.offsets {
		if k >= cp.nextObj {
			delete(w.offsets, k)
		}
	}
	w.nextObj = cp.nextObj
}

// AllocRef reserves an object number and returns a Ref.
// The object must later be written with WriteObject or WriteStream.
func (w *Writer) AllocRef() Ref {
	ref := Ref{Num: w.nextObj, Gen: 0}
	w.nextObj++
	return ref
}

// WriteObject writes an indirect object. If obj is a *Stream, the stream
// is written with its existing Data and Dict (no recompression).
func (w *Writer) WriteObject(ref Ref, obj any) error {
	// Handle raw streams (e.g., image streams with passthrough filters).
	if stream, ok := obj.(*Stream); ok {
		w.offsets[ref.Num] = w.buf.Len()
		fmt.Fprintf(&w.buf, "%d 0 obj\n", ref.Num)
		if err := writeValue(&w.buf, stream.Dict); err != nil {
			return err
		}
		w.buf.WriteString("\nstream\n")
		w.buf.Write(stream.Data)
		w.buf.WriteString("\nendstream\nendobj\n")
		return nil
	}

	w.offsets[ref.Num] = w.buf.Len()
	fmt.Fprintf(&w.buf, "%d 0 obj\n", ref.Num)
	if err := writeValue(&w.buf, obj); err != nil {
		return err
	}
	w.buf.WriteString("\nendobj\n")
	return nil
}

// WriteStream writes a stream object, compressing data with FlateDecode.
func (w *Writer) WriteStream(ref Ref, dict Dict, data []byte) error {
	compressed, err := flateCompress(data)
	if err != nil {
		return fmt.Errorf("compressing stream: %w", err)
	}

	// Set stream-specific keys.
	dict["Filter"] = Name("FlateDecode")
	dict["Length"] = len(compressed)

	w.offsets[ref.Num] = w.buf.Len()
	fmt.Fprintf(&w.buf, "%d 0 obj\n", ref.Num)
	if err := writeValue(&w.buf, dict); err != nil {
		return err
	}
	w.buf.WriteString("\nstream\n")
	w.buf.Write(compressed)
	w.buf.WriteString("\nendstream\nendobj\n")
	return nil
}

// Finish appends the xref table, trailer, and %%EOF. Returns the complete PDF.
func (w *Writer) Finish(rootRef Ref) ([]byte, error) {
	return w.FinishWithID(rootRef, nil)
}

// FinishWithID is like Finish but preserves the original document's
// creation ID (first element of the /ID array). This prevents Adobe
// Acrobat from showing a "save changes?" prompt on open.
func (w *Writer) FinishWithID(rootRef Ref, originalID Array) ([]byte, error) {
	infoRef := w.AllocRef()
	now := time.Now().UTC()
	pdfDate := fmt.Sprintf("D:%s", now.Format("20060102150405Z"))
	if err := w.WriteObject(infoRef, Dict{
		"Producer":     "gopdf",
		"CreationDate": pdfDate,
		"ModDate":      pdfDate,
	}); err != nil {
		return nil, fmt.Errorf("writing info dict: %w", err)
	}

	xrefOffset := w.buf.Len()
	size := w.nextObj

	// Write xref table.
	fmt.Fprintf(&w.buf, "xref\n0 %d\n", size)
	w.buf.WriteString("0000000000 65535 f \r\n")
	for i := 1; i < size; i++ {
		offset, ok := w.offsets[i]
		if ok {
			fmt.Fprintf(&w.buf, "%010d 00000 n \r\n", offset)
		} else {
			w.buf.WriteString("0000000000 00000 f \r\n")
		}
	}

	// Document ID: preserve original creation ID, generate new modification ID.
	h := md5.New()
	h.Write(w.buf.Bytes())
	modID := string(h.Sum(nil))
	creationID := modID
	if len(originalID) >= 1 {
		if s, ok := originalID[0].(string); ok && len(s) > 0 {
			creationID = s
		}
	}

	trailer := Dict{
		"Size": size,
		"Root": rootRef,
		"Info": infoRef,
		"ID":   Array{creationID, modID},
	}
	w.buf.WriteString("trailer\n")
	if err := writeValue(&w.buf, trailer); err != nil {
		return nil, fmt.Errorf("writing trailer: %w", err)
	}
	fmt.Fprintf(&w.buf, "\nstartxref\n%d\n%%%%EOF\n", xrefOffset)

	return w.buf.Bytes(), nil
}

// writeValue serializes a PDF value to w.
func writeValue(w io.Writer, obj any) error {
	switch v := obj.(type) {
	case nil:
		_, err := io.WriteString(w, "null")
		return err

	case bool:
		if v {
			_, err := io.WriteString(w, "true")
			return err
		}
		_, err := io.WriteString(w, "false")
		return err

	case int:
		_, err := io.WriteString(w, strconv.Itoa(v))
		return err

	case float64:
		s := strconv.FormatFloat(v, 'f', -1, 64)
		_, err := io.WriteString(w, s)
		return err

	case Name:
		_, err := fmt.Fprintf(w, "/%s", escapeName(string(v)))
		return err

	case string:
		_, err := fmt.Fprintf(w, "<%s>", hexEncode([]byte(v)))
		return err

	case Ref:
		_, err := fmt.Fprintf(w, "%d %d R", v.Num, v.Gen)
		return err

	case Dict:
		if _, err := io.WriteString(w, "<<"); err != nil {
			return err
		}
		// Sort keys for deterministic output.
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, string(k))
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, " /%s ", escapeName(k))
			if err := writeValue(w, v[Name(k)]); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, " >>")
		return err

	case Array:
		if _, err := io.WriteString(w, "["); err != nil {
			return err
		}
		for i, elem := range v {
			if i > 0 {
				io.WriteString(w, " ")
			}
			if err := writeValue(w, elem); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, "]")
		return err

	default:
		return fmt.Errorf("writeValue: unsupported type %T", obj)
	}
}

// escapeName escapes special characters in a PDF name.
func escapeName(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '!' || c > '~' || c == '#' || c == '/' || c == '(' || c == ')' ||
			c == '<' || c == '>' || c == '[' || c == ']' || c == '{' || c == '}' || c == '%' {
			fmt.Fprintf(&b, "#%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// hexEncode returns the hex representation of data.
func hexEncode(data []byte) string {
	var b strings.Builder
	for _, c := range data {
		fmt.Fprintf(&b, "%02x", c)
	}
	return b.String()
}

func flateCompress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
