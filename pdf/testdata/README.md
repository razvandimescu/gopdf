# Encryption fixtures

`base.pdf` is a one-page document holding the text `Encrypted Hello` and an
`/Info` dictionary with `/Title (confidential title)`. Every other file here is
that same document encrypted by [MuPDF](https://mupdf.com), so the encrypted
variants and the plaintext control are directly comparable.

MuPDF is used deliberately. An encryptor written alongside `crypt.go` can only
show that our two halves agree with each other, which any invertible transform
satisfies — including a wrong one. Only a foreign producer can testify to
conformance, and only a real one carries the quirks that matter: these files
write `/Length` in bits at the top level but in bytes inside `/CF`, and they set
`/Perms` and `/AuthEvent`, none of which a fixture built to match our
expectations would have exercised.

## Regenerating

Requires `mutool` (`brew install mupdf-tools`). From this directory:

```sh
for s in rc4-40 rc4-128 aes-128 aes-256; do
    mutool clean -m -E $s -U user-secret -O owner-secret base.pdf $s.pdf
    mutool clean -m -E $s -O owner-secret base.pdf $s-nouser.pdf
done
```

The `-nouser` variants have an empty user password — the common case for emailed
bank statements, which must open through the ordinary entry points with no
password supplied.

Passwords are fixed in `crypt_test.go`. They protect nothing; these files hold no
secrets and exist only to be decrypted by the test suite.

`base.pdf` itself was written by hand and normalised with `mutool clean -m`,
which rebuilt its cross-reference table. It needs regenerating only if the test's
expected text or title changes.
