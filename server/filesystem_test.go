package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func fixture(t *testing.T) (*fileAccess, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &fileAccess{roots: []fileEntry{{root, "Test", "directory"}}, check: func(context.Context, string, bool) error { return nil }}, root
}
func put(t *testing.T, path string) {
	t.Helper()
	if e := os.WriteFile(path, []byte("preserved data"), 0600); e != nil {
		t.Fatal(e)
	}
}
func link(t *testing.T, target, path string) {
	t.Helper()
	if e := os.Symlink(target, path); e != nil {
		t.Fatal(e)
	}
}
func getLink(t *testing.T, path string) linkEntry {
	t.Helper()
	d, e := openDirectory(filepath.Dir(path))
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	l, e := inspectLink(int(d.Fd()), filepath.Dir(path), filepath.Base(path))
	if e != nil {
		t.Fatal(e)
	}
	return l
}
func mustMutate(t *testing.T, f *fileAccess, action string, m mutation) {
	t.Helper()
	if _, e := f.mutate(context.Background(), action, m); e != nil {
		t.Fatalf("%s: %v", action, e)
	}
}
func TestScanStatusesAndDoesNotTraverseLinks(t *testing.T) {
	f, root := fixture(t)
	put(t, filepath.Join(root, "file"))
	os.Mkdir(filepath.Join(root, "sub"), 0700)
	for name, target := range map[string]string{"valid": "file", "broken": "missing", "loop-a": "loop-b", "loop-b": "loop-a", "outside": "/etc/passwd", "dir-link": "sub"} {
		link(t, target, filepath.Join(root, name))
	}
	link(t, "../file", filepath.Join(root, "sub", "nested"))
	result, err := f.scan(context.Background(), root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Links) != 7 || result.Directories != 2 || result.Partial {
		t.Fatalf("%+v", result)
	}
	statuses := map[string]string{}
	for _, l := range result.Links {
		statuses[l.Name] = l.Status
	}
	for name, want := range map[string]string{"valid": "valid", "broken": "broken", "loop-a": "loop", "loop-b": "loop", "outside": "outside", "dir-link": "valid", "nested": "valid"} {
		if statuses[name] != want {
			t.Fatalf("%s: %s", name, statuses[name])
		}
	}
	direct, err := f.scan(context.Background(), root, false)
	if err != nil || len(direct.Links) != 6 {
		t.Fatal(err, direct)
	}
}
func TestCreateUpdateRenameDeletePreservesTargets(t *testing.T) {
	f, root := fixture(t)
	put(t, filepath.Join(root, "a"))
	put(t, filepath.Join(root, "b"))
	p := filepath.Join(root, "链接")
	mustMutate(t, f, "create", mutation{Path: p, Target: "a"})
	original := getLink(t, p)
	mustMutate(t, f, "update", mutation{Path: p, Target: "b", Fingerprint: original.Fingerprint})
	updated := getLink(t, p)
	if updated.Target != "b" {
		t.Fatal(updated)
	}
	mustMutate(t, f, "rename", mutation{Path: p, Name: "renamed", Fingerprint: updated.Fingerprint})
	p = filepath.Join(root, "renamed")
	renamed := getLink(t, p)
	mustMutate(t, f, "delete", mutation{Path: p, Fingerprint: renamed.Fingerprint})
	if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		data, e := os.ReadFile(filepath.Join(root, name))
		if e != nil || string(data) != "preserved data" {
			t.Fatal(name, e)
		}
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 2 {
		t.Fatal("temporary files left", entries)
	}
}
func TestConflictsAndStaleStateNeverOverwrite(t *testing.T) {
	f, root := fixture(t)
	put(t, filepath.Join(root, "target"))
	put(t, filepath.Join(root, "taken"))
	p := filepath.Join(root, "link")
	link(t, "target", p)
	original := getLink(t, p)
	for _, test := range []struct {
		action string
		m      mutation
		code   string
	}{
		{"create", mutation{Path: filepath.Join(root, "taken"), Target: "target"}, "PATH_EXISTS"},
		{"rename", mutation{Path: p, Name: "taken", Fingerprint: original.Fingerprint}, "PATH_EXISTS"},
		{"delete", mutation{Path: p, Fingerprint: "stale"}, "LINK_CHANGED"},
		{"delete", mutation{Path: filepath.Join(root, "taken"), Fingerprint: original.Fingerprint}, "NOT_A_LINK"},
		{"create", mutation{Path: filepath.Join(root, "bad"), Target: "missing"}, "TARGET_BROKEN"},
		{"rename", mutation{Path: p, Name: "../escape", Fingerprint: original.Fingerprint}, "NAME_INVALID"},
	} {
		_, e := f.mutate(context.Background(), test.action, test.m)
		if errorCode(e) != test.code {
			t.Fatalf("%s: %v", test.action, e)
		}
	}
	if getLink(t, p).Target != "target" {
		t.Fatal("original changed")
	}
	data, _ := os.ReadFile(filepath.Join(root, "taken"))
	if string(data) != "preserved data" {
		t.Fatal("overwritten")
	}
	mustMutate(t, f, "create", mutation{Path: filepath.Join(root, "dangling"), Target: "missing", AllowBroken: true})
}
func TestRetargetRejectsIndirectCycle(t *testing.T) {
	f, root := fixture(t)
	put(t, filepath.Join(root, "file"))
	p := filepath.Join(root, "a")
	link(t, "file", p)
	link(t, "a", filepath.Join(root, "b"))
	_, err := f.mutate(context.Background(), "update", mutation{Path: p, Target: "b", Fingerprint: getLink(t, p).Fingerprint})
	if errorCode(err) != "TARGET_LOOP" {
		t.Fatal(err)
	}
}
func TestTargetResolutionPreservesDotDotSemantics(t *testing.T) {
	f, root := fixture(t)
	os.MkdirAll(filepath.Join(root, "one", "two"), 0700)
	put(t, filepath.Join(root, "one", "file"))
	link(t, "one/two", filepath.Join(root, "alias"))
	link(t, "alias/../file", filepath.Join(root, "link"))
	result, e := f.scan(context.Background(), root, false)
	if e != nil {
		t.Fatal(e)
	}
	for _, l := range result.Links {
		if l.Name == "link" && l.Status != "valid" {
			t.Fatal(l)
		}
	}
}
func TestNoAncestorSymlinkTraversalAndOutsideTargets(t *testing.T) {
	f, root := fixture(t)
	outside := t.TempDir()
	link(t, outside, filepath.Join(root, "escape"))
	put(t, filepath.Join(outside, "file"))
	if _, e := f.scan(context.Background(), filepath.Join(root, "escape"), true); e == nil {
		t.Fatal("followed directory link")
	}
	for _, m := range []mutation{{Path: filepath.Join(root, "escape", "bad"), Target: filepath.Join(outside, "file")}, {Path: filepath.Join(root, "bad"), Target: filepath.Join(outside, "file")}, {Path: filepath.Join(root, "bad"), Target: "escape/file"}} {
		if _, e := f.mutate(context.Background(), "create", m); e == nil {
			t.Fatal("escaped", m)
		}
	}
	if _, e := os.Lstat(filepath.Join(outside, "bad")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
}
func TestPermissionDeniedDirectoriesAndTargetStatus(t *testing.T) {
	f, root := fixture(t)
	os.Mkdir(filepath.Join(root, "denied"), 0700)
	put(t, filepath.Join(root, "denied", "file"))
	link(t, "denied/file", filepath.Join(root, "link"))
	f.check = func(_ context.Context, p string, _ bool) error {
		if p == filepath.Join(root, "denied") {
			return &apiError{409, "TOS_ACCESS_DENIED"}
		}
		return nil
	}
	result, e := f.scan(context.Background(), root, true)
	if e != nil || !result.Partial || result.Skipped != 1 || result.Links[0].Status != "inaccessible" {
		t.Fatal(e, result)
	}
}
func TestConcurrentCreateExactlyOneWinner(t *testing.T) {
	f, root := fixture(t)
	put(t, filepath.Join(root, "file"))
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, e := f.mutate(context.Background(), "create", mutation{Path: filepath.Join(root, "link"), Target: "file"})
			results <- e
		})
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if errorCode(e) != "PATH_EXISTS" {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
}
func TestFileRoutesAndInputValidation(t *testing.T) {
	_, root := fixture(t)
	a := testApp(t)
	a.dev = true
	a.roots = []string{root}
	put(t, filepath.Join(root, "file"))
	for _, test := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/api/files/list", "", 200}, {"GET", "/api/files/list?path=" + root, "", 200}, {"GET", "/api/files/stat?path=" + root + "/file", "", 200},
		{"POST", "/api/scan", `{"path":"` + root + `","recursive":true}`, 200},
		{"POST", "/api/scan", `{"path":"` + root + `","unknown":true}`, 400},
		{"POST", "/api/scan", `{"path":"` + root + `"} {}`, 400},
		{"GET", "/api/scan", "", 405}, {"POST", "/api/links/create", strings.Repeat("x", 17000), 400},
		{"POST", "/api/links/create", `{"path":"` + root + `/new","target":"file"}`, 200},
		{"GET", "/api/files/stat?path=" + root + "/new", "", 409},
	} {
		r := httptest.NewRequest(test.method, "http://127.0.0.1:18947"+test.path, strings.NewReader(test.body))
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != test.want {
			t.Fatalf("%s: %d %s", test.path, w.Code, w.Body.String())
		}
		var value any
		if json.Unmarshal(w.Body.Bytes(), &value) != nil {
			t.Fatal("invalid JSON")
		}
	}
}
func TestCancelledScan(t *testing.T) {
	f, root := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := f.scan(ctx, root, true); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestScanReportsDepthLimit(t *testing.T) {
	f, root := fixture(t)
	p := root
	for range 130 {
		p = filepath.Join(p, "d")
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	result, e := f.scan(context.Background(), root, true)
	if e != nil || !result.Partial || result.Skipped != 1 || result.Warnings[0].Code != "DEPTH_LIMIT" {
		t.Fatal(e, result)
	}
}
