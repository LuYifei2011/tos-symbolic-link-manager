package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTOSAuthorizationBeforeNativeWrites(t *testing.T) {
	for _, mode := range []string{"allowed", "read-only", "denied", "missing-proof", "malformed", "ancestor-link"} {
		t.Run(mode, func(t *testing.T) {
			_, root := fixture(t)
			put(t, filepath.Join(root, "file"))
			a := testApp(t)
			writes := 0
			a.client = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("X-Csrf-Token") != "csrf" || strings.Contains(r.Header.Get("Cookie"), "extra") {
					t.Fatal("credential forwarding")
				}
				data := any(map[string]any{})
				ok := true
				switch filepath.Base(r.URL.Path) {
				case "homeList":
					data = []map[string]string{{"path": "/Volume1/Public", "name": "Public", "f_type": "folder"}}
				case "folderInfoAll":
					p := r.URL.Query().Get("path")
					data = map[string]any{"path": p, "f_type": "folder", "is_only_read": mode == "read-only"}
					if mode == "ancestor-link" {
						data = map[string]string{"path": p, "f_type": "folder", "f_real_type": "symlink"}
					}
					if mode == "denied" {
						ok = false
					}
				case "CreateFolder":
					writes++
					if mode == "missing-proof" {
						ok = false
						break
					}
					var body struct {
						Path string `json:"path"`
						Type int    `json:"type"`
					}
					json.NewDecoder(r.Body).Decode(&body)
					if body.Type != 1 || filepath.Dir(body.Path) != root {
						t.Fatal(body)
					}
					file, e := os.OpenFile(body.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
					if e != nil {
						t.Fatal(e)
					}
					file.Close()
				default:
					t.Fatal(r.URL)
				}
				body, _ := json.Marshal(map[string]any{"is_login": true, "code": ok, "code_num": 0, "data": data})
				if mode == "malformed" {
					body = []byte(`{"code":true}`)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
			})}
			access, e := a.fileAccess(request("POST", "/api/links/create", ""))
			if mode == "malformed" {
				if e == nil {
					t.Fatal("accepted malformed session")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			// Bind the mock share to the native temporary fixture after checking root parsing.
			if len(access.roots) != 1 || access.roots[0].Path != "/Volume1/Public" {
				t.Fatal(access.roots)
			}
			access.roots = []fileEntry{{root, "Test", "directory"}}
			_, e = access.mutate(context.Background(), "create", mutation{Path: filepath.Join(root, "link"), Target: "file"})
			if mode == "allowed" {
				if e != nil || writes != 1 {
					t.Fatal(e, writes)
				}
				entries, _ := os.ReadDir(root)
				if len(entries) != 2 {
					t.Fatal("probe was not cleaned", entries)
				}
			} else {
				if e == nil {
					t.Fatal("unauthorized write")
				}
				if _, e := os.Lstat(filepath.Join(root, "link")); !os.IsNotExist(e) {
					t.Fatal("native mutation occurred")
				}
				if mode != "missing-proof" && writes != 0 {
					t.Fatal("unexpected write probe")
				}
			}
		})
	}
}
