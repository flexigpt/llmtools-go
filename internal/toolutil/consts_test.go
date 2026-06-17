package toolutil

import "testing"

func TestConstants(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "GOOSLinux", got: GOOSLinux, want: "linux"},
		{name: "GOOSWindows", got: GOOSWindows, want: "windows"},
		{name: "GOOSDarwin", got: GOOSDarwin, want: "darwin"},
		{name: "GOOSFreebsd", got: GOOSFreebsd, want: "freebsd"},
		{name: "GOOSOpenbsd", got: GOOSOpenbsd, want: "openbsd"},
		{name: "GOOSNetbsd", got: GOOSNetbsd, want: "netbsd"},
		{name: "GOOSDragonfly", got: GOOSDragonfly, want: "dragonfly"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("unexpected value: got %q want %q", tc.got, tc.want)
			}
		})
	}

	const wantMaxBytes = 16 * 1024 * 1024
	if maxToolBytes != wantMaxBytes {
		t.Fatalf("unexpected maxToolBytes: got %d want %d", maxToolBytes, wantMaxBytes)
	}
	if MaxTextProcessingBytes != maxToolBytes {
		t.Fatalf(
			"MaxTextProcessingBytes should match maxToolBytes: got %d want %d",
			MaxTextProcessingBytes,
			maxToolBytes,
		)
	}
	if MaxFileReadBytes != maxToolBytes {
		t.Fatalf("MaxFileReadBytes should match maxToolBytes: got %d want %d", MaxFileReadBytes, maxToolBytes)
	}
	if MaxFileWriteBytes != maxToolBytes {
		t.Fatalf("MaxFileWriteBytes should match maxToolBytes: got %d want %d", MaxFileWriteBytes, maxToolBytes)
	}
}
