package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AfterShip/email-verifier/internal/feedback"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const adapterToken = "adapter-secret"

func newAdapterServer(t *testing.T, token string) (http.Handler, *feedback.Store) {
	t.Helper()
	fs := feedback.New()
	h := newRouter(newHandlers(handlerOpts{
		svc: okService(), batch: defaultTestBatch, logger: testLogger,
		feedbackStore: fs, feedbackAdapterToken: []byte(token),
	}), "")
	return h, fs
}

// postAdapter posts a raw body to an adapter endpoint. headerToken and queryToken
// are optional; contentType may be empty (SNS sends text/plain).
func postAdapter(t *testing.T, h http.Handler, path, body, headerToken, queryToken, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	target := path
	if queryToken != "" {
		target += "?token=" + queryToken
	}
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if headerToken != "" {
		r.Header.Set("X-Syncore-Token", headerToken)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func recorded(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	var body struct {
		Recorded int `json:"recorded"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.Recorded
}

func snsBody(innerJSON string) string {
	b, _ := json.Marshal(map[string]any{"Type": "Notification", "Message": innerJSON})
	return string(b)
}

func TestAdapterSES_BounceRecorded_TokenHeader(t *testing.T) {
	h, fs := newAdapterServer(t, adapterToken)
	inner := `{"notificationType":"Bounce","bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"bad@bounced.com"}]}}`
	// SNS delivers with text/plain — the endpoint must accept it.
	rec := postAdapter(t, h, "/v1/feedback/ses", snsBody(inner), adapterToken, "", "text/plain; charset=UTF-8")

	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, 1, recorded(t, rec))
	rep, ok := fs.Domain("bounced.com")
	require.True(t, ok)
	assert.Equal(t, 1, rep.Bounced)
}

func TestAdapterSES_TokenViaQueryParam(t *testing.T) {
	// SNS HTTPS subscriptions can't set headers, so the token goes in the URL.
	h, fs := newAdapterServer(t, adapterToken)
	inner := `{"notificationType":"Complaint","complaint":{"complainedRecipients":[{"emailAddress":"c@dom.com"}]}}`
	rec := postAdapter(t, h, "/v1/feedback/ses", snsBody(inner), "", adapterToken, "")
	require.Equal(t, http.StatusAccepted, rec.Code)
	rep, ok := fs.Domain("dom.com")
	require.True(t, ok)
	assert.Equal(t, 1, rep.Complained)
}

func TestAdapterSES_WrongAndMissingToken(t *testing.T) {
	h, fs := newAdapterServer(t, adapterToken)
	inner := `{"notificationType":"Bounce","bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"x@dom.com"}]}}`

	rec := postAdapter(t, h, "/v1/feedback/ses", snsBody(inner), "wrong", "", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assertErrorEnvelope(t, rec, "unauthorized")

	rec = postAdapter(t, h, "/v1/feedback/ses", snsBody(inner), "", "", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	_, ok := fs.Domain("dom.com")
	assert.False(t, ok, "unauthorized events must not be recorded")
}

func TestAdapterSES_SubscriptionConfirmation(t *testing.T) {
	h, fs := newAdapterServer(t, adapterToken)
	body, _ := json.Marshal(map[string]any{
		"Type":         "SubscriptionConfirmation",
		"SubscribeURL": "https://sns.us-east-1.amazonaws.com/?Action=ConfirmSubscription",
	})
	rec := postAdapter(t, h, "/v1/feedback/ses", string(body), adapterToken, "", "")
	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["subscription_confirmation"])
	// A confirmation carries no address, so nothing is recorded.
	_, ok := fs.Domain("amazonaws.com")
	assert.False(t, ok)
}

func TestAdapterSES_MalformedBody(t *testing.T) {
	h, _ := newAdapterServer(t, adapterToken)
	rec := postAdapter(t, h, "/v1/feedback/ses", `{not json`, adapterToken, "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assertErrorEnvelope(t, rec, "invalid_request")
}

func TestAdapterSmartlead_Events(t *testing.T) {
	h, fs := newAdapterServer(t, adapterToken)

	rec := postAdapter(t, h, "/v1/feedback/smartlead",
		`{"event_type":"EMAIL_BOUNCE","to_email":"bad@sl.com"}`, adapterToken, "", "application/json")
	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, 1, recorded(t, rec))

	rec = postAdapter(t, h, "/v1/feedback/smartlead",
		`{"event_type":"EMAIL_REPLY","to_email":"keen@sl.com"}`, adapterToken, "", "application/json")
	require.Equal(t, http.StatusAccepted, rec.Code)

	bad, _ := fs.Domain("sl.com")
	assert.Equal(t, 1, bad.Bounced)
	assert.Equal(t, 1, bad.Engaged)
}

func TestAdapterSmartlead_UnknownEventRecordsNothing(t *testing.T) {
	h, fs := newAdapterServer(t, adapterToken)
	rec := postAdapter(t, h, "/v1/feedback/smartlead",
		`{"event_type":"LEAD_UNSUBSCRIBED","to_email":"u@sl.com"}`, adapterToken, "", "")
	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, 0, recorded(t, rec))
	_, ok := fs.Domain("sl.com")
	assert.False(t, ok)
}

func TestAdapters_DisabledWhenNoToken(t *testing.T) {
	h := newTestServer(t, okService(), 0) // no feedback store / adapter token
	for _, path := range []string{"/v1/feedback/ses", "/v1/feedback/smartlead"} {
		rec := postAdapter(t, h, path, `{}`, "anything", "", "")
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assertErrorEnvelope(t, rec, "not_available")
	}
}
