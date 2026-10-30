package relay

import (
	"strings"
	"testing"
)

func TestRedactingWriterStripsQueries(t *testing.T) {
	cases := []struct {
		name   string
		writes []string
		want   string
	}{
		{"plain line", []string{"warning: slow segment\n"}, "warning: slow segment\n"},
		{"https query", []string{"Unable to open URL: https://edge.example/a/b.m3u8?token=SECRET&e=1 (403)\n"},
			"Unable to open URL: https://edge.example/a/b.m3u8?<redacted> (403)\n"},
		{"hls scheme", []string{"No playable streams found on this URL: hls://https://edge.example/x.m3u8?sig=SECRET\n"},
			"No playable streams found on this URL: hls://https://edge.example/x.m3u8?<redacted>\n"},
		{"split across writes", []string{"a https://h.example/p?tok", "en=SECRET b\n"}, "a https://h.example/p?<redacted> b\n"},
		{"trailing partial line flushed", []string{"tail https://h.example/p?k=SECRET"}, "tail https://h.example/p?<redacted>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			w := &redactingWriter{out: &out}
			for _, s := range tc.writes {
				if n, err := w.Write([]byte(s)); err != nil || n != len(s) {
					t.Fatalf("Write(%q) = %d, %v", s, n, err)
				}
			}
			w.Flush()
			if got := out.String(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
