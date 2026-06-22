package docker

import (
	"testing"
)

func TestAutoAppVolumeName(t *testing.T) {
	if got := AutoAppVolumeName("blog", 0); got != "nanoku-blog-vol-0" {
		t.Errorf("idx 0 = %q", got)
	}
	if got := AutoAppVolumeName("blog", 12); got != "nanoku-blog-vol-12" {
		t.Errorf("idx 12 = %q", got)
	}
}

func TestIsAutoAppVolumeName(t *testing.T) {
	cases := []struct {
		name string
		app  string
		vol  string
		want bool
	}{
		{"match", "blog", "nanoku-blog-vol-0", true},
		{"other app", "blog", "nanoku-other-vol-0", false},
		{"bare prefix", "blog", "nanoku-blog-vol-", false},
		{"unrelated", "blog", "my-data", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAutoAppVolumeName(tc.app, tc.vol); got != tc.want {
				t.Errorf("IsAutoAppVolumeName(%q, %q) = %v, want %v", tc.app, tc.vol, got, tc.want)
			}
		})
	}
}

func TestFormatMount(t *testing.T) {
	cases := []struct {
		name string
		in   VolumeMount
		want string
	}{
		{
			name: "volume rw",
			in:   VolumeMount{Type: "volume", Source: "myvol", Target: "/data"},
			want: "type=volume,source=myvol,target=/data",
		},
		{
			name: "bind ro",
			in:   VolumeMount{Type: "bind", Source: "/srv", Target: "/data", ReadOnly: true},
			want: "type=bind,source=/srv,target=/data,readonly",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatMount(tc.in.Type, tc.in.Source, tc.in.Target, tc.in.ReadOnly)
			if got != tc.want {
				t.Fatalf("formatMount = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMountType(t *testing.T) {
	cases := []struct {
		in   string
		want mountTypeResult
	}{
		{"bind", "bind"},
		{"volume", "volume"},
		{"tmpfs", "tmpfs"},
		{"", ""},
		{"BIND", "bind"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := string(mountType(tc.in)); got != string(tc.want) {
				t.Errorf("mountType(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// mountTypeResult is just a string alias to keep the table test readable.
type mountTypeResult = string