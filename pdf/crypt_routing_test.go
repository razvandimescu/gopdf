package pdf

import (
	"bytes"
	"testing"
)

// The fixtures in testdata/ prove the ciphers and key derivation are right,
// because a foreign producer made them. They cannot reach the routing decisions
// below: MuPDF never emits an /StmF /Identity file, an indirect /O, a /Crypt
// filter, or a signature, so nothing in testdata/ exercises the code that
// handles them.
//
// These are routing tests, not crypto tests. Each asks "does the reader send
// these bytes down the right path", which is a question a hand-built Dict can
// answer — and, unlike an encryptor of our own, one where writing the input
// ourselves proves something, since the input is structure rather than
// ciphertext. Resolve short-circuits on r.cache, so an indirect reference needs
// no document behind it.

// --- /Crypt filter naming /Identity ------------------------------------------

func TestIdentityCryptFilterExemptsStream(t *testing.T) {
	identityParms := Dict{"Type": Name("CryptFilterDecodeParms"), "Name": Name("Identity")}

	cases := []struct {
		name string
		d    Dict
		want bool // want the stream decrypted?
	}{
		{
			name: "ordinary stream is decrypted",
			d:    Dict{"Filter": Name("FlateDecode")},
			want: true,
		},
		{
			name: "no filter at all is decrypted",
			d:    Dict{},
			want: true,
		},
		{
			name: "/Crypt naming /Identity opts out",
			d:    Dict{"Filter": Array{Name("Crypt")}, "DecodeParms": identityParms},
			want: false,
		},
		{
			name: "bare /Crypt defaults to /Identity",
			d:    Dict{"Filter": Array{Name("Crypt")}},
			want: false,
		},
		{
			name: "/Crypt naming a real filter still decrypts",
			d: Dict{
				"Filter":      Array{Name("Crypt")},
				"DecodeParms": Dict{"Name": Name("StdCF")},
			},
			want: true,
		},
		{
			// The parms array is positional: the /Crypt entry must read its own
			// slot, not the first one.
			name: "/Crypt second in the chain reads the matching parms slot",
			d: Dict{
				"Filter":      Array{Name("FlateDecode"), Name("Crypt")},
				"DecodeParms": Array{nil, identityParms},
			},
			want: false,
		},
		{
			// Malformed: /DecodeParms must be an array when /Filter is, so the
			// /Crypt entry has no parms of its own. /Name then falls to its
			// default of /Identity, which is also what the producer plainly
			// meant by writing those parms at all.
			name: "unmatched parms leaves /Crypt at its /Identity default",
			d: Dict{
				"Filter":      Array{Name("FlateDecode"), Name("Crypt")},
				"DecodeParms": identityParms,
			},
			want: false,
		},
	}

	r := &Reader{crypt: &encryptInfo{streams: cryptRC4}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := r.streamIsEncrypted(c.d); got != c.want {
				t.Errorf("streamIsEncrypted() = %v, want %v", got, c.want)
			}
		})
	}
}

// --- exemptions that survive the string walk ---------------------------------

// A cross-reference stream is exempt whole. Its body is skipped by
// streamIsEncrypted; this covers the dictionary, whose /ID would otherwise be
// transformed and cached in that state.
func TestXRefStreamDictIsNotStringDecrypted(t *testing.T) {
	r := &Reader{crypt: &encryptInfo{key: bytes.Repeat([]byte{1}, 16), strings: cryptRC4}}

	xref := &Stream{Dict: Dict{"Type": Name("XRef"), "ID": "plaintext-id"}}
	if got := r.decryptStrings(xref, 3, 0).(*Stream); got.Dict["ID"] != "plaintext-id" {
		t.Errorf("/ID = %q, want it untouched", got.Dict["ID"])
	}

	// Control: an ordinary stream's dictionary strings must still be decrypted,
	// or the exemption above would pass for the wrong reason.
	other := &Stream{Dict: Dict{"Type": Name("Page"), "ID": "plaintext-id"}}
	if got := r.decryptStrings(other, 3, 0).(*Stream); got.Dict["ID"] == "plaintext-id" {
		t.Error("an ordinary stream dictionary was left undecrypted")
	}
}

// A signature's /Contents signs the surrounding bytes, so the spec exempts it.
// Decrypting it corrupts a blob that Rewrite then copies verbatim.
func TestSignatureContentsIsNotDecrypted(t *testing.T) {
	e := &encryptInfo{key: bytes.Repeat([]byte{1}, 16), strings: cryptRC4}
	key := e.objectKey(4, 0, e.strings)

	sig := Dict{
		"Type":      Name("Sig"),
		"ByteRange": Array{0, 840, 960, 240},
		"Contents":  "signature-blob",
		"Name":      "signer name",
	}
	e.walkStrings(sig, key)

	if sig["Contents"] != "signature-blob" {
		t.Errorf("/Contents = %q, want it untouched", sig["Contents"])
	}
	// Every other string in the same dictionary is encrypted as usual, so the
	// skip has to be exactly one key wide.
	if sig["Name"] == "signer name" {
		t.Error("/Name was left undecrypted; the exemption is too broad")
	}

	// Without /ByteRange it is not a signature dictionary and nothing is spared.
	plain := Dict{"Contents": "signature-blob"}
	e.walkStrings(plain, key)
	if plain["Contents"] == "signature-blob" {
		t.Error("/Contents was spared in a dictionary that is not a signature")
	}
}

