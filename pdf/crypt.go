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
)

// ErrEncrypted reports that a PDF is encrypted and needs a password. The
// password-free entry points return it only when the empty user password fails,
// so files encrypted with no user password (common for emailed statements) open
// through OpenFile and OpenBytes as usual.
var ErrEncrypted = errors.New("pdf: file is encrypted, use OpenFileWithPassword")

// ErrWrongPassword reports that the supplied password matched neither the user
// nor the owner password.
var ErrWrongPassword = errors.New("pdf: incorrect password")

// ErrUnsupportedEncryption reports an encryption scheme this package cannot
// read: a non-standard security handler, or a public-key handler.
var ErrUnsupportedEncryption = errors.New("pdf: unsupported encryption scheme")

// cryptMethod is the algorithm a crypt filter applies.
type cryptMethod int

const (
	cryptNone cryptMethod = iota
	cryptRC4
	cryptAESV2 // AES-128-CBC, per-object key
	cryptAESV3 // AES-256-CBC, file key used directly
)

// encryptInfo holds the derived file encryption key and the per-use filters.
type encryptInfo struct {
	key             []byte
	v               int
	streams         cryptMethod
	strings         cryptMethod
	encryptMetadata bool
}

// passwordPad is the 32-byte padding string from Algorithm 2.
var passwordPad = []byte{
	0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41, 0x64, 0x00, 0x4E, 0x56,
	0xFF, 0xFA, 0x01, 0x08, 0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80,
	0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A,
}

// padPassword truncates to 32 bytes or pads with passwordPad (Algorithm 2, step a).
func padPassword(password string) []byte {
	p := []byte(password)
	if len(p) > 32 {
		return p[:32]
	}
	return append(append(make([]byte, 0, 32), p...), passwordPad[:32-len(p)]...)
}

// setupEncryption derives the file key from the trailer's /Encrypt dictionary.
// It returns nil with no side effects when the document is not encrypted.
func (r *Reader) setupEncryption(password string) error {
	encObj, ok := r.trailer["Encrypt"]
	if !ok {
		return nil
	}
	// Resolving /Encrypt here, while r.crypt is still nil, is what keeps the
	// dictionary's own strings (/O, /U, /OE, /UE, /Perms) in plaintext:
	// decryption is inert during this resolve, and Resolve caches the result, so
	// no later lookup re-parses it once the key is armed.
	enc, ok := r.ResolveDict(encObj)
	if !ok {
		return fmt.Errorf("pdf: /Encrypt is not a dictionary")
	}

	if filter, _ := enc.Name("Filter"); filter != "Standard" {
		return fmt.Errorf("%w: /Filter %s", ErrUnsupportedEncryption, filter)
	}

	v, _ := enc.Int("V")
	rev, _ := enc.Int("R")
	if rev == 0 {
		return fmt.Errorf("pdf: /Encrypt missing /R")
	}

	info := &encryptInfo{v: v, encryptMetadata: true}
	if em, ok := enc["EncryptMetadata"].(bool); ok {
		info.encryptMetadata = em
	}

	keyLen := 5
	if n, ok := enc.Int("Length"); ok && n >= 40 {
		keyLen = n / 8
	}

	switch v {
	case 1:
		keyLen = 5
		info.streams, info.strings = cryptRC4, cryptRC4
	case 2:
		info.streams, info.strings = cryptRC4, cryptRC4
	case 4, 5:
		stmF, _ := enc.Name("StmF")
		strF, _ := enc.Name("StrF")
		var n int
		info.streams, n = r.cryptFilter(enc, stmF)
		if n > 0 {
			keyLen = n
		}
		info.strings, _ = r.cryptFilter(enc, strF)
	default:
		return fmt.Errorf("%w: /V %d", ErrUnsupportedEncryption, v)
	}

	key, err := r.deriveKey(enc, rev, info.encryptMetadata, password, keyLen)
	if err != nil {
		return err
	}
	info.key = key
	r.crypt = info
	return nil
}

