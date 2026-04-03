package couchdb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNew_ParsesURL(t *testing.T) {
	rawURL := "http://admin:password@localhost:5984/my-vault"
	c, err := New(rawURL)
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}
	if c.dbName != "my-vault" {
		t.Errorf("expected dbName %q, got %q", "my-vault", c.dbName)
	}
	if c.username != "admin" {
		t.Errorf("expected username %q, got %q", "admin", c.username)
	}
	if c.password != "password" {
		t.Errorf("expected password %q, got %q", "password", c.password)
	}
	if c.baseURL.Host != "localhost:5984" {
		t.Errorf("expected host %q, got %q", "localhost:5984", c.baseURL.Host)
	}
}

func TestNew_MissingDBName(t *testing.T) {
	_, err := New("http://admin:password@localhost:5984/")
	if err == nil {
		t.Fatal("expected error for missing db name, got nil")
	}
}

func TestNew_InvalidURL(t *testing.T) {
	_, err := New("://invalid")
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
}

func TestHTTPClient_Changes_ReceivesEvents(t *testing.T) {
	// Serve a short _changes stream: two events then close.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/testdb/_changes" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter does not support Flusher")
			return
		}
		lines := []string{
			`{"seq":"1","id":"doc1","changes":[{"rev":"1-abc"}]}`,
			`{"seq":"2","id":"doc2","changes":[{"rev":"1-def"}],"deleted":true}`,
			`{"last_seq":"2"}`,
		}
		for _, line := range lines {
			fmt.Fprintln(w, line)
			flusher.Flush()
		}
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	c, err := New(srv.URL + "/testdb")
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := c.Changes(ctx, "0")
	if err != nil {
		t.Fatalf("Changes(): %v", err)
	}

	var events []ChangeEvent
	for e := range ch {
		events = append(events, e)
		if len(events) == 2 {
			// Cancel after receiving 2 events so the goroutine doesn't retry forever.
			cancel()
		}
	}

	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(events))
	}
	if events[0].ID != "doc1" {
		t.Errorf("events[0].ID: expected %q, got %q", "doc1", events[0].ID)
	}
	if events[1].ID != "doc2" {
		t.Errorf("events[1].ID: expected %q, got %q", "doc2", events[1].ID)
	}
	if !events[1].Deleted {
		t.Error("events[1].Deleted: expected true")
	}
}

func TestHTTPClient_Changes_SkipsHeartbeats(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		// Send two empty heartbeat lines then a real event.
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, `{"seq":"5","id":"heartbeat-test","changes":[{"rev":"1-abc"}]}`)
		flusher.Flush()
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	c, err := New(srv.URL + "/testdb")
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := c.Changes(ctx, "0")
	if err != nil {
		t.Fatalf("Changes(): %v", err)
	}

	var got ChangeEvent
	select {
	case e, ok := <-ch:
		if ok {
			got = e
			cancel()
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for event")
	}

	// Drain remaining.
	for range ch {
	}

	if got.ID != "heartbeat-test" {
		t.Errorf("expected ID %q, got %q", "heartbeat-test", got.ID)
	}
}

func TestHTTPClient_AllMetaDocs_NetworkError(t *testing.T) {
	// Point at a non-existent server; expect a network error (not a panic).
	c, err := New("http://admin:password@localhost:19999/vault")
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}
	ctx := context.Background()
	_, err = c.AllMetaDocs(ctx)
	if err == nil {
		t.Error("expected an error when server is unreachable, got nil")
	}
}
