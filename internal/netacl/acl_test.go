package netacl

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDefaultAllowsPrivateRejectsPublic(t *testing.T) {
	acl, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"10.1.2.3", true},
		{"192.168.1.50", true},
		{"172.16.0.1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"::1", true},
		{"2001:4860:4860::8888", false},
	}
	for _, tc := range cases {
		if got := acl.Allows(net.ParseIP(tc.ip)); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.ip, got, tc.want)
		}
	}
}

func TestMiddlewareIgnoresForwardedHeaders(t *testing.T) {
	acl, _ := New([]string{"127.0.0.0/8"})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := acl.Middleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "8.8.8.8:12345"
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:9999"
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}
}

func TestIsPublicBind(t *testing.T) {
	if !IsPublicBind(":7947") || !IsPublicBind("0.0.0.0:7947") {
		t.Fatal("expected public bind")
	}
	if IsPublicBind("127.0.0.1:7950") {
		t.Fatal("loopback should not be public bind")
	}
}
