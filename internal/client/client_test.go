package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func newTest(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/", "sio_k", "tf-test/1")
	c.RetryWait = time.Millisecond
	return c
}

func TestHeadersAndDecode(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sio_k" || r.Header.Get("User-Agent") != "tf-test/1" {
			t.Errorf("headers: %v", r.Header)
		}
		if r.Method == "POST" && r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type: %q", r.Header.Get("Content-Type"))
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "x", "echo": b["name"]})
	})
	var out struct{ ID, Echo string }
	if err := c.Post(context.Background(), "/segments", map[string]any{"name": "n"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != "x" || out.Echo != "n" {
		t.Fatalf("got %+v", out)
	}
}

func TestErrorShape(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"statusCode":404,"name":"not_found","message":"The requested resource does not exist."}`))
	})
	err := c.Get(context.Background(), "/domains/abc", nil)
	if !IsNotFound(err) || StatusCode(err) != 404 {
		t.Fatalf("err = %v", err)
	}
	if got, want := err.Error(), "GET /domains/abc: 404 not_found: The requested resource does not exist."; got != want {
		t.Fatalf("message %q, want %q", got, want)
	}
}

func TestNonJSONError(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte("bad gateway html"))
	})
	err := c.Get(context.Background(), "/x", nil)
	if StatusCode(err) != 400 || err.(*APIError).Message != "bad gateway html" {
		t.Fatalf("err = %#v", err)
	}
}

func TestRetries429ForWrites(t *testing.T) {
	var n atomic.Int32
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	})
	var out Created
	if err := c.Post(context.Background(), "/webhooks", nil, &out); err != nil || out.ID != "ok" || n.Load() != 3 {
		t.Fatalf("err=%v out=%+v calls=%d", err, out, n.Load())
	}
}

func TestNoRetry503ForWrites(t *testing.T) {
	var n atomic.Int32
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(503)
	})
	if err := c.Post(context.Background(), "/domains", nil, nil); StatusCode(err) != 503 || n.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, n.Load())
	}
	n.Store(0)
	if err := c.Get(context.Background(), "/domains", nil); StatusCode(err) != 503 || n.Load() != int32(c.MaxRetries+1) {
		t.Fatalf("GET err=%v calls=%d", err, n.Load())
	}
}

func TestListAll(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "100" {
			t.Errorf("limit %q", r.URL.Query().Get("limit"))
		}
		start := 0
		if a := r.URL.Query().Get("after"); a != "" {
			start, _ = strconv.Atoi(a)
		}
		data := []map[string]string{}
		for i := start + 1; i <= start+100 && i <= 250; i++ {
			data = append(data, map[string]string{"id": strconv.Itoa(i)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "has_more": start+100 < 250})
	})
	type item struct{ ID string }
	all, err := ListAll(context.Background(), c, "/templates", func(i item) string { return i.ID })
	if err != nil || len(all) != 250 || all[249].ID != "250" {
		t.Fatalf("err=%v n=%d", err, len(all))
	}
}

func TestPath(t *testing.T) {
	if got := Path("/suppressions", "a b@x.com"); got != "/suppressions/a%20b@x.com" {
		t.Fatal(got)
	}
}
