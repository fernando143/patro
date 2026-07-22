package setup

import "testing"

func TestCellarOptPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "macos cellar path",
			in:   "/opt/homebrew/Cellar/patro/0.2.0/bin/patro",
			want: "/opt/homebrew/opt/patro/bin/patro",
		},
		{
			name: "linuxbrew cellar path",
			in:   "/home/linuxbrew/.linuxbrew/Cellar/patro/0.1.1/bin/patro",
			want: "/home/linuxbrew/.linuxbrew/opt/patro/bin/patro",
		},
		{
			name: "non-cellar path",
			in:   "/usr/local/bin/patro",
			want: "",
		},
		{
			name: "cellar without version segment",
			in:   "/opt/homebrew/Cellar/patro",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cellarOptPath(tc.in); got != tc.want {
				t.Errorf("cellarOptPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
