package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tosapp "github.com/LuYifei2011/tos-app-kit/go"
	appconfig "tos-app-template"
)

func testApp(t *testing.T) *application {
	t.Helper()
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("<h1>template</h1>"), 0600); err != nil {
		t.Fatal(err)
	}
	return &application{config: appconfig.Config{ID: "other-app", Version: "1.2.3"}, web: web, verify: func(context.Context, tosapp.Credentials) error { return nil }}
}

func request(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, "http://nas:8181"+path, strings.NewReader(body))
	r.Header.Set("Origin", "http://nas:8181")
	r.Header.Set("Cookie", "TMSESSNAME=session; X-Csrf-Token=csrf; extra=not-forwarded")
	r.Header.Set("X-Csrf-Token", "csrf")
	return r
}

func TestMountedRoutesAndStaticBoundaries(t *testing.T) {
	a := testApp(t)
	for _, path := range []string{"/", "/other-app/", "/v2/proxy/other-app/", "/api/info", "/other-app/api/info", "/v2/proxy/other-app/api/info"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, request("GET", path, ""))
		if w.Code != 200 || w.Header().Get("X-TOS-App-Version") != "1.2.3" {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if err := os.Mkdir(filepath.Join(a.web, "private"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/private/", "/missing", "/other-app-evil/api/info"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, request("GET", path, ""))
		if w.Code != 404 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}

func TestAuthenticationRejectsBeforeBusinessHandler(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*http.Request, *application)
		status int
	}{
		{"missing credentials", func(r *http.Request, a *application) { r.Header.Del("Cookie") }, 401},
		{"CSRF mismatch", func(r *http.Request, a *application) { r.Header.Set("X-Csrf-Token", "wrong") }, 401},
		{"cross origin", func(r *http.Request, a *application) { r.Header.Set("Origin", "http://evil.test") }, 403},
		{"expired", func(r *http.Request, a *application) {
			a.verify = func(context.Context, tosapp.Credentials) error { return &apiError{401, "SESSION_EXPIRED"} }
		}, 401},
		{"unavailable", func(r *http.Request, a *application) {
			a.verify = func(context.Context, tosapp.Credentials) error { return &apiError{503, "PLATFORM_UNAVAILABLE"} }
		}, 503},
		{"no verifier", func(r *http.Request, a *application) { a.verify = nil }, 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := testApp(t)
			r := request("POST", "/api/echo", `{"message":"secret"}`)
			test.mutate(r, a)
			w := httptest.NewRecorder()
			a.ServeHTTP(w, r)
			if w.Code != test.status || strings.Contains(w.Body.String(), "secret") {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestEchoValidationAndMethods(t *testing.T) {
	a := testApp(t)
	for _, test := range []struct {
		method, body string
		status       int
	}{
		{"POST", `{"message":"你好 <script>"}`, 200},
		{"POST", `{"message":" "}`, 400},
		{"POST", `{"message":"ok","unknown":true}`, 400},
		{"POST", `{"message":"ok"} {}`, 400},
		{"POST", `{"message":"` + strings.Repeat("a", 201) + `"}`, 400},
		{"POST", strings.Repeat("x", 5000), 400},
		{"GET", "", 405},
	} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, request(test.method, "/api/echo", test.body))
		if w.Code != test.status {
			t.Fatalf("%s %s: %d %s", test.method, test.body[:min(30, len(test.body))], w.Code, w.Body.String())
		}
	}
}

func TestPreflightWithoutSession(t *testing.T) {
	a := testApp(t)
	r := request("OPTIONS", "/api/echo", "")
	r.Header.Del("Cookie")
	r.Header.Del("X-Csrf-Token")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "http://nas:8181" {
		t.Fatal(w.Code, w.Header())
	}
}

func TestDevelopmentRejectsDNSRebindingAndRemoteOrigins(t *testing.T) {
	a := testApp(t)
	a.dev = true
	for _, test := range []struct {
		host, origin string
		status       int
	}{
		{"127.0.0.1:18947", "http://127.0.0.1:5173", 200},
		{"localhost:18947", "", 200},
		{"[::1]:18947", "http://[::1]:5173", 200},
		{"evil.test:18947", "", 403},
		{"127.0.0.1:18947", "http://evil.test", 403},
		{"127.0.0.1:18947", "http://127.0.0.1:5173/?bad", 403},
	} {
		r := httptest.NewRequest("GET", "http://"+test.host+"/api/info", nil)
		if test.origin != "" {
			r.Header.Set("Origin", test.origin)
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("%s %s: %d", test.host, test.origin, w.Code)
		}
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPlatformVerifierContract(t *testing.T) {
	for _, test := range []struct {
		body string
		code int
		want int
	}{
		{`{"is_login":true,"code":true,"data":[]}`, 200, 200},
		{`{"is_login":false,"code":true}`, 200, 401},
		{`{"is_login":true,"code":false}`, 200, 403},
		{`{"code":true}`, 200, 502},
		{`{"is_login":true,"code":"true"}`, 200, 502},
		{`<html>login</html>`, 200, 502},
		{strings.Repeat("x", (1<<20)+1), 200, 502},
		{`sensitive upstream text`, 401, 401},
		{`sensitive upstream text`, 403, 403},
		{`sensitive upstream text`, 302, 502},
	} {
		a := testApp(t)
		a.verify = verifySession(&http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "http://127.0.0.1:8181/v2/fileManage/homeList" || r.Header.Get("Cookie") != "TMSESSNAME=session; X-Csrf-Token=csrf" || r.Header.Get("X-Csrf-Token") != "csrf" {
				t.Fatal("upstream destination or credential filtering changed")
			}
			return &http.Response{StatusCode: test.code, Body: io.NopCloser(strings.NewReader(test.body)), Header: make(http.Header)}, nil
		})})
		w := httptest.NewRecorder()
		a.ServeHTTP(w, request("GET", "/api/info", ""))
		if w.Code != test.want || strings.Contains(w.Body.String(), "sensitive") {
			t.Fatalf("upstream %d: got %d, want %d", test.code, w.Code, test.want)
		}
	}
}
