package docker

// files.go owns the in-container file-browser primitives that
// power the admin UI's Files tab. The API surface is small:
//   - ListContainerDir  → directory listing (name / size / mode / mtime / type)
//   - ReadContainerFile → read a single file's bytes (with a hard size cap)
//   - ContainerIDByName → resolve a container name to its docker ID
//   - ContainerStatPath → one-shot stat for a path (size, mode, mtime, isDir)
//
// All four run as the calling process, talking to the docker engine
// over the same client the rest of the package uses. There is no
// auth layer at this level — the caller (internal/api) is responsible
// for verifying that the requested container belongs to the app
// the user asked about before reaching into it. The helpers here
// treat any container ID they're given as a target they can poke.

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/pkg/stdcopy"
)

// filtersListByName is the small helper that builds the
// `name=<name>` filter args used to resolve a container by
// name. It's a one-liner; keeping it next to the call site
// would just be visual noise.
func filtersListByName(name string) filters.Args {
	args := filters.NewArgs()
	args.Add("name", name)
	return args
}

// FileInfo is a single entry in a directory listing. Fields are
// picked to cover what a typical "Files" tab wants to show:
// name for display, size in bytes, mode for the permission
// column, modTime for the mtime column, and the trio of boolean
// flags for distinguishing directories from symlinks. The
// combination of mode + isDir is technically redundant (the
// first byte of mode already encodes the type) but having
// IsDir/IsLink as explicit flags keeps the UI's row rendering
// simple — it doesn't have to remember which position in the
// mode string means what.
type FileInfo struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	Mode       string    `json:"mode"`
	ModTime    time.Time `json:"modTime"`
	IsDir      bool      `json:"isDir"`
	IsLink     bool      `json:"isLink"`
	LinkTarget string    `json:"linkTarget,omitempty"`
}

// ListContainerDir returns the direct children of `path` inside
// `containerID`. The path must be absolute — relative paths are
// rejected because the user-facing API always uses absolute
// container paths and silently rewriting a relative one would
// hide bugs in the caller.
//
// The implementation runs `ls -lan` via the exec API rather than
// reaching for the docker /containers/{id}/archive endpoint.
// `ls` is in every image (busybox + coreutils), doesn't require
// building a tar parser, and gives us a single, fast round-trip
// even on very large directories. The price is parsing
// vendor-specific output — the parser at the bottom of this file
// handles both busybox and coreutils because the *first eight*
// whitespace-separated fields are identical: mode, nlinks, uid,
// gid, size, month, day, time|year.
func (m *Manager) ListContainerDir(ctx context.Context, containerID, path string) ([]FileInfo, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("path must be absolute: %q", path)
	}
	stdout, err := m.execCapture(ctx, containerID, []string{"ls", "-lan", "--", path})
	if err != nil {
		return nil, err
	}
	return parseLsOutput(stdout), nil
}

