package pdf

import (
	"bytes"
	"testing"
)

// Tests for the routing decisions the testdata/ fixtures cannot reach. MuPDF
// made those files, which is what lets them testify to cipher and key-derivation
// conformance — but it also means they only ever contain shapes MuPDF emits. It
// never writes an /StmF /Identity file, an indirect /O, a /Crypt filter or a
// signature, so nothing there exercises the code handling them.
//
// These ask which path bytes take, not whether a cipher is right, and that is a
// question a hand-built Dict can answer. Authoring the input proves something
// here — unlike authoring ciphertext, which would only show our encryptor agrees
// with our decryptor.
//
// Shares fixture and testUserPassword with crypt_test.go.

// --- /Crypt filter naming /Identity ------------------------------------------

func TestIdentityCryptFilterExemptsStream(t *testing.T) {
	identityParms := Dict{"Name": Name("Identity")}

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

// Both exemptions go through decryptStrings, the single entry point that derives
// the object key and walks. Each pairs its exemption with a control, since an
// assertion that a string is unchanged passes just as well when nothing is being
// decrypted at all.

func newStringDecrypter() *Reader {
	return &Reader{crypt: &encryptInfo{key: bytes.Repeat([]byte{1}, 16), strings: cryptRC4}}
}

// A cross-reference stream is exempt whole. Its body is skipped by
// streamIsEncrypted; this covers the dictionary, whose /ID would otherwise be
// transformed and cached in that state.
func TestXRefStreamDictIsNotStringDecrypted(t *testing.T) {
	r := newStringDecrypter()

	xref := &Stream{Dict: Dict{"Type": Name("XRef"), "ID": "plaintext-id"}}
	if got := r.decryptStrings(xref, 3, 0).(*Stream); got.Dict["ID"] != "plaintext-id" {
		t.Errorf("/ID = %q, want it untouched", got.Dict["ID"])
	}

	other := &Stream{Dict: Dict{"Type": Name("Page"), "ID": "plaintext-id"}}
	if got := r.decryptStrings(other, 3, 0).(*Stream); got.Dict["ID"] == "plaintext-id" {
		t.Error("an ordinary stream dictionary was left undecrypted")
	}
}

// A signature's /Contents signs the surrounding bytes, so the spec exempts it.
// Decrypting it corrupts a blob that Rewrite then copies verbatim.
func TestSignatureContentsIsNotDecrypted(t *testing.T) {
	r := newStringDecrypter()

	// Only the presence of /ByteRange marks the dictionary as a signature.
	sig := Dict{"ByteRange": Array{}, "Contents": "signature-blob", "Name": "signer name"}
	r.decryptStrings(sig, 4, 0)

	if sig["Contents"] != "signature-blob" {
		t.Errorf("/Contents = %q, want it untouched", sig["Contents"])
	}
	// Every other string in the same dictionary is encrypted as usual, so the
	// skip has to be exactly one key wide.
	if sig["Name"] == "signer name" {
		t.Error("/Name was left undecrypted; the exemption is too broad")
	}

	plain := Dict{"Contents": "signature-blob"}
	r.decryptStrings(plain, 4, 0)
	if plain["Contents"] == "signature-blob" {
		t.Error("/Contents was spared in a dictionary that is not a signature")
	}
}

// --- indirect /O, /U, /UE, /OE -----------------------------------------------

// Producers occasionally write the password values indirectly. Reading them with
// a bare type assertion yields "", which fails every password — including the
// correct one — with no hint that the file was merely non-canonical. Resolve
// short-circuits on r.cache, so the reference needs no document behind it.
func TestPasswordValuesResolveThroughIndirectRefs(t *testing.T) {
	r := &Reader{cache: map[int]any{9: "owner-value"}}

	if got := r.dictString(Dict{"O": "owner-value"}, "O"); got != "owner-value" {
		t.Errorf("direct /O = %q, want %q", got, "owner-value")
	}
	if got := r.dictString(Dict{"O": Ref{Num: 9}}, "O"); got != "owner-value" {
		t.Errorf("indirect /O = %q, want %q", got, "owner-value")
	}
}

// --- crypt filter key length -------------------------------------------------

// A crypt filter's /Length is specified in bytes, but many producers write bits.
// 40 and above can only be bits, since no filter uses a key that long.
func TestCryptFilterLengthInBits(t *testing.T) {
	r := &Reader{}
	for _, c := range []struct{ length, want int }{{16, 16}, {40, 5}, {128, 16}} {
		enc := Dict{"CF": Dict{"F": Dict{"CFM": Name("V2"), "Length": c.length}}}
		if _, n := r.cryptFilter(enc, "F"); n != c.want {
			t.Errorf("/Length %d gave %d bytes, want %d", c.length, n, c.want)
		}
	}
}

// The file key is shared by both crypt filters, so its length is the longer of
// the two. A file encrypting only its strings leaves /StmF pointing at a filter
// with no length of its own, and consulting that alone falls back to the 40-bit
// default — deriving a 5-byte key that rejects the correct password.
//
// This drives setupEncryption with MuPDF's own /O, /U and /ID lifted out of a
// fixture, so the key still has to derive correctly. No ciphertext is authored;
// only the shape of the /Encrypt dictionary that selects the length. V2/R3 and
// V4/R4 derive identical keys while /EncryptMetadata is true (the 0xFFFFFFFF
// step is gated on rev >= 4 && !encryptMetadata), so rc4-128's values stay valid
// across the conversion.
func TestFileKeyLengthTakesLongerCryptFilter(t *testing.T) {
	src, err := Open(fixture(t, "rc4-128"), WithPassword(testUserPassword))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	orig, ok := src.ResolveDict(src.trailer["Encrypt"])
	if !ok {
		t.Fatal("fixture has no /Encrypt dictionary")
	}

	r := &Reader{trailer: Dict{
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
	}}

	if err := r.setupEncryption(testUserPassword); err != nil {
		t.Fatalf("setupEncryption: %v (a 40-bit key would reject the correct password)", err)
	}
	if got := len(r.crypt.key); got != 16 {
		t.Errorf("file key = %d bytes, want 16", got)
	}
}