// --- indirect /O, /U, /UE, /OE -----------------------------------------------

// Producers occasionally write the password values indirectly. Reading them with
// a bare type assertion yields "", which fails every password — including the
// correct one — with no hint that the file was merely non-canonical.
func TestPasswordValuesResolveThroughIndirectRefs(t *testing.T) {
	r := &Reader{cache: map[int]any{9: "owner-value"}}

	if got := r.dictString(Dict{"O": "owner-value"}, "O"); got != "owner-value" {
		t.Errorf("direct /O = %q, want %q", got, "owner-value")
	}
	if got := r.dictString(Dict{"O": Ref{Num: 9}}, "O"); got != "owner-value" {
		t.Errorf("indirect /O = %q, want %q", got, "owner-value")
	}
	if got := r.dictString(Dict{}, "O"); got != "" {
		t.Errorf("absent /O = %q, want empty", got)
	}
}

// --- file key length across both crypt filters -------------------------------

// The file key is shared by both filters, so its length is the longer of the
// two. A file encrypting only its strings leaves /StmF as /Identity with no
// length of its own; taking the stream filter's alone falls back to the 40-bit
// default and decrypts every string under the wrong key.
func TestFileKeyLengthTakesLongerCryptFilter(t *testing.T) {
	enc := Dict{
		"CF": Dict{
			"Plain":  Dict{"CFM": Name("None")},
			"Strong": Dict{"CFM": Name("V2"), "Length": 16},
		},
	}
	r := &Reader{}

	if _, n := r.cryptFilter(enc, "Strong"); n != 16 {
		t.Fatalf("crypt filter length = %d, want 16", n)
	}
	if m, n := r.cryptFilter(enc, "Identity"); m != cryptNone || n != 0 {
		t.Fatalf("/Identity = (%v, %d), want (cryptNone, 0)", m, n)
	}

	// The bits-vs-bytes rule the same lookup applies: 40 and above can only be
	// bits, since no filter uses a key that long.
	bits := Dict{"CF": Dict{"F": Dict{"CFM": Name("V2"), "Length": 40}}}
	if _, n := r.cryptFilter(bits, "F"); n != 5 {
		t.Errorf("/Length 40 gave %d bytes, want 5", n)
	}
}

// The selection above is only half the fix; the other half is setupEncryption
// choosing between the two filters, which cryptFilter alone cannot show. This
// drives the real thing with MuPDF's own /O, /U and /ID lifted out of a fixture,
// so the key still has to derive correctly — no ciphertext is authored here,
// only the shape of the /Encrypt dictionary that selects the key length.
//
// V2/R3 and V4/R4 derive identical keys while /EncryptMetadata is true (the
// 0xFFFFFFFF step is gated on rev >= 4 && !encryptMetadata), so rc4-128's
// values stay valid after the conversion.
func TestStringsOnlyEncryptionDerivesFullLengthKey(t *testing.T) {
	src, err := Open(fixture(t, "rc4-128"), WithPassword(testUserPassword))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	orig, ok := src.ResolveDict(src.trailer["Encrypt"])
	if !ok {
		t.Fatal("fixture has no /Encrypt dictionary")
	}

	// /StmF names a filter carrying no /Length of its own; /StrF carries 16.
	// Omitting the top-level /Length leaves the 40-bit default in place, so a
	// reader that consults only /StmF derives a 5-byte key and fails the
	// password it should accept.
	r := &Reader{
		xref:       map[int]int64{},
		compressed: map[int]compressedRef{},
		cache:      map[int]any{},
		trailer: Dict{
			"ID": src.trailer["ID"],
			"Encrypt": Dict{
				"Filter": Name("Standard"),
				"V":      4,
				"R":      4,
				"P":      orig["P"],
				"O":      orig["O"],
				"U":      orig["U"],
				"CF": Dict{
					"NoLen":  Dict{"CFM": Name("V2")},
					"Strong": Dict{"CFM": Name("V2"), "Length": 16},
				},
				"StmF": Name("NoLen"),
				"StrF": Name("Strong"),
			},
		},
	}

	if err := r.setupEncryption(testUserPassword); err != nil {
		t.Fatalf("setupEncryption: %v (a 40-bit key would reject the correct password)", err)
	}
	if got := len(r.crypt.key); got != 16 {
		t.Errorf("file key = %d bytes, want 16", got)
	}
}
