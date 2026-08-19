package pdf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rc4"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The encryption side below is written independently of crypt.go so the reader's
// decryption path is exercised against a separate implementation rather than
// against itself.

const (
	testUserPassword  = "user-secret"
	testOwnerPassword = "owner-secret"
	testDocID         = "0123456789abcdef"
	testPageText      = "Encrypted Hello"
	testTitle         = "confidential title"
	// A fixed IV keeps generated fixtures byte-stable across runs.
	testIV = "0123456789abcdef"
)

type encScheme struct {
	name    string
	v, r    int
	keyLen  int // bytes
	aes     bool
	aes256  bool
	cfmName string
}

var encSchemes = []encScheme{
	{name: "RC4-40", v: 1, r: 2, keyLen: 5},
	{name: "RC4-128", v: 2, r: 3, keyLen: 16},
	{name: "AES-128", v: 4, r: 4, keyLen: 16, aes: true, cfmName: "AESV2"},
	{name: "AES-256-R6", v: 5, r: 6, keyLen: 32, aes: true, aes256: true, cfmName: "AESV3"},
}

// --- independent encryption primitives -------------------------------------

func encRC4(key, data []byte) []byte {
	c, err := rc4.NewCipher(key)
	if err != nil {
		panic(err)
	}
	out := make([]byte, len(data))
	c.XORKeyStream(out, data)
	return out
}

func encAES(key, data []byte) []byte {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	pad := aes.BlockSize - len(data)%aes.BlockSize
	padded := append(append([]byte{}, data...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, []byte(testIV)).CryptBlocks(out, padded)
	return append([]byte(testIV), out...)
}

func encXorKey(key []byte, b byte) []byte {
	out := make([]byte, len(key))
	for i, k := range key {
		out[i] = k ^ b
	}
	return out
}

// encFileKey implements Algorithm 2 for R2-R4.
func encFileKey(userPw string, o []byte, perm int, id []byte, r, keyLen int) []byte {
	h := md5.New()
	h.Write(padPassword(userPw))
	h.Write(o)
	p := uint32(int32(perm))
	h.Write([]byte{byte(p), byte(p >> 8), byte(p >> 16), byte(p >> 24)})
	h.Write(id)
	key := h.Sum(nil)
	if r >= 3 {
		for i := 0; i < 50; i++ {
			s := md5.Sum(key[:keyLen])
			key = s[:]
		}
	}
	return key[:keyLen]
}

// encOwnerValue implements Algorithm 3.
func encOwnerValue(ownerPw, userPw string, r, keyLen int) []byte {
	s := md5.Sum(padPassword(ownerPw))
	key := s[:]
	if r >= 3 {
		for i := 0; i < 50; i++ {
			ss := md5.Sum(key)
			key = ss[:]
		}
	}
	key = key[:keyLen]
	out := encRC4(key, padPassword(userPw))
	if r >= 3 {
		for i := 1; i <= 19; i++ {
			out = encRC4(encXorKey(key, byte(i)), out)
		}
	}
	return out
}

// encUserValue implements Algorithms 4 and 5.
func encUserValue(fileKey, id []byte, r int) []byte {
	if r == 2 {
		return encRC4(fileKey, passwordPad)
	}
	h := md5.New()
	h.Write(passwordPad)
	h.Write(id)
	x := encRC4(fileKey, h.Sum(nil))
	for i := 1; i <= 19; i++ {
		x = encRC4(encXorKey(fileKey, byte(i)), x)
	}
	// Pad to 32 bytes; the trailing 16 are arbitrary per the spec.
	return append(x, bytes.Repeat([]byte{0x00}, 16)...)
}

// encHash2B implements Algorithm 2.B independently of crypt.go's version.
func encHash2B(pw, salt, udata []byte) []byte {
	h := sha256.New()
	h.Write(pw)
	h.Write(salt)
	h.Write(udata)
	k := h.Sum(nil)
	for i := 0; ; i++ {
		var k1 []byte
		for j := 0; j < 64; j++ {
			k1 = append(k1, pw...)
			k1 = append(k1, k...)
			k1 = append(k1, udata...)
		}
		block, _ := aes.NewCipher(k[:16])
		e := make([]byte, len(k1))
		cipher.NewCBCEncrypter(block, k[16:32]).CryptBlocks(e, k1)
		sum := 0
		for _, b := range e[:16] {
			sum += int(b)
		}
		switch sum % 3 {
		case 0:
			s := sha256.Sum256(e)
			k = s[:]
		case 1:
			s := sha512.Sum384(e)
			k = s[:]
		case 2:
			s := sha512.Sum512(e)
			k = s[:]
		}
		if i >= 63 && int(e[len(e)-1]) <= i-32 {
			break
		}
	}
	return k[:32]
}

// encWrap encrypts the file key into /UE or /OE: AES-256-CBC, zero IV, no padding.
func encWrap(ikey, fileKey []byte) []byte {
	block, _ := aes.NewCipher(ikey)
	out := make([]byte, 32)
	cipher.NewCBCEncrypter(block, make([]byte, 16)).CryptBlocks(out, fileKey)
	return out
}

// --- fixture construction ---------------------------------------------------

// buildEncryptedPDF assembles a single-page PDF encrypted under the given
// scheme, with an encrypted content stream and an encrypted /Title string.
func buildEncryptedPDF(t *testing.T, s encScheme) []byte {
	t.Helper()
	return buildEncryptedPDFWith(t, s, testUserPassword)
}

// buildEmptyPasswordPDF builds an encrypted PDF whose user password is empty,
// which is how most password-protected statements are actually produced.
func buildEmptyPasswordPDF(t *testing.T, s encScheme) []byte {
	t.Helper()
	return buildEncryptedPDFWith(t, s, "")
}

func buildEncryptedPDFWith(t *testing.T, s encScheme, userPw string) []byte {
	t.Helper()

	const perm = -3904 // typical: print/copy denied, everything else allowed
	id := []byte(testDocID)

	var fileKey, oVal, uVal, ueVal, oeVal []byte
	if s.aes256 {
		// Deterministic 32-byte file key; real producers use a CSPRNG.
		fileKey = []byte("0123456789abcdef0123456789abcdef")
		uvs, uks := []byte("uvalsalt"), []byte("ukeysalt")
		uVal = append(append(encHash2B([]byte(userPw), uvs, nil), uvs...), uks...)
		ueVal = encWrap(encHash2B([]byte(userPw), uks, nil), fileKey)

		ovs, oks := []byte("ovalsalt"), []byte("okeysalt")
		oVal = append(append(encHash2B([]byte(testOwnerPassword), ovs, uVal[:48]), ovs...), oks...)
		oeVal = encWrap(encHash2B([]byte(testOwnerPassword), oks, uVal[:48]), fileKey)
	} else {
		oVal = encOwnerValue(testOwnerPassword, userPw, s.r, s.keyLen)
		fileKey = encFileKey(userPw, oVal, perm, id, s.r, s.keyLen)
		uVal = encUserValue(fileKey, id, s.r)
	}

	// encryptFor returns the ciphertext for one object's string or stream body.
	encryptFor := func(data []byte, num, gen int) []byte {
		if s.aes256 {
			return encAES(fileKey, data)
		}
		h := md5.New()
		h.Write(fileKey)
		h.Write([]byte{byte(num), byte(num >> 8), byte(num >> 16), byte(gen), byte(gen >> 8)})
		if s.aes {
			h.Write([]byte{0x73, 0x41, 0x6C, 0x54})
		}
		sum := h.Sum(nil)
		n := len(fileKey) + 5
		if n > 16 {
			n = 16
		}
		key := sum[:n]
		if s.aes {
			return encAES(key, data)
		}
		return encRC4(key, data)
	}

	content := fmt.Sprintf("BT /F1 24 Tf 72 700 Td (%s) Tj ET", testPageText)
	encContent := encryptFor([]byte(content), 4, 0)
	encTitle := encryptFor([]byte(testTitle), 6, 0)

	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] " +
			"/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(encContent), encContent),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Title (%s) >>", escapePDFString(encTitle)),
		encryptDict(s, oVal, uVal, ueVal, oeVal, perm),
	}

	trailer := fmt.Sprintf("/Size %d /Root 1 0 R /Info 6 0 R /Encrypt 7 0 R /ID [<%x> <%x>]",
		len(objs)+1, id, id)
	return assemblePDF(objs, trailer)
}

