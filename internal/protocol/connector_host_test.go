package protocol

import "testing"

func TestClassifyHost(t *testing.T) {
	for _, tc := range []struct{ ua, want string }{
		{"", HostUnknown},
		{"   ", HostUnknown},
		{"Mozilla/5.0 (Macintosh) AppleWebKit/537.36 (KHTML, like Gecko) edadesk/0.0.1 Chrome/152.0.7977.130 Electron/44.4.5 Safari/537.36", HostEdadesk},
		{"Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 lceda-pro/3.2.149 Chrome/120.0 Electron/28.2.0 Safari/537.36", HostElectron},
		{"Mozilla/5.0 (Macintosh) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36", HostBrowser},
		{"Mozilla/5.0 (notes: try edadesk later) Chrome/141.0 Safari/537.36", HostBrowser},
	} {
		got := ClassifyHost(tc.ua)
		if got.Kind != tc.want {
			t.Errorf("ClassifyHost(%q).Kind = %q, want %q", tc.ua, got.Kind, tc.want)
		}
		if tc.want == HostUnknown && got.UserAgent != "" {
			t.Errorf("unknown host must not carry a user agent: %+v", got)
		}
	}
}