// cryptFilter looks up a crypt filter by name in /CF and returns its method and
// key length in bytes. An absent or /Identity name means no encryption.
func (r *Reader) cryptFilter(enc Dict, name Name) (cryptMethod, int) {
	if name == "" || name == "Identity" {
		return cryptNone, 0
	}
	cf, ok := r.ResolveDict(enc["CF"])
	if !ok {
		return cryptNone, 0
	}
	f, ok := r.ResolveDict(cf[name])
	if !ok {
		return cryptNone, 0
	}
	length := 0
	if n, ok := f.Int("Length"); ok {
		// Spec says bytes; many producers write bits. 40 and above can only be
		// bits, since no filter uses a key that long — 40 itself is the legacy
		// RC4-40 case, which some producers emit here in bits.
		if n >= 40 {
			n /= 8
		}
		length = n
	}
	cfm, _ := f.Name("CFM")
	switch cfm {
	case "V2":
		return cryptRC4, length
	case "AESV2":
		return cryptAESV2, 16
	case "AESV3":
		return cryptAESV3, 32
	case "None":
		return cryptNone, 0
	}
	return cryptNone, length
}

// deriveKey validates the password and returns the file encryption key, trying
// the user password first and then the owner password.
func (r *Reader) deriveKey(enc Dict, rev int, encryptMetadata bool, password string, keyLen int) ([]byte, error) {
	o := []byte(dictString(enc, "O"))
	u := []byte(dictString(enc, "U"))

	if rev >= 5 {
		return deriveKeyR5R6(enc, rev, password, o, u)
	}

	perm, _ := enc.Int("P")
	var idFirst []byte
	if id := r.OriginalID(); len(id) > 0 {
		if s, ok := id[0].(string); ok {
			idFirst = []byte(s)
		}
	}
	keyLen = clampKeyLen(rev, keyLen)

	key := legacyFileKey(padPassword(password), o, perm, idFirst, rev, keyLen, encryptMetadata)
	if validateUserPassword(key, u, idFirst, rev) {
		return key, nil
	}

	// Try the password as the owner password: recover the user password from /O
	// (Algorithm 7), then validate that.
	if userPwd := recoverUserPassword(password, o, rev, keyLen); userPwd != nil {
		key = legacyFileKey(userPwd, o, perm, idFirst, rev, keyLen, encryptMetadata)
		if validateUserPassword(key, u, idFirst, rev) {
			return key, nil
		}
	}
	return nil, ErrWrongPassword
}

// clampKeyLen bounds the file key length to the range the standard handler
// allows. Revision 2 is fixed at 40 bits regardless of what /Length claims.
func clampKeyLen(rev, keyLen int) int {
	if rev == 2 {
		return 5
	}
	return min(max(keyLen, 5), 16)
}

// legacyFileKey implements Algorithm 2 for revisions 2 through 4. keyLen must
// already be clamped by clampKeyLen.
func legacyFileKey(padded, o []byte, perm int, idFirst []byte, rev, keyLen int, encryptMetadata bool) []byte {
	h := md5.New()
	h.Write(padded)
	h.Write(o)
	p := uint32(int32(perm))
	h.Write([]byte{byte(p), byte(p >> 8), byte(p >> 16), byte(p >> 24)})
	h.Write(idFirst)
	if rev >= 4 && !encryptMetadata {
		h.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	}
	key := h.Sum(nil)

	if rev >= 3 {
		for i := 0; i < 50; i++ {
			sum := md5.Sum(key[:keyLen])
			key = sum[:]
		}
	}
	return key[:keyLen]
}

// validateUserPassword implements Algorithms 4 and 5 in reverse.
func validateUserPassword(key, u, idFirst []byte, rev int) bool {
	if rev == 2 {
		want := rc4Bytes(key, passwordPad)
		return len(u) >= 32 && bytes.Equal(want, u[:32])
	}
	h := md5.New()
	h.Write(passwordPad)
	h.Write(idFirst)
	x := rc4Bytes(key, h.Sum(nil))
	for i := 1; i <= 19; i++ {
		x = rc4Bytes(xorKey(key, byte(i)), x)
	}
	// Only the first 16 bytes are meaningful; the rest is arbitrary padding.
	return len(u) >= 16 && bytes.Equal(x[:16], u[:16])
}

