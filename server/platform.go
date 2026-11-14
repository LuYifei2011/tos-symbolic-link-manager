package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	tosapp "github.com/LuYifei2011/tos-app-kit/go"
)

type apiError struct {
	status int
	code   string
}

func (e *apiError) Error() string { return e.code }

// This read-only endpoint is also used by batch-rename. A different business API
// can provide its own verifier; every operation must still enforce its permissions.
func verifySession(client *http.Client) tosapp.SessionVerifier {
	return func(ctx context.Context, credentials tosapp.Credentials) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:8181/v2/fileManage/homeList", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		if err := credentials.Apply(req.Header); err != nil {
			return err
		}
		res, err := client.Do(req)
		if err != nil {
			return &apiError{503, "PLATFORM_UNAVAILABLE"}
		}
		defer res.Body.Close()
		switch res.StatusCode {
		case 401:
			return &apiError{401, "SESSION_EXPIRED"}
		case 403:
			return &apiError{403, "PLATFORM_DENIED"}
		case 200:
		default:
			return &apiError{502, "PLATFORM_RESPONSE_INVALID"}
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
		if err != nil || len(data) > 1<<20 {
			return &apiError{502, "PLATFORM_RESPONSE_INVALID"}
		}
		var payload struct {
			Login *bool `json:"is_login"`
			Code  *bool `json:"code"`
		}
		if json.Unmarshal(data, &payload) != nil || payload.Login == nil || payload.Code == nil {
			return &apiError{502, "PLATFORM_RESPONSE_INVALID"}
		}
		if !*payload.Login {
			return &apiError{401, "SESSION_EXPIRED"}
		}
		if !*payload.Code {
			return &apiError{403, "PLATFORM_DENIED"}
		}
		return nil
	}
}

func writeFailure(w http.ResponseWriter, err error) {
	status, code := 409, errorCode(err)
	var failure *apiError
	switch {
	case errors.Is(err, tosapp.ErrInvalidCredentials):
		status, code = 401, "AUTH_HEADERS_INVALID"
	case errors.Is(err, tosapp.ErrOriginMismatch):
		status, code = 403, "ORIGIN_MISMATCH"
	case errors.As(err, &failure):
		status, code = failure.status, failure.code
	}
	tosapp.WriteJSON(w, status, map[string]string{"code": code})
}

