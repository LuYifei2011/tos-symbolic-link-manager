package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	tosapp "github.com/LuYifei2011/tos-app-kit/go"
	"golang.org/x/sys/unix"
)

const maxEntries = 100000
const maxLinks = 5000

type fileEntry struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}
type linkEntry struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Target      string `json:"target"`
	Resolved    string `json:"resolved"`
	Status      string `json:"status"`
	Kind        string `json:"kind"`
	Modified    string `json:"modified"`
	Fingerprint string `json:"fingerprint"`
}
type scanResult struct {
	Links       []linkEntry   `json:"links"`
	Visited     int           `json:"visited"`
	Directories int           `json:"directories"`
	Warnings    []scanWarning `json:"warnings"`
	Skipped     int           `json:"skipped"`
	Partial     bool          `json:"partial"`
	Reason      string        `json:"reason"`
}
type scanWarning struct {
	Path string `json:"path"`
	Code string `json:"code"`
}
type fileAccess struct {
	roots []fileEntry
	check func(context.Context, string, bool) error
}

var mutations sync.Mutex

func safePath(p string) bool {
	if p == "" || len(p) > 4096 || !utf8.ValidString(p) || !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.Contains(p, "\\") || strings.ContainsFunc(p, unicode.IsControl) {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasPrefix(part, "@") || strings.EqualFold(part, "#recycle") || strings.HasPrefix(part, ".symlink-manager-") {
			return false
		}
	}
	return true
}
func inside(root, p string) bool { return p == root || strings.HasPrefix(p, root+"/") }
func (f *fileAccess) allowed(p string) bool {
	if !safePath(p) {
		return false
	}
	for _, root := range f.roots {
		if inside(root.Path, p) {
			return true
		}
	}
	return false
}
func (f *fileAccess) directory(ctx context.Context, p string, write bool) (*os.File, error) {
	if !f.allowed(p) {
		return nil, &apiError{409, "PATH_OUTSIDE_SHARES"}
	}
	if err := f.check(ctx, p, write); err != nil {
		return nil, err
	}
	return openDirectory(p)
}

// Pin each directory component using O_NOFOLLOW. A symlink swapped into any
// ancestor cannot redirect an operation into another directory.
func openDirectory(p string) (*os.File, error) {
	if !safePath(p) {
		return nil, &apiError{400, "PATH_INVALID"}
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		if part == "" {
			continue
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), p), nil
}
func readlinkAt(fd int, name string) (string, error) {
	b := make([]byte, 4097)
	n, e := unix.Readlinkat(fd, name, b)
	if e != nil {
		return "", e
	}
	if n >= len(b) {
		return "", &apiError{400, "TARGET_INVALID"}
	}
	return string(b[:n]), nil
}
func fingerprint(st *unix.Stat_t, target string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d:%v:%s", st.Dev, st.Ino, st.Mode, statModified(st), target)))
	return hex.EncodeToString(h[:])
}
func inspectLink(fd int, dir, name string) (linkEntry, error) {
	var st unix.Stat_t
	if e := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); e != nil {
		return linkEntry{}, e
	}
	if st.Mode&unix.S_IFMT != unix.S_IFLNK {
		return linkEntry{}, &apiError{409, "NOT_A_LINK"}
	}
	target, e := readlinkAt(fd, name)
	if e != nil {
		return linkEntry{}, e
	}
	p := filepath.Join(dir, name)
	resolved := target
	if !filepath.IsAbs(target) {
		resolved = dir + "/" + target
	}
	return linkEntry{Path: p, Name: name, Target: target, Resolved: resolved, Status: "unchecked", Kind: "unknown", Modified: statModified(&st).UTC().Format(time.RFC3339), Fingerprint: fingerprint(&st, target)}, nil
}