// minimalPDF builds the same document with no encryption, as a control that the
// decryption hooks stay inert on ordinary files.
func minimalPDF(t *testing.T) []byte {
	t.Helper()

	content := fmt.Sprintf("BT /F1 24 Tf 72 700 Td (%s) Tj ET", testPageText)
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] " +
			"/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	return assemblePDF(objs, "/Size 6 /Root 1 0 R")
}

// assemblePDF writes objects 1..N with a traditional xref table and trailer.
func assemblePDF(objs []string, trailerExtra string) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")
	offsets := make([]int, len(objs)+1)
	for i, body := range objs {
		offsets[i+1] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xrefPos := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objs)+1)
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= len(objs); i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< %s >>\nstartxref\n%d\n%%%%EOF\n", trailerExtra, xrefPos)
	return buf.Bytes()
}

func encryptDict(s encScheme, o, u, ue, oe []byte, perm int) string {
	base := fmt.Sprintf("/Filter /Standard /V %d /R %d /P %d /O (%s) /U (%s)",
		s.v, s.r, perm, escapePDFString(o), escapePDFString(u))
	switch {
	case s.aes256:
		return fmt.Sprintf("<< %s /Length 256 /UE (%s) /OE (%s) "+
			"/CF << /StdCF << /CFM /AESV3 /Length 32 >> >> /StmF /StdCF /StrF /StdCF >>",
			base, escapePDFString(ue), escapePDFString(oe))
	case s.v == 4:
		return fmt.Sprintf("<< %s /Length 128 "+
			"/CF << /StdCF << /CFM /%s /Length 16 >> >> /StmF /StdCF /StrF /StdCF >>",
			base, s.cfmName)
	case s.v == 2:
		return fmt.Sprintf("<< %s /Length %d >>", base, s.keyLen*8)
	default:
		return fmt.Sprintf("<< %s >>", base)
	}
}

