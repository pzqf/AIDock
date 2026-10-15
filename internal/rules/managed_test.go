package rules

import (
	"bytes"
	"errors"
	"testing"

	"github.com/pzqf/AIDock/internal/result"
)

func TestManagedRulesPreserveHandwrittenBytesOrderAndIdempotence(t *testing.T) {
	manual := []byte("\xef\xbb\xbf# 手写\r\n保留每个字节")
	want := []string{"多行一\n多行二", "重复", "重复"}
	r, err := Compile(manual, want, "")
	if err != nil || !bytes.HasPrefix(r.Output, manual) {
		t.Fatal(r, err)
	}
	before := append([]byte(r.Prefix), []byte(r.Managed+"\r\n末尾手写\r\n")...)
	r, err = Compile(before, want, r.Digest)
	if err != nil || !bytes.Equal(r.Output, before) {
		t.Fatal("not idempotent", r, err)
	}
	r2, err := Compile(before, []string{}, r.Digest)
	if err != nil || r2.Prefix != r.Prefix || r2.Suffix != r.Suffix || r2.Managed != Begin+"\n\n"+End {
		t.Fatal(r2, err)
	}
	if !bytes.Contains(r.Output, []byte("多行一\n多行二\n\n重复\n\n重复")) {
		t.Fatal("rules reordered or deduplicated")
	}
}
func TestManagedRulesNeverTakeOverAmbiguousOrDriftedContent(t *testing.T) {
	valid := Begin + "\na\n" + End
	for _, c := range []struct {
		name, text, owned, code string
		source                  []string
	}{
		{"unowned", valid, "", "RULES_OWNERSHIP_REQUIRED", nil},
		{"changed", valid, digest([]byte("old")), "RULES_MANAGED_DRIFT", nil},
		{"deleted", "manual", digest([]byte(valid)), "RULES_MANAGED_DRIFT", nil},
		{"missing-end", Begin, "", "RULES_MARKERS_INVALID", nil},
		{"reversed", End + "\n" + Begin, "", "RULES_MARKERS_INVALID", nil},
		{"nested", Begin + "\n" + valid + "\n" + End, "", "RULES_MARKERS_INVALID", nil},
		{"inline", "prefix " + valid, "", "RULES_MARKERS_INVALID", nil},
		{"unknown", "<!-- aidock:begin project -->", "", "RULES_MARKERS_INVALID", nil},
		{"invalid-utf8", string([]byte{0xff}), "", "RULES_ENCODING_UNSUPPORTED", nil},
		{"nul", "a\x00b", "", "RULES_ENCODING_UNSUPPORTED", nil},
		{"source-marker", "", "", "RULES_SOURCE_INVALID", []string{Begin}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Compile([]byte(c.text), c.source, c.owned)
			var e *result.Error
			if !errors.As(err, &e) || e.Code != c.code {
				t.Fatal(err)
			}
		})
	}
}
