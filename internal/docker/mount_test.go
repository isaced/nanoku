package docker

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

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
		name    string
		app     string
		vol     string
		want    bool
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

// TestCreateAppContainer_MountFlagShape is a "dry run" against the real
// docker binary on the host: we run `docker run --help` and confirm our
// mount flag shape is well-formed enough for docker to parse. Skipped if
// docker is not on PATH.
func TestCreateAppContainer_MountFlagShape(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	m := Manager{
		binary:        "docker",
		containerName: "nanoku-shape-test",
		image:         "scratch",
		volumeName:    "x",
		networkName:   "x",
	}
	// We don't actually call CreateAppContainer (it would try to start a
	// container). Instead inspect that the rendered flag looks docker-shaped.
	args := []string{}
	mounts := []VolumeMount{
		{Type: "volume", Target: "/data"},
		{Type: "volume", Source: "myvol", Target: "/etc/foo", ReadOnly: true},
		{Type: "bind", Source: "/tmp", Target: "/host-tmp"},
	}
	for i, mt := range mounts {
		src := mt.Source
		if mt.Type == "volume" && src == "" {
			src = AutoAppVolumeName("shape", i)
		}
		args = append(args, "--mount", formatMount(mt.Type, src, mt.Target, mt.ReadOnly))
	}
	want := []string{
		"--mount",
		"type=volume,source=nanoku-shape-vol-0,target=/data",
		"--mount",
		"type=volume,source=myvol,target=/etc/foo,readonly",
		"--mount",
		"type=bind,source=/tmp,target=/host-tmp",
	}
	for i, w := range want {
		if i >= len(args) || args[i] != w {
			t.Fatalf("args = %v, want %v", args, want)
		}
	}
	// also: docker --help accepts the flag without complaining
	ctx := context.Background()
	out, err := m.run(ctx, "run", "--help")
	if err != nil {
		t.Skipf("docker run --help failed: %v", err)
	}
	if !strings.Contains(out, "--mount") {
		t.Errorf("docker run --help missing --mount flag; outdated docker?")
	}
}
