package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"mailservice/internal/store"
)

type fakeStore struct {
	keys   map[string]*store.APIKey
	logged []store.RequestLog
}

func (f *fakeStore) FindKeyByHash(ctx context.Context, hash []byte) (*store.APIKey, error) {
	if k, ok := f.keys[string(hash)]; ok {
		return k, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) LogRequest(ctx context.Context, e store.RequestLog) error {
	f.logged = append(f.logged, e)
	return nil
}

func TestMiddleware(t *testing.T) {
	fs := &fakeStore{keys: map[string]*store.APIKey{
		string(store.HashKey("open-key")):     {ID: 1, Active: true, AllowedIPs: []string{"*"}},
		string(store.HashKey("office-key")):   {ID: 2, Active: true, AllowedIPs: []string{"10.0.0.0/8"}},
		string(store.HashKey("disabled-key")): {ID: 3, Active: false, AllowedIPs: []string{"*"}},
		string(store.HashKey("super-key")):    {ID: 4, Active: true, IsSuper: true, AllowedIPs: []string{"10.0.0.1"}},
	}}
	a := NewAPIKeyAuth(fs, false, "/health", "/admin/")
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := CallerFrom(r.Context()); !ok && r.URL.Path == "/send" {
			t.Error("caller missing from context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name   string
		path   string
		remote string
		header string
		value  string
		want   int
	}{
		{"public path", "/health", "1.2.3.4:1", "", "", http.StatusOK},
		{"public subtree", "/admin/api/keys", "1.2.3.4:1", "", "", http.StatusOK},
		{"missing key", "/send", "1.2.3.4:1", "", "", http.StatusUnauthorized},
		{"unknown key", "/send", "1.2.3.4:1", "X-API-Key", "nope", http.StatusUnauthorized},
		{"valid key", "/send", "1.2.3.4:1", "X-API-Key", "open-key", http.StatusOK},
		{"bearer", "/send", "1.2.3.4:1", "Authorization", "Bearer open-key", http.StatusOK},
		{"basic rejected", "/send", "1.2.3.4:1", "Authorization", "Basic open-key", http.StatusUnauthorized},
		{"ip in cidr", "/send", "10.1.2.3:1", "X-API-Key", "office-key", http.StatusOK},
		{"ip outside cidr", "/send", "11.1.2.3:1", "X-API-Key", "office-key", http.StatusForbidden},
		{"disabled key", "/send", "1.2.3.4:1", "X-API-Key", "disabled-key", http.StatusForbidden},
		{"super ignores ip", "/send", "8.8.8.8:1", "X-API-Key", "super-key", http.StatusOK},
		{"unknown path needs key", "/other", "1.2.3.4:1", "", "", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, tc.path, nil)
		req.RemoteAddr = tc.remote
		if tc.header != "" {
			req.Header.Set(tc.header, tc.value)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: got %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body.String())
		}
	}

	if len(fs.logged) != 2 {
		t.Errorf("expected 2 rejected requests logged, got %d", len(fs.logged))
	}
}

func TestClientIPIgnoresForwardedHeaderUnlessTrusted(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:5000"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")

	if got := ClientIP(req, false); got != "192.0.2.1" {
		t.Errorf("untrusted: got %s", got)
	}
	if got := ClientIP(req, true); got != "203.0.113.9" {
		t.Errorf("trusted: got %s", got)
	}
}