// A fresh access object is created for every request. TOS credentials and
// per-directory authorizations are never shared between users.
func (a *application) fileAccess(r *http.Request) (*fileAccess, error) {
	if a.dev {
		roots := []fileEntry{}
		for _, p := range a.roots {
			roots = append(roots, fileEntry{p, filepath.Base(p), "directory"})
		}
		return &fileAccess{roots: roots, check: func(ctx context.Context, p string, write bool) error { return ctx.Err() }}, nil
	}
	credentials, err := tosapp.Authenticate(r, a.verify)
	if err != nil {
		return nil, err
	}
	client := a.client
	if client == nil {
		client = tosapp.NewPlatformClient()
	}
	s := &fileSession{client: client, credentials: credentials}
	usernames := r.CookiesNamed("userName")
	if len(usernames) > 1 {
		return nil, tosapp.ErrInvalidCredentials
	}
	if len(usernames) == 1 {
		s.username = usernames[0].String()
	}
	var shares []struct {
		Path     string `json:"path"`
		Name     string `json:"name"`
		Type     string `json:"f_type"`
		RealType string `json:"f_real_type"`
	}
	if err := s.call(r.Context(), "GET", "homeList", nil, &shares); err != nil {
		return nil, err
	}
	access := &fileAccess{roots: []fileEntry{}}
	for _, share := range shares {
		parts := strings.Split(strings.TrimPrefix(share.Path, "/"), "/")
		if !safePath(share.Path) || len(parts) < 2 || !volumePattern.MatchString(parts[0]) || share.Type != "folder" || strings.Contains(strings.ToLower(share.RealType), "link") || len(parts) == 2 && strings.EqualFold(parts[1], "homes") {
			continue
		}
		access.roots = append(access.roots, fileEntry{share.Path, share.Name, "directory"})
	}
	checked := map[string]bool{}
	access.check = func(ctx context.Context, p string, write bool) error {
		root := ""
		for _, entry := range access.roots {
			if inside(entry.Path, p) && (root == "" || len(entry.Path) > len(root)) {
				root = entry.Path
			}
		}
		if root == "" {
			return &apiError{409, "PATH_OUTSIDE_SHARES"}
		}
		paths := []string{root}
		current := root
		for _, part := range strings.Split(strings.TrimPrefix(p, root), "/") {
			if part != "" {
				current = filepath.Join(current, part)
				paths = append(paths, current)
			}
		}
		for _, path := range paths {
			if checked[path] && !write {
				continue
			}
			var item struct {
				Path     string `json:"path"`
				Type     string `json:"f_type"`
				RealType string `json:"f_real_type"`
				ReadOnly *bool  `json:"is_only_read"`
			}
			if err := s.call(ctx, "GET", "folderInfoAll?path="+url.QueryEscape(path), nil, &item); err != nil {
				return err
			}
			if item.Path != path || item.Type != "folder" || strings.Contains(strings.ToLower(item.RealType), "link") {
				return &apiError{409, "DIRECTORY_LINK_OR_INVALID"}
			}
			if write && path == p && item.ReadOnly != nil && *item.ReadOnly {
				return &apiError{409, "TOS_WRITE_DENIED"}
			}
			checked[path] = true
		}
		if !write {
			return nil
		}
		// is_only_read describes the filesystem, not the user's ACL. Ask TOS to
		// create an unpredictable empty file to prove current-user write access.
		// Remove only that regular file through a pinned directory descriptor.
		dir, err := openDirectory(p)
		if err != nil {
			return err
		}
		defer dir.Close()
		probe := randomName() + "-permission"
		full := filepath.Join(p, probe)
		if err := s.call(ctx, "POST", "CreateFolder", map[string]any{"path": full, "type": 1, "back_up": 0}, nil); err != nil {
			return err
		}
		var st unix.Stat_t
		if err := unix.Fstatat(int(dir.Fd()), probe, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return &apiError{409, "WRITE_PROBE_CLEANUP"}
		}
		if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Size != 0 {
			return &apiError{409, "WRITE_PROBE_CLEANUP"}
		}
		if err := unix.Unlinkat(int(dir.Fd()), probe, 0); err != nil {
			return &apiError{409, "WRITE_PROBE_CLEANUP"}
		}
		return nil
	}
	return access, nil
}

var volumePattern = regexp.MustCompile(`^Volume[0-9]+$`)

type fileSession struct {
	client      *http.Client
	credentials tosapp.Credentials
	username    string
}

func (s *fileSession) call(ctx context.Context, method, endpoint string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:8181/v2/fileManage/"+endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if err := s.credentials.Apply(req.Header); err != nil {
		return err
	}
	if s.username != "" {
		req.Header.Set("Cookie", req.Header.Get("Cookie")+"; "+s.username)
	}
	res, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &apiError{503, "PLATFORM_UNAVAILABLE"}
	}
	defer res.Body.Close()
	if res.StatusCode == 401 {
		return &apiError{401, "SESSION_EXPIRED"}
	}
	if res.StatusCode == 403 {
		return &apiError{409, "TOS_ACCESS_DENIED"}
	}
	if res.StatusCode != 200 {
		return &apiError{502, "PLATFORM_RESPONSE_INVALID"}
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return &apiError{502, "PLATFORM_RESPONSE_INVALID"}
	}
	var result struct {
		Login  *bool           `json:"is_login"`
		Code   *bool           `json:"code"`
		Number int             `json:"code_num"`
		Data   json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &result) != nil || result.Login == nil || result.Code == nil {
		return &apiError{502, "PLATFORM_RESPONSE_INVALID"}
	}
	if !*result.Login {
		return &apiError{401, "SESSION_EXPIRED"}
	}
	if !*result.Code || result.Number != 0 {
		return &apiError{409, "TOS_ACCESS_DENIED"}
	}
	if out != nil && (string(result.Data) == "null" || json.Unmarshal(result.Data, out) != nil) {
		return &apiError{502, "PLATFORM_RESPONSE_INVALID"}
	}
	return nil
}
