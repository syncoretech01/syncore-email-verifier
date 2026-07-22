package jobs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AfterShip/email-verifier/internal/classify"
	"github.com/AfterShip/email-verifier/internal/verification"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhook_SignsAndDelivers(t *testing.T) {
	var gotSig string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get(SignatureHeader)
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh := NewWebhook("secret-key", srv.Client())
	require.NoError(t, wh.Send(context.Background(), srv.URL, map[string]any{"batch_id": "abc", "state": "done"}))

	assert.Equal(t, Sign([]byte("secret-key"), gotBody), gotSig, "signature must be HMAC-SHA256 of the body")
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(gotBody, &decoded))
	assert.Equal(t, "abc", decoded["batch_id"])
}

func TestWebhook_RetriesThenSucceeds(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&attempts, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh := NewWebhook("k", srv.Client())
	wh.backoff = time.Millisecond // fast retries for the test
	require.NoError(t, wh.Send(context.Background(), srv.URL, map[string]string{"x": "y"}))
	assert.Equal(t, int32(3), atomic.LoadInt32(&attempts))
}

func TestWebhook_GivesUpAfterMaxRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	wh := NewWebhook("k", srv.Client())
	wh.backoff = time.Millisecond
	assert.Error(t, wh.Send(context.Background(), srv.URL, map[string]string{"x": "y"}))
}

func TestManager_FiresSignedWebhookOnCompletion(t *testing.T) {
	received := make(chan []byte, 1)
	var sig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sig = r.Header.Get(SignatureHeader)
		w.WriteHeader(http.StatusOK)
		received <- body
	}))
	defer srv.Close()

	stub := newStub(func(email string, _ int) verification.Assessment {
		return verification.Assessment{Email: email, Status: classify.StatusValid, ReasonCode: classify.ReasonSMTPAccepted}
	})
	m := NewManager(stub, Config{Workers: 1, Webhook: NewWebhook("hook-key", srv.Client())})
	m.Start()
	defer m.Stop()

	_, err := m.Submit([]string{"a@x.com", "b@x.com"}, srv.URL, json.RawMessage(`{"list":"1"}`))
	require.NoError(t, err)

	select {
	case body := <-received:
		assert.Equal(t, Sign([]byte("hook-key"), body), sig)
		var p map[string]any
		require.NoError(t, json.Unmarshal(body, &p))
		assert.Equal(t, "done", p["state"])
		assert.Equal(t, float64(2), p["total"])
	case <-time.After(3 * time.Second):
		t.Fatal("webhook was not delivered")
	}
}
