package mf

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
)

// NewSalt returns a random per-request salt. It is never stored, so folio hashes cannot be linked
// across requests or reversed from a database dump (there is none in guest mode).
func NewSalt() []byte {
	s := make([]byte, 16)
	if _, err := rand.Read(s); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return s
}

// RedactFolio replaces a folio number with a short salted HMAC.
func RedactFolio(folio string, salt []byte) string {
	h := hmac.New(sha256.New, salt)
	h.Write([]byte(folio))
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// Zero overwrites b. Use in defer for PDF bytes and passwords we own. (Go's GC may have moved copies;
// this is best-effort hygiene on top of never persisting anything.)
func Zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

var (
	rePAN   = regexp.MustCompile(`\b[A-Z]{5}[0-9]{4}[A-Z]\b`)
	reEmail = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	rePhone = regexp.MustCompile(`(?:\+91[\-\s]?)?\b[6-9][0-9]{9}\b`)
)

// ScrubPII masks PAN, email and mobile numbers in free text (e.g. before logging a parse error).
func ScrubPII(s string) string {
	s = rePAN.ReplaceAllString(s, "[PAN]")
	s = reEmail.ReplaceAllString(s, "[EMAIL]")
	return rePhone.ReplaceAllString(s, "[PHONE]")
}
