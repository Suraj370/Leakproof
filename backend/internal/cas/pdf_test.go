package cas

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second) // guards against a hung decrypt loop
	t.Cleanup(cancel)
	return c
}

func assertParsed(t *testing.T, r *Result) {
	t.Helper()
	if r.Source != "CAMS" || len(r.Positions) != 2 ||
		r.Positions[0].FundISIN != "INFDEMO00001" || r.Positions[1].FundISIN != "INFDEMO00002" {
		t.Fatalf("%+v", r)
	}
	if r.Positions[0].ValuePaise() != 9_000_000 || r.Positions[1].ValuePaise() != 1_320_000 {
		t.Fatalf("values %d %d", r.Positions[0].ValuePaise(), r.Positions[1].ValuePaise())
	}
}

func TestParsePDFUnencrypted(t *testing.T) {
	r, err := ParsePDF(ctx(t), fixture(t, "cas_sample.pdf"), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertParsed(t, r)
}

func TestParsePDFAES128(t *testing.T) {
	r, err := ParsePDF(ctx(t), fixture(t, "cas_sample_aes128.pdf"), []byte("userpw"))
	if err != nil {
		t.Fatal(err)
	}
	assertParsed(t, r)
}

func TestParsePDFRC4(t *testing.T) {
	r, err := ParsePDF(ctx(t), fixture(t, "cas_sample_rc4.pdf"), []byte("userpw"))
	if err != nil {
		t.Fatal(err)
	}
	assertParsed(t, r)
}

func TestWrongOrMissingPassword(t *testing.T) {
	for _, pw := range [][]byte{[]byte("nope"), nil, []byte("")} {
		_, err := ParsePDF(ctx(t), fixture(t, "cas_sample_aes128.pdf"), pw)
		if !errors.Is(err, ErrWrongPassword) {
			t.Fatalf("password %q: got %v", pw, err)
		}
	}
}

func TestAES256IsReportedAsUnsupportedNotCorrupt(t *testing.T) {
	_, err := ParsePDF(ctx(t), fixture(t, "cas_sample_aes256.pdf"), []byte("userpw"))
	if !errors.Is(err, ErrUnsupportedEncryption) {
		t.Fatalf("got %v", err)
	}
}

func TestGarbageInputDoesNotPanic(t *testing.T) {
	for _, in := range [][]byte{nil, []byte("not a pdf"), []byte("%PDF-1.4\n1 0 obj\n<<"), fixture(t, "cas_sample.txt")} {
		if _, err := ParsePDF(ctx(t), in, nil); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
}

func TestCancelledContext(t *testing.T) {
	c, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ExtractText(c, fixture(t, "cas_sample.pdf"), nil); !errors.Is(err, context.Canceled) {
		// the extraction may legitimately win the race; only a non-nil unrelated error is a failure
		if err != nil {
			t.Fatalf("got %v", err)
		}
	}
}