// Resolve every link hop and authorize the final parent before stat. This avoids
// probing targets outside the user's shares, including indirect link escapes.
func (f *fileAccess) targetStatus(ctx context.Context, p string) (string, string) {
	return f.resolveTarget(ctx, p, "")
}
func (f *fileAccess) resolveTarget(ctx context.Context, p, forbidden string) (string, string) {
	if !filepath.IsAbs(p) || strings.ContainsFunc(p, unicode.IsControl) {
		return "inaccessible", "unknown"
	}
	pending := strings.Split(p, "/")
	current := "/"
	hops := 0
	for len(pending) > 0 {
		if ctx.Err() != nil {
			return "unchecked", "unknown"
		}
		part := pending[0]
		pending = pending[1:]
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			current = filepath.Dir(current)
			continue
		}
		candidate := filepath.Join(current, part)
		permitted := false
		for _, root := range f.roots {
			if inside(root.Path, candidate) || inside(candidate, root.Path) {
				permitted = true
			}
		}
		if !permitted || !safePath(candidate) {
			return "outside", "unknown"
		}
		if candidate == forbidden {
			return "loop", "unknown"
		}
		if f.allowed(current) {
			if e := f.check(ctx, current, false); e != nil {
				return "inaccessible", "unknown"
			}
		}
		if f.allowed(candidate) && !f.allowed(current) {
			if e := f.check(ctx, candidate, false); e != nil {
				return "inaccessible", "unknown"
			}
		}
		dir, e := openDirectory(current)
		if e != nil {
			return targetError(e), "unknown"
		}
		var st unix.Stat_t
		e = unix.Fstatat(int(dir.Fd()), part, &st, unix.AT_SYMLINK_NOFOLLOW)
		if e != nil {
			dir.Close()
			return targetError(e), "unknown"
		}
		if st.Mode&unix.S_IFMT == unix.S_IFLNK {
			hops++
			if hops > 40 {
				dir.Close()
				return "loop", "unknown"
			}
			target, e := readlinkAt(int(dir.Fd()), part)
			dir.Close()
			if e != nil {
				return targetError(e), "unknown"
			}
			if filepath.IsAbs(target) {
				current = "/"
			}
			pending = append(strings.Split(target, "/"), pending...)
			continue
		}
		dir.Close()
		if st.Mode&unix.S_IFMT != unix.S_IFDIR {
			if len(pending) > 0 {
				return "broken", "unknown"
			}
			return "valid", "file"
		}
		current = candidate
	}
	if !f.allowed(current) {
		return "outside", "unknown"
	}
	if e := f.check(ctx, current, false); e != nil {
		return "inaccessible", "unknown"
	}
	return "valid", "directory"
}

func targetError(e error) string {
	if errors.Is(e, os.ErrNotExist) || errors.Is(e, syscall.ENOTDIR) {
		return "broken"
	}
	if errors.Is(e, syscall.ELOOP) {
		return "loop"
	}
	return "inaccessible"
}
func (f *fileAccess) scan(ctx context.Context, p string, recursive bool) (scanResult, error) {
	result := scanResult{Links: []linkEntry{}, Warnings: []scanWarning{}}
	start, err := f.directory(ctx, p, false)
	if err != nil {
		return result, err
	}
	start.Close()
	type task struct {
		path  string
		depth int
	}
	queue := []task{{p, 0}}
	warning := func(p string, e error) {
		result.Skipped++
		if len(result.Warnings) < 100 {
			result.Warnings = append(result.Warnings, scanWarning{p, errorCode(e)})
		}
	}
	deadline := time.Now().Add(15 * time.Second)
	for len(queue) > 0 {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if time.Now().After(deadline) {
			result.Partial = true
			result.Reason = "time"
			break
		}
		item := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		dir, e := f.directory(ctx, item.path, false)
		if e != nil {
			warning(item.path, e)
			continue
		}
		result.Directories++
		stop := false
		for !stop {
			entries, e := dir.ReadDir(256)
			if e != nil && e != io.EOF {
				warning(item.path, e)
				break
			}
			for _, entry := range entries {
				if ctx.Err() != nil {
					dir.Close()
					return result, ctx.Err()
				}
				if result.Visited >= maxEntries || len(result.Links) >= maxLinks || time.Now().After(deadline) {
					result.Partial = true
					result.Reason = "limit"
					stop = true
					break
				}
				result.Visited++
				path := filepath.Join(item.path, entry.Name())
				if !safePath(path) {
					continue
				}
				var st unix.Stat_t
				if err := unix.Fstatat(int(dir.Fd()), entry.Name(), &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
					warning(path, err)
					continue
				}
				if st.Mode&unix.S_IFMT == unix.S_IFLNK {
					link, e := inspectLink(int(dir.Fd()), item.path, entry.Name())
					if e != nil {
						warning(path, e)
						continue
					}
					link.Status, link.Kind = f.targetStatus(ctx, link.Resolved)
					result.Links = append(result.Links, link)
				} else if st.Mode&unix.S_IFMT == unix.S_IFDIR && recursive {
					if item.depth >= 128 {
						warning(path, &apiError{409, "DEPTH_LIMIT"})
					} else {
						queue = append(queue, task{path, item.depth + 1})
					}
				}
			}
			if e == io.EOF {
				break
			}
		}
		dir.Close()
		if result.Partial {
			break
		}
	}
	if result.Skipped > 0 {
		result.Partial = true
		if result.Reason == "" {
			result.Reason = "skipped"
		}
	}
	sort.Slice(result.Links, func(i, j int) bool { return result.Links[i].Path < result.Links[j].Path })
	return result, nil
}