// recoverUserPassword implements Algorithm 7: decrypt /O with a key derived from
// the owner password to recover the padded user password. keyLen must already be
// clamped by clampKeyLen.
//
// Note the 50-round loop here hashes the full digest, where Algorithm 2 hashes
// only the first keyLen bytes. The difference is deliberate and specified.
func recoverUserPassword(password string, o []byte, rev, keyLen int) []byte {
	if len(o) < 32 {
		return nil
	}
	sum := md5.Sum(padPassword(password))
	key := sum[:]
	if rev >= 3 {
		for i := 0; i < 50; i++ {
			s := md5.Sum(key)
			key = s[:]
		}
	}
	key = key[:keyLen]

	if rev == 2 {
		return rc4Bytes(key, o[:32])
	}
	out := o[:32]
	for i := 19; i >= 0; i-- {
		out = rc4Bytes(xorKey(key, byte(i)), out)
	}
	return out
}

// deriveKeyR5R6 implements Algorithm 2.A for AES-256 (revisions 5 and 6).
func deriveKeyR5R6(enc Dict, rev int, password string, o, u []byte) ([]byte, error) {
	if len(u) < 48 || len(o) < 48 {
		return nil, fmt.Errorf("pdf: /U or /O too short for R%d", rev)
	}
	pw := []byte(password)
	if len(pw) > 127 {
		pw = pw[:127]
	}

	// User password: validation salt is U[32:40], key salt U[40:48].
	if bytes.Equal(hash2B(pw, u[32:40], nil, rev), u[:32]) {
		ikey := hash2B(pw, u[40:48], nil, rev)
		return unwrapFileKey(ikey, []byte(dictString(enc, "UE")))
	}
	// Owner password: salts are in /O, and the full 48-byte /U is mixed in.
	if bytes.Equal(hash2B(pw, o[32:40], u[:48], rev), o[:32]) {
		ikey := hash2B(pw, o[40:48], u[:48], rev)
		return unwrapFileKey(ikey, []byte(dictString(enc, "OE")))
	}
	return nil, ErrWrongPassword
}

// unwrapFileKey decrypts /UE or /OE with the intermediate key: AES-256-CBC with
// a zero IV and no padding.
func unwrapFileKey(ikey, wrapped []byte) ([]byte, error) {
	if len(wrapped) < 32 {
		return nil, fmt.Errorf("pdf: /UE or /OE too short")
	}
	block, err := aes.NewCipher(ikey)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 32)
	cipher.NewCBCDecrypter(block, make([]byte, 16)).CryptBlocks(out, wrapped[:32])
	return out, nil
}

// hash2BDone implements Algorithm 2.B step (e): after at least 64 rounds, stop
// once the last byte of E is no longer greater than the round count minus 32.
//
// rounds counts completed rounds. Expressing this against a 0-based loop index
// shifts the threshold by one and silently derives the wrong key for a few
// percent of passwords, so it is kept separate and tested directly.
func hash2BDone(rounds int, lastE byte) bool {
	return rounds >= 64 && int(lastE) <= rounds-32
}

// hash2B implements the revision 6 hardened hash (Algorithm 2.B). For revision 5
// it degrades to a single SHA-256, which is what that revision specifies.
func hash2B(password, salt, udata []byte, rev int) []byte {
	h := sha256.New()
	h.Write(password)
	h.Write(salt)
	h.Write(udata)
	k := h.Sum(nil)
	if rev == 5 {
		return k
	}

	// Both buffers are sized for the largest k the loop can produce (SHA-512, 64
	// bytes) and reused across rounds. Allocating inside the loop costs ~1.2 MB
	// of garbage per call, since it runs at least 64 times.
	maxRound := 64 * (len(password) + sha512.Size + len(udata))
	k1 := make([]byte, 0, maxRound)
	buf := make([]byte, maxRound)

	// rounds counts completed rounds, which is what step (e) is expressed in
	// terms of. Counting from 0 instead makes the threshold off by one and
	// derives a wrong key for roughly one file in 38.
	var e []byte
	for rounds := 1; ; rounds++ {
		k1 = k1[:0]
		for j := 0; j < 64; j++ {
			k1 = append(k1, password...)
			k1 = append(k1, k...)
			k1 = append(k1, udata...)
		}
		block, err := aes.NewCipher(k[:16])
		if err != nil {
			return k
		}
		e = buf[:len(k1)]
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
		if hash2BDone(rounds, e[len(e)-1]) {
			break
		}
	}
	return k[:32]
}

