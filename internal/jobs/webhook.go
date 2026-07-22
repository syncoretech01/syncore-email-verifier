package jobs

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SignatureHeader carries the HMAC-SHA256 signature over the raw request body.
const SignatureHeader = "X-Syncore-Signature"

// Webhook posts an HMAC-signed JSON payload to a callback URL, retrying on
// transient failures.
type Webhook struct {
	client     *http.Client
	signingKey []byte
	maxRetries int
	backoff    time.Duration
}

// NewWebhook builds a Webhook that signs bodies with signingKey.
func NewWebhook(signingKey string, client *http.Client) *Webhook {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Webhook{client: client, signingKey: []byte(signingKey), maxRetries: 3, backoff: time.Second}
}

// Send delivers payload to url, signed and retried. It returns the last error if
// every attempt fails.
func (w *Webhook) Send(ctx context.Context, url string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	sig := Sign(w.signingKey, body)

	var lastErr error
	for attempt := 0; attempt <= w.maxRetries; attempt++ {
		if attempt > 0 && w.backoff > 0 {
			select {
			case <-time.After(w.backoff * time.Duration(attempt)):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(SignatureHeader, sig)

		resp, err := w.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		lastErr = fmt.Errorf("webhook: status %d", resp.StatusCode)
	}
	return lastErr
}

// Sign returns "sha256=<hex>" — the HMAC-SHA256 of body under key. Exported so a
// receiver can verify the signature.
func Sign(key, body []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// webhookPayload is the completion summary posted to the callback URL. It omits
// the (potentially large) per-item results; the CRM fetches those via the
// results endpoint.
type webhookPayload struct {
	BatchID   string          `json:"batch_id"`
	State     State           `json:"state"`
	Total     int             `json:"total"`
	Done      int             `json:"done"`
	Counts    map[string]int  `json:"counts"`
	Meta      json.RawMessage `json:"meta,omitempty"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func (b *Batch) webhookSummary() webhookPayload {
	return webhookPayload{
		BatchID:   b.ID,
		State:     b.State,
		Total:     b.Total,
		Done:      b.Done,
		Counts:    b.Counts,
		Meta:      b.Meta,
		UpdatedAt: b.UpdatedAt,
	}
}
