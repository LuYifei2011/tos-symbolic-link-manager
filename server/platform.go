package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

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
	status, code := 503, "PLATFORM_UNAVAILABLE"
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
