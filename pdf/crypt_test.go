package pdf

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures in testdata/ are produced by MuPDF, an independent implementation
// of the standard security handler. An encryptor of our own could only prove
// that our two halves agree with each other, which any invertible transform
// satisfies — including a wrong one. A foreign producer is the only thing that
// can testify to conformance. See testdata/README.md to regenerate them.

const (
	testUserPassword  = "user-secret"
	testOwnerPassword = "owner-secret"
	testPageText      = "Encrypted Hello"
	testTitle         = "confidential title"
)

// Every fixture is the same document as testdata/base.pdf, so an unencrypted
// control and each encrypted variant are directly comparable.
var encSchemes = []string{"rc4-40", "rc4-128", "aes-128", "aes-256"}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".pdf"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

// assertPageText checks that the document's only page decrypted to readable text.
func assertPageText(t *testing.T, doc *Document) {
	t.Helper()
	got, err := doc.Page(0).Text()
	if err != nil {
		t.Fatalf("extract text: %v", err)
	}
	if !strings.Contains(got, testPageText) {
		t.Errorf("page text = %q, want it to contain %q", got, testPageText)
	}
}

func TestOpenEncryptedWithUserPassword(t *testing.T) {
	for _, s := range encSchemes {
		t.Run(s, func(t *testing.T) {
			doc, err := OpenBytes(fixture(t, s), WithPassword(testUserPassword))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if !doc.IsEncrypted() {
				t.Error("IsEncrypted() = false, want true")
			}
			if got := doc.NumPages(); got != 1 {
				t.Fatalf("NumPages() = %d, want 1", got)
			}
			assertPageText(t, doc)
		})
	}
}

func TestOpenEncryptedWithOwnerPassword(t *testing.T) {
	for _, s := range encSchemes {
		t.Run(s, func(t *testing.T) {
			doc, err := OpenBytes(fixture(t, s), WithPassword(testOwnerPassword))
			if err != nil {
				t.Fatalf("open with owner password: %v", err)
			}
			assertPageText(t, doc)
		})
	}
}

// Strings live outside content streams and take a different code path, so they
// are checked separately.
func TestEncryptedStringsAreDecrypted(t *testing.T) {
	for _, s := range encSchemes {
		t.Run(s, func(t *testing.T) {
			r, err := Open(fixture(t, s), WithPassword(testUserPassword))
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
		t.Run(s, func(t *testing.T) {
			_, err := OpenBytes(fixture(t, s), WithPassword("not-the-password"))
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
		t.Run(s, func(t *testing.T) {
			if _, err := OpenBytes(fixture(t, s)); !errors.Is(err, ErrEncrypted) {
				t.Errorf("err = %v, want ErrEncrypted", err)
			}
		})
	}
}

// The common case for emailed statements: encrypted, but with no user password.
// These must open through the ordinary entry points.
func TestEmptyUserPasswordOpensWithoutPassword(t *testing.T) {
	for _, s := range encSchemes {
		t.Run(s, func(t *testing.T) {
			doc, err := OpenBytes(fixture(t, s+"-nouser"))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if !doc.IsEncrypted() {
				t.Error("IsEncrypted() = false, want true")
			}
			assertPageText(t, doc)
		})
	}
}

// base.pdf is the plaintext original every fixture was encrypted from, so this
// proves the hooks stay inert on the exact document they otherwise transform.
func TestUnencryptedFileIsUnaffected(t *testing.T) {
	doc, err := OpenBytes(fixture(t, "base"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if doc.IsEncrypted() {
		t.Error("IsEncrypted() = true for an unencrypted file")
	}
	assertPageText(t, doc)
}

// TestHash2BTermination pins Algorithm 2.B step (e) against explicit cases.
//
// The aes-256 fixture exercises this rule, but only along the path its own
// password happens to take: termination is data-dependent, so a file that stops
// at round 64 says nothing about the boundary at higher rounds. These cases are
// taken from the rule itself — stop when the last byte of E is no longer greater
// than the number of completed rounds minus 32.
func TestHash2BTermination(t *testing.T) {
	cases := []struct {
		rounds int
		lastE  byte
		want   bool
	}{
		{rounds: 1, lastE: 0, want: false},   // 64 rounds are mandatory
		{rounds: 63, lastE: 0, want: false},  // still one short
		{rounds: 64, lastE: 31, want: true},  // 31 is not greater than 64-32
		{rounds: 64, lastE: 32, want: true},  // boundary: equal, so stop
		{rounds: 64, lastE: 33, want: false}, // greater, so continue
		{rounds: 70, lastE: 38, want: true},
		{rounds: 70, lastE: 39, want: false},
		{rounds: 96, lastE: 255, want: false},
	}
	for _, c := range cases {
		if got := hash2BDone(c.rounds, c.lastE); got != c.want {
			t.Errorf("hash2BDone(%d, %d) = %v, want %v", c.rounds, c.lastE, got, c.want)
		}
	}
}