// ReadContainerFile returns the bytes of a single regular file at
// `path` inside `containerID`, capped at `maxBytes`. The cap is
// applied both as a pre-flight stat (so we fail fast without
// reading the file) and as a read-side limit (in case the stat
// is unavailable or racy). Returns the actual size, the file's
// mode string, and the modtime. Errors out if the path resolves
// to a directory or to anything other than a regular file.
func (m *Manager) ReadContainerFile(ctx context.Context, containerID, path string, maxBytes int64) ([]byte, int64, string, time.Time, error) {
	if !filepath.IsAbs(path) {
		return nil, 0, "", time.Time{}, fmt.Errorf("path must be absolute: %q", path)
	}
	if maxBytes <= 0 {
		return nil, 0, "", time.Time{}, fmt.Errorf("maxBytes must be positive")
	}

	// Pre-flight stat to reject directories without ever pulling
	// the file. ContainerStat returns the resolved entry, so
	// following symlinks here is what we want — if the user
	// clicks through a symlink to a directory, we should tell
	// them "that's a directory" rather than returning the
	// directory's tar listing. The docker SDK doesn't expose an
	// IsDir flag on PathStat, so we derive it from the mode's
	// high bit — the same bit the OS uses for directory entries.
	stat, err := m.cli.ContainerStatPath(ctx, containerID, path)
	if err != nil {
		return nil, 0, "", time.Time{}, fmt.Errorf("stat %s: %w", path, err)
	}
	if stat.Mode.IsDir() {
		return nil, 0, "", time.Time{}, fmt.Errorf("path is a directory: %s", path)
	}
	if stat.Size > maxBytes {
		return nil, 0, "", time.Time{}, fmt.Errorf("file too large: %d bytes (max %d)", stat.Size, maxBytes)
	}

	rc, _, err := m.cli.CopyFromContainer(ctx, containerID, path)
	if err != nil {
		return nil, 0, "", time.Time{}, fmt.Errorf("copy %s: %w", path, err)
	}
	defer rc.Close()

	tr := tar.NewReader(rc)
	hdr, err := tr.Next()
	if err != nil {
		return nil, 0, "", time.Time{}, fmt.Errorf("read tar header: %w", err)
	}
	if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
		return nil, 0, "", time.Time{}, fmt.Errorf("not a regular file: typeflag=%d", hdr.Typeflag)
	}

	// hdr.Size is the upstream-reported size; we still cap our
	// read at maxBytes in case the tar header lies (it can —
	// some older builds report 0 for sparse files). Reading into
	// a bounded buffer means a maliciously-large size can't OOM
	// the process.
	limit := hdr.Size
	if limit <= 0 || limit > maxBytes {
		limit = maxBytes
	}
	buf := bytes.NewBuffer(make([]byte, 0, limit))
	if _, err := io.Copy(buf, io.LimitReader(tr, maxBytes+1)); err != nil {
		return nil, 0, "", time.Time{}, fmt.Errorf("read file: %w", err)
	}
	if buf.Len() > int(maxBytes) {
		return nil, 0, "", time.Time{}, fmt.Errorf("file too large: exceeds %d bytes", maxBytes)
	}

	return buf.Bytes(), stat.Size, stat.Mode.String(), stat.Mtime, nil
}

// ContainerIDByName resolves a container name to its docker ID.
// The resolution uses the same "name" filter as the rest of the
// package: a single ContainerList call with a `name=<name>` filter
// and a strict equality check on the trimmed first name, so we
// don't pick up `nanoku-foo` when looking for `nanoku-fo`. The
// returned empty string + nil error means "no such container";
// the caller decides whether that's a 404 or something else.
func (m *Manager) ContainerIDByName(ctx context.Context, name string) (string, error) {
	args := filtersListByName(name)
	list, err := m.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return "", fmt.Errorf("container list: %w", err)
	}
	for _, c := range list {
		for _, n := range c.Names {
			if strings.TrimPrefix(n, "/") == name {
				return c.ID, nil
			}
		}
	}
	return "", nil
}

// ContainerStatPath returns the container's view of a path's
// metadata. This is the same call ReadContainerFile uses for
// its pre-flight; exposed at package level so the API layer
// can answer "is this a directory?" without first reading
// anything.
func (m *Manager) ContainerStatPath(ctx context.Context, containerID, path string) (container.PathStat, error) {
	if !filepath.IsAbs(path) {
		return container.PathStat{}, fmt.Errorf("path must be absolute: %q", path)
	}
	stat, err := m.cli.ContainerStatPath(ctx, containerID, path)
	if err != nil {
		return container.PathStat{}, fmt.Errorf("stat %s: %w", path, err)
	}
	return stat, nil
}

// execCapture runs `cmd` inside `containerID` and returns the
// combined stdout. Stderr is folded into the returned error so
// the caller (usually a "command failed: <stderr>" wrap) can
// surface the actual reason — a missing directory, a permission
// denial, etc. — instead of just "exit code 1". The exec is
// started before we attach so the attach call gets a hijacked
// connection that streams stdout as it's produced; the goroutine
// drains the framed stream into the buffer via stdcopy.
func (m *Manager) execCapture(ctx context.Context, containerID string, cmd []string) (string, error) {
	execCfg := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}
	exec, err := m.cli.ContainerExecCreate(ctx, containerID, execCfg)
	if err != nil {
		return "", fmt.Errorf("exec create: %w", err)
	}
	hijacked, err := m.cli.ContainerExecAttach(ctx, exec.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("exec attach: %w", err)
	}
	defer hijacked.Close()

	var stdout, stderr bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		// StdCopy demuxes the engine's 8-byte-framed stream into
		// separate stdout/stderr. It returns when the connection
		// is closed, which happens when the exec finishes.
		_, _ = stdcopy.StdCopy(&stdout, &stderr, hijacked.Conn)
	}()
	if err := m.cli.ContainerExecStart(ctx, exec.ID, container.ExecStartOptions{}); err != nil {
		return "", fmt.Errorf("exec start: %w", err)
	}
	<-done

	inspect, err := m.cli.ContainerExecInspect(ctx, exec.ID)
	if err != nil {
		return "", fmt.Errorf("exec inspect: %w", err)
	}
	if inspect.ExitCode != 0 {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail != "" {
			return "", fmt.Errorf("exec failed (exit %d): %s", inspect.ExitCode, detail)
		}
		return "", fmt.Errorf("exec failed (exit %d)", inspect.ExitCode)
	}
	return stdout.String(), nil
}

