package driver

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestParseBindOptions(t *testing.T) {
	cases := []struct {
		name      string
		in        []string
		wantFlags uintptr
		wantData  string
		wantErr   bool
	}{
		{"plain bind", []string{"bind"}, 0, "", false},
		{"ro", []string{"bind", "ro"}, unix.MS_RDONLY, "", false},
		{"ro+noatime", []string{"bind", "ro", "noatime"}, unix.MS_RDONLY | unix.MS_NOATIME, "", false},
		{"hardening trio", []string{"bind", "nosuid", "nodev", "noexec"}, unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC, "", false},
		{"unknown -> data", []string{"bind", "discard", "user_xattr"}, 0, "discard,user_xattr", false},
		{"missing bind", []string{"ro"}, 0, "", true},
		{"empty", nil, 0, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			flags, data, err := parseBindOptions(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("got (%v, %q, nil); want error", flags, data)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if flags != c.wantFlags {
				t.Errorf("flags = %#x; want %#x", flags, c.wantFlags)
			}
			if data != c.wantData {
				t.Errorf("data = %q; want %q", data, c.wantData)
			}
		})
	}
}