// escapePDFString escapes bytes for a PDF literal string.
func escapePDFString(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		switch c {
		case '(', ')', '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		case '\r':
			sb.WriteString(`\r`)
		case '\n':
			sb.WriteString(`\n`)
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// --- tests ------------------------------------------------------------------

func TestOpenEncryptedWithUserPassword(t *testing.T) {
	for _, s := range encSchemes {
		t.Run(s.name, func(t *testing.T) {
			data := buildEncryptedPDF(t, s)

			doc, err := OpenBytesWithPassword(data, testUserPassword)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if !doc.IsEncrypted() {
				t.Error("IsEncrypted() = false, want true")
			}
			if got := doc.NumPages(); got != 1 {
				t.Fatalf("NumPages() = %d, want 1", got)
			}
			got, err := doc.Page(0).Text()
			if err != nil {
				t.Fatalf("extract text: %v", err)
			}
			if !strings.Contains(got, testPageText) {
				t.Errorf("page text = %q, want it to contain %q", got, testPageText)
			}
		})
	}
}

func TestOpenEncryptedWithOwnerPassword(t *testing.T) {
	for _, s := range encSchemes {
		t.Run(s.name, func(t *testing.T) {
			data := buildEncryptedPDF(t, s)

			doc, err := OpenBytesWithPassword(data, testOwnerPassword)
			if err != nil {
				t.Fatalf("open with owner password: %v", err)
			}
			got, err := doc.Page(0).Text()
			if err != nil {
				t.Fatalf("extract text: %v", err)
			}
			if !strings.Contains(got, testPageText) {
				t.Errorf("page text = %q, want it to contain %q", got, testPageText)
			}
		})
	}
}

// Strings live outside content streams and take a different code path, so they
// are checked separately.
func TestEncryptedStringsAreDecrypted(t *testing.T) {
	for _, s := range encSchemes {
		t.Run(s.name, func(t *testing.T) {
			data := buildEncryptedPDF(t, s)

			r, err := OpenWithPassword(data, testUserPassword)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			info, ok := r.ResolveDict(r.Trailer()["Info"])
			if !ok {
				t.Fatal("no /Info dictionary")
			}
			if got, _ := info.String("Title"); got != testTitle {
				t.Errorf("/Title = %q, want %q", got, testTitle)
			}
		})
	}
}

func TestWrongPassword(t *testing.T) {
	for _, s := range encSchemes {
		t.Run(s.name, func(t *testing.T) {
			data := buildEncryptedPDF(t, s)

			_, err := OpenBytesWithPassword(data, "not-the-password")
			if !errors.Is(err, ErrWrongPassword) {
				t.Errorf("err = %v, want ErrWrongPassword", err)
			}
		})
	}
}

// A password-protected file opened through the password-free entry point must
// report ErrEncrypted rather than failing as a malformed file.
func TestPasswordRequiredReportsErrEncrypted(t *testing.T) {
	for _, s := range encSchemes {
		t.Run(s.name, func(t *testing.T) {
			data := buildEncryptedPDF(t, s)

			if _, err := OpenBytes(data); !errors.Is(err, ErrEncrypted) {
				t.Errorf("err = %v, want ErrEncrypted", err)
			}
		})
	}
}

// The common case for emailed statements: encrypted, but with no user password.
// These must open through the ordinary entry points.
func TestEmptyUserPasswordOpensWithoutPassword(t *testing.T) {
	for _, s := range encSchemes {
		t.Run(s.name, func(t *testing.T) {
			data := buildEmptyPasswordPDF(t, s)

			doc, err := OpenBytes(data)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if !doc.IsEncrypted() {
				t.Error("IsEncrypted() = false, want true")
			}
			got, err := doc.Page(0).Text()
			if err != nil {
				t.Fatalf("extract text: %v", err)
			}
			if !strings.Contains(got, testPageText) {
				t.Errorf("page text = %q, want it to contain %q", got, testPageText)
			}
		})
	}
}

func TestUnencryptedFileIsUnaffected(t *testing.T) {
	doc, err := OpenBytes(minimalPDF(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if doc.IsEncrypted() {
		t.Error("IsEncrypted() = true for an unencrypted file")
	}
	got, err := doc.Page(0).Text()
	if err != nil {
		t.Fatalf("extract text: %v", err)
	}
	if !strings.Contains(got, testPageText) {
		t.Errorf("page text = %q, want it to contain %q", got, testPageText)
	}
}