// parseLsOutput turns `ls -lan` output into FileInfo rows. The
// parser is deliberately tolerant: it ignores the "total N"
// summary line, skips "." / ".." self/parent entries, and bails
// out quietly on any line that doesn't look like an ls row
// (e.g. a stderr line that snuck through). Both busybox and
// coreutils produce the same first 9 whitespace-separated
// fields — mode, nlinks, uid, gid, size, month, day, time|year,
// name — and we use that contract instead of trying to be clever
// about column positions, which differ between the two.
//
// Symlinks carry a "name -> target" tail in both vendors; we
// split on the first " -> " and treat the LHS as the name and
// the RHS as the link target. The mode's first byte still
// marks it as a symlink, so the IsLink flag is set off `mode[0]`
// rather than the "->" text.
//
// "year" position: when ls decides the file is older than
// 6 months, it swaps the "HH:MM" field for a 4-digit year.
// We don't try to detect which we're looking at — we just
// join the trailing fields and let the name-with-arrow
// splitter handle it. The modtime we report is whatever
// parseLsDate makes of the month + day pair, which loses
// precision in that case but is at least in the right
// ballpark.
func parseLsOutput(s string) []FileInfo {
	var out []FileInfo
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" {
			continue
		}
		// "total 1234" — ls's directory-size summary.
		if strings.HasPrefix(line, "total ") {
			continue
		}
		// First byte must be a file-type marker; anything else
		// (a stray stderr line, a "Permission denied" suffix
		// concatenated onto a previous line, etc.) gets skipped.
		if len(line) < 1 {
			continue
		}
		switch line[0] {
		case 'd', '-', 'l', 'c', 'b', 'p', 's':
			// valid type marker
		default:
			continue
		}

		// Field layout:
		//   0: mode      (e.g. -rw-r--r--)
		//   1: nlinks
		//   2: uid       (numeric under -n)
		//   3: gid       (numeric under -n)
		//   4: size      (bytes)
		//   5: month     (Jan, Feb, ...)
		//   6: day       (1, 2, ..., 31)
		//   7: time|year (HH:MM for recent, YYYY for older)
		//   8+: name     (possibly with spaces; may contain " -> target" for symlinks)
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		mode := fields[0]
		size, _ := strconv.ParseInt(fields[4], 10, 64)
		rest := strings.Join(fields[8:], " ")

		var name, linkTarget string
		if i := strings.Index(rest, " -> "); i >= 0 {
			name = rest[:i]
			linkTarget = rest[i+len(" -> "):]
		} else {
			name = rest
		}
		if name == "." || name == ".." {
			continue
		}

		out = append(out, FileInfo{
			Name:       name,
			Size:       size,
			Mode:       mode,
			ModTime:    parseLsDate(fields[5], fields[6]),
			IsDir:      mode[0] == 'd',
			IsLink:     mode[0] == 'l',
			LinkTarget: linkTarget,
		})
	}
	return out
}

// parseLsDate turns an ls "Mon Day" pair into a time.Time. The
// year is taken from the current year; the "older than 6 months"
// rule that ls uses to switch from "HH:MM" to "YYYY" is ignored
// because we don't need second-level precision and the UI's
// relative-time formatter handles "in this year" vs "earlier"
// just fine. If the field doesn't look like a month name we
// return the zero time rather than guessing — the UI then shows
// the modtime as "—" instead of an obviously-wrong date.
func parseLsDate(month, day string) time.Time {
	now := time.Now()
	t, err := time.Parse("Jan _2 2006", month+" "+day+" "+strconv.Itoa(now.Year()))
	if err != nil {
		return time.Time{}
	}
	return t
}
