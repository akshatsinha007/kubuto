package cmd

import "testing"

// TestMaskSecret locks down config show's redaction behavior: the API key
// must never appear in full, but enough of it must survive that a user can
// still tell which key is configured.
func TestMaskSecret(t *testing.T) {
	cases := map[string]string{
		"":                                  "",
		"short":                             "***",
		"kbt_test_5fb55aadeb592f0c089750d3": "kbt_...50d3",
		"12345678":                          "***", // exactly 8 chars: still masked fully
		"123456789":                         "1234...6789",
	}
	for in, want := range cases {
		got := maskSecret(in)
		if got != want {
			t.Errorf("maskSecret(%q) = %q, want %q", in, got, want)
		}
		if in != "" && got == in {
			t.Errorf("maskSecret(%q) returned the input unmodified", in)
		}
	}
}