type mutation struct {
	Path        string `json:"path"`
	Target      string `json:"target"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	AllowBroken bool   `json:"allowBroken"`
}

func randomName() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return ".symlink-manager-" + hex.EncodeToString(b[:])
}
func (f *fileAccess) mutate(ctx context.Context, action string, m mutation) (map[string]any, error) {
	mutations.Lock()
	defer mutations.Unlock()
	if !safePath(m.Path) || !f.allowed(filepath.Dir(m.Path)) {
		return nil, &apiError{400, "PATH_INVALID"}
	}
	parent := filepath.Dir(m.Path)
	name := filepath.Base(m.Path)
	if action == "rename" && (!safePath(filepath.Join(parent, m.Name)) || m.Name == "" || m.Name == "." || m.Name == ".." || strings.ContainsAny(m.Name, "/\\") || len(m.Name) > 255) {
		return nil, &apiError{400, "NAME_INVALID"}
	}
	if action == "create" || action == "update" {
		if m.Target == "" || len(m.Target) > 4096 || !utf8.ValidString(m.Target) || strings.ContainsFunc(m.Target, unicode.IsControl) || strings.Contains(m.Target, "\\") {
			return nil, &apiError{400, "TARGET_INVALID"}
		}
		target := m.Target
		if !filepath.IsAbs(target) {
			target = parent + "/" + target
		}
		if target == m.Path {
			return nil, &apiError{409, "TARGET_LOOP"}
		}
		status, _ := f.resolveTarget(ctx, target, m.Path)
		if status != "valid" && !(status == "broken" && m.AllowBroken) {
			return nil, &apiError{409, "TARGET_" + strings.ToUpper(status)}
		}
	}
	dir, err := f.directory(ctx, parent, true)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	fd := int(dir.Fd())
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if action == "create" {
		if err := unix.Symlinkat(m.Target, fd, name); err != nil {
			return nil, err
		}
		return map[string]any{"path": m.Path}, nil
	}
	before, err := inspectLink(fd, parent, name)
	if err != nil {
		return nil, err
	}
	if m.Fingerprint == "" || before.Fingerprint != m.Fingerprint {
		return nil, &apiError{409, "LINK_CHANGED"}
	}
	if action == "rename" && m.Name == name {
		return map[string]any{"path": m.Path}, nil
	}
	// Capture the source in a private directory. If another process replaces it
	// before capture, restore it without overwriting anything. No recursive deletes.
	stash := randomName()
	if err := unix.Mkdirat(fd, stash, 0700); err != nil {
		return nil, err
	}
	sfd, err := unix.Openat(fd, stash, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(sfd)
	defer unix.Unlinkat(fd, stash, unix.AT_REMOVEDIR)
	if err := renameNoReplace(fd, name, sfd, "original"); err != nil {
		return nil, err
	}
	restore := func(cause error) (map[string]any, error) {
		if e := renameNoReplace(sfd, "original", fd, name); e != nil {
			return map[string]any{"code": "RECOVERY_REQUIRED", "recoveryPath": filepath.Join(parent, stash, "original"), "error": filepath.Join(parent, stash, "original")}, &apiError{409, "RECOVERY_REQUIRED"}
		}
		return nil, cause
	}
	captured, err := inspectLink(sfd, parent, "original")
	if err != nil || captured.Fingerprint != m.Fingerprint {
		return restore(&apiError{409, "LINK_CHANGED"})
	}
	switch action {
	case "delete":
		err = unix.Unlinkat(sfd, "original", 0)
	case "rename":
		err = renameNoReplace(sfd, "original", fd, m.Name)
	case "update":
		err = unix.Symlinkat(m.Target, fd, name)
		if err == nil {
			if e := unix.Unlinkat(sfd, "original", 0); e != nil {
				return map[string]any{"code": "RECOVERY_REQUIRED", "recoveryPath": filepath.Join(parent, stash, "original"), "error": filepath.Join(parent, stash, "original")}, &apiError{409, "RECOVERY_REQUIRED"}
			}
		}
	default:
		err = &apiError{400, "ACTION_INVALID"}
	}
	if err != nil {
		return restore(err)
	}
	out := m.Path
	if action == "rename" {
		out = filepath.Join(parent, m.Name)
	}
	return map[string]any{"path": out}, nil
}
func errorCode(err error) string {
	var e *apiError
	if errors.As(err, &e) {
		return e.code
	}
	switch {
	case errors.Is(err, os.ErrExist):
		return "PATH_EXISTS"
	case errors.Is(err, os.ErrNotExist):
		return "PATH_NOT_FOUND"
	case errors.Is(err, os.ErrPermission), errors.Is(err, syscall.EROFS):
		return "FILESYSTEM_DENIED"
	case errors.Is(err, syscall.ELOOP), errors.Is(err, syscall.ENOTDIR):
		return "DIRECTORY_LINK_OR_INVALID"
	case errors.Is(err, context.DeadlineExceeded):
		return "REQUEST_TIMEOUT"
	case errors.Is(err, context.Canceled):
		return "REQUEST_CANCELLED"
	}
	return "FILESYSTEM_ERROR"
}
func (a *application) handleFiles(w http.ResponseWriter, r *http.Request, p string) {
	access, err := a.fileAccess(r)
	if err != nil {
		writeFailure(w, err)
		return
	}
	switch p {
	case "/api/files/stat":
		if !requireMethod(w, r, "GET") {
			return
		}
		p := r.URL.Query().Get("path")
		if !access.allowed(p) {
			writeFailure(w, &apiError{400, "PATH_INVALID"})
			return
		}
		var root bool
		for _, entry := range access.roots {
			if entry.Path == p {
				root = true
			}
		}
		if root {
			d, e := access.directory(r.Context(), p, false)
			if e != nil {
				writeFailure(w, e)
				return
			}
			d.Close()
			tosapp.WriteJSON(w, 200, map[string]any{"entry": fileEntry{p, filepath.Base(p), "directory"}})
			return
		}
		d, e := access.directory(r.Context(), filepath.Dir(p), false)
		if e != nil {
			writeFailure(w, e)
			return
		}
		defer d.Close()
		var st unix.Stat_t
		e = unix.Fstatat(int(d.Fd()), filepath.Base(p), &st, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(e, os.ErrNotExist) {
			tosapp.WriteJSON(w, 200, map[string]any{"entry": nil})
			return
		}
		if e != nil {
			writeFailure(w, e)
			return
		}
		if st.Mode&unix.S_IFMT == unix.S_IFLNK {
			writeFailure(w, &apiError{409, "DIRECTORY_LINK_OR_INVALID"})
			return
		}
		kind := "file"
		if st.Mode&unix.S_IFMT == unix.S_IFDIR {
			kind = "directory"
		}
		tosapp.WriteJSON(w, 200, map[string]any{"entry": fileEntry{p, filepath.Base(p), kind}})
	case "/api/files/list":
		if !requireMethod(w, r, "GET") {
			return
		}
		p := r.URL.Query().Get("path")
		if p == "" {
			tosapp.WriteJSON(w, 200, map[string]any{"directory": nil, "parentPath": nil, "entries": access.roots})
			return
		}
		dir, err := access.directory(r.Context(), p, false)
		if err != nil {
			writeFailure(w, err)
			return
		}
		defer dir.Close()
		entries := []fileEntry{}
		count := 0
		for {
			items, e := dir.ReadDir(256)
			if e != nil && e != io.EOF {
				writeFailure(w, e)
				return
			}
			for _, item := range items {
				count++
				if count > maxEntries {
					writeFailure(w, &apiError{413, "ENTRY_LIMIT"})
					return
				}
				path := filepath.Join(p, item.Name())
				if !safePath(path) {
					continue
				}
				var st unix.Stat_t
				if unix.Fstatat(int(dir.Fd()), item.Name(), &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
					continue
				}
				kind := "file"
				if st.Mode&unix.S_IFMT == unix.S_IFLNK {
					continue
				}
				if st.Mode&unix.S_IFMT == unix.S_IFDIR {
					kind = "directory"
				}
				entries = append(entries, fileEntry{path, item.Name(), kind})
			}
			if e == io.EOF {
				break
			}
			if r.Context().Err() != nil {
				writeFailure(w, r.Context().Err())
				return
			}
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Kind != entries[j].Kind {
				return entries[i].Kind == "directory"
			}
			return entries[i].Name < entries[j].Name
		})
		var parent any
		if access.allowed(filepath.Dir(p)) {
			parent = filepath.Dir(p)
		}
		tosapp.WriteJSON(w, 200, map[string]any{"directory": fileEntry{p, filepath.Base(p), "directory"}, "parentPath": parent, "entries": entries})
	case "/api/scan":
		if !requireMethod(w, r, "POST") {
			return
		}
		var b struct {
			Path      string `json:"path"`
			Recursive bool   `json:"recursive"`
		}
		if tosapp.DecodeJSON(w, r, &b, 8192) != nil {
			writeFailure(w, &apiError{400, "REQUEST_INVALID"})
			return
		}
		result, err := access.scan(r.Context(), b.Path, b.Recursive)
		if err != nil {
			writeFailure(w, err)
			return
		}
		tosapp.WriteJSON(w, 200, result)
	default:
		if !requireMethod(w, r, "POST") {
			return
		}
		var m mutation
		if tosapp.DecodeJSON(w, r, &m, 16384) != nil {
			writeFailure(w, &apiError{400, "REQUEST_INVALID"})
			return
		}
		result, err := access.mutate(r.Context(), strings.TrimPrefix(p, "/api/links/"), m)
		if err != nil {
			if result != nil {
				tosapp.WriteJSON(w, 409, result)
			} else {
				writeFailure(w, err)
			}
			return
		}
		tosapp.WriteJSON(w, 200, result)
	}
}
