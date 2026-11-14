package main

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	tosapp "github.com/LuYifei2011/tos-app-kit/go"
	appconfig "tos-app-template"
)

type application struct {
	config appconfig.Config
	web    string
	dev    bool
	verify tosapp.SessionVerifier
}

func localRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil || (host != "127.0.0.1" && host != "[::1]" && host != "::1" && host != "localhost") {
		return false
	}
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return true
	}
	if len(origins) != 1 {
		return false
	}
	u, err := url.Parse(origins[0])
	return err == nil && u.Scheme == "http" && u.User == nil && u.Path == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" &&
		(u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")
}

func (a *application) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-TOS-App-Version", a.config.Version)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	if a.dev && !localRequest(r) {
		writeFailure(w, &apiError{403, "ORIGIN_MISMATCH"})
		return
	}
	p := tosapp.StripAppPrefix(r.URL.Path, a.config.ID)
	if p != "/api" && !strings.HasPrefix(p, "/api/") {
		a.serveStatic(w, r, p)
		return
	}
	if r.Method == http.MethodOptions {
		if err := tosapp.WritePreflight(w, r, []string{"GET", "POST", "OPTIONS"}); err != nil {
			writeFailure(w, err)
		}
		return
	}
	if !a.dev {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		if _, err := tosapp.Authenticate(r, a.verify); err != nil {
			writeFailure(w, err)
			return
		}
	}
	switch p {
	case "/api/info":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		mode := "tos"
		if a.dev {
			mode = "development"
		}
		tosapp.WriteJSON(w, 200, map[string]string{"id": a.config.ID, "version": a.config.Version, "mode": mode})
	case "/api/echo":
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var body struct {
			Message string `json:"message"`
		}
		if tosapp.DecodeJSON(w, r, &body, 4096) != nil || !utf8.ValidString(body.Message) || utf8.RuneCountInString(body.Message) > 200 || strings.TrimSpace(body.Message) == "" {
			writeFailure(w, &apiError{400, "MESSAGE_INVALID"})
			return
		}
		tosapp.WriteJSON(w, 200, map[string]string{"message": body.Message})
	default:
		writeFailure(w, &apiError{404, "ENDPOINT_NOT_FOUND"})
	}
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeFailure(w, &apiError{405, "METHOD_NOT_ALLOWED"})
	return false
}

func (a *application) serveStatic(w http.ResponseWriter, r *http.Request, p string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeFailure(w, &apiError{405, "METHOD_NOT_ALLOWED"})
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'self'; object-src 'none'; base-uri 'self'")
	file := strings.TrimPrefix(path.Clean("/"+p), "/")
	if file == "" {
		file = "index.html"
	}
	fs := http.Dir(a.web)
	f, err := fs.Open(file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	if file == "index.html" {
		w.Header().Set("Cache-Control", "no-store")
	}
	http.ServeContent(w, r, file, info.ModTime(), f)
}