// objectKey derives the per-object key (Algorithm 1). AES-256 uses the file key
// directly, with no per-object derivation.
func (e *encryptInfo) objectKey(num, gen int, m cryptMethod) []byte {
	if e.v >= 5 {
		return e.key
	}
	h := md5.New()
	h.Write(e.key)
	h.Write([]byte{byte(num), byte(num >> 8), byte(num >> 16), byte(gen), byte(gen >> 8)})
	if m == cryptAESV2 {
		h.Write([]byte{0x73, 0x41, 0x6C, 0x54}) // "sAlT"
	}
	sum := h.Sum(nil)
	n := len(e.key) + 5
	if n > 16 {
		n = 16
	}
	return sum[:n]
}

// decrypt applies the given method to one string or stream body.
func (e *encryptInfo) decrypt(data []byte, num, gen int, m cryptMethod) []byte {
	if m == cryptNone || len(data) == 0 {
		return data
	}
	return e.apply(e.objectKey(num, gen, m), data, m)
}

// apply runs one cipher against an already-derived object key.
func (e *encryptInfo) apply(key, data []byte, m cryptMethod) []byte {
	switch m {
	case cryptRC4:
		return rc4Bytes(key, data)
	case cryptAESV2, cryptAESV3:
		return aesCBCDecrypt(key, data)
	}
	return data
}

// aesCBCDecrypt decrypts data whose first 16 bytes are the initialisation
// vector, stripping PKCS#7 padding. Malformed input yields nil rather than an
// error so a single damaged object cannot fail the whole document.
func aesCBCDecrypt(key, data []byte) []byte {
	if len(data) <= aes.BlockSize {
		return nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil
	}
	iv, body := data[:aes.BlockSize], data[aes.BlockSize:]
	body = body[:len(body)-len(body)%aes.BlockSize]
	if len(body) == 0 {
		return nil
	}
	out := make([]byte, len(body))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, body)

	pad := int(out[len(out)-1])
	if pad >= 1 && pad <= aes.BlockSize && pad <= len(out) {
		out = out[:len(out)-pad]
	}
	return out
}

func rc4Bytes(key, data []byte) []byte {
	c, err := rc4.NewCipher(key)
	if err != nil {
		return data
	}
	out := make([]byte, len(data))
	c.XORKeyStream(out, data)
	return out
}

// xorKey returns key with every byte XORed by b (Algorithms 5 and 7).
func xorKey(key []byte, b byte) []byte {
	out := make([]byte, len(key))
	for i, k := range key {
		out[i] = k ^ b
	}
	return out
}

// dictString reads a direct string value, tolerating its absence.
func dictString(d Dict, key Name) string {
	s, _ := d.String(key)
	return s
}

// decryptStrings walks a parsed object and decrypts every string in place.
// Streams are handled separately in readStreamData; only their dictionaries are
// walked here.
//
// The object key is derived once here rather than per string: it is invariant
// for the whole object, and deriving it costs an MD5 each time.
func (r *Reader) decryptStrings(obj any, num, gen int) any {
	if r.crypt == nil || r.crypt.strings == cryptNone {
		return obj
	}
	return r.crypt.walkStrings(obj, r.crypt.objectKey(num, gen, r.crypt.strings))
}

func (e *encryptInfo) walkStrings(obj any, key []byte) any {
	switch v := obj.(type) {
	case string:
		if v == "" {
			return v
		}
		return string(e.apply(key, []byte(v), e.strings))
	case Dict:
		for k, item := range v {
			v[k] = e.walkStrings(item, key)
		}
	case Array:
		for i, item := range v {
			v[i] = e.walkStrings(item, key)
		}
	case *Stream:
		e.walkStrings(v.Dict, key)
	}
	return obj
}

// streamIsEncrypted reports whether a stream body should be decrypted. Cross
// reference streams are never encrypted, and metadata is exempt when the
// document sets /EncryptMetadata false.
func (r *Reader) streamIsEncrypted(d Dict) bool {
	if r.crypt == nil || r.crypt.streams == cryptNone {
		return false
	}
	switch t, _ := d.Name("Type"); t {
	case "XRef":
		return false
	case "Metadata":
		return r.crypt.encryptMetadata
	}
	return true
}
