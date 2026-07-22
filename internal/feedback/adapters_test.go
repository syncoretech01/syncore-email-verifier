package feedback

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snsNotification wraps an inner SES event JSON string in an SNS Notification
// envelope, the way SES-over-SNS delivers it.
func snsNotification(t *testing.T, innerJSON string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"Type": "Notification", "Message": innerJSON})
	require.NoError(t, err)
	return b
}

func TestParseSES_Bounce_PermanentOnly(t *testing.T) {
	perm := `{"notificationType":"Bounce","bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"bad@example.com"},{"emailAddress":"gone@example.com"}]}}`
	res, err := ParseSES(snsNotification(t, perm))
	require.NoError(t, err)
	assert.False(t, res.SubscriptionConfirmation)
	assert.Equal(t, []Event{
		{Email: "bad@example.com", Type: EventBounced},
		{Email: "gone@example.com", Type: EventBounced},
	}, res.Events)

	// Transient bounces are not a durable negative signal → ignored.
	trans := `{"notificationType":"Bounce","bounce":{"bounceType":"Transient","bouncedRecipients":[{"emailAddress":"slow@example.com"}]}}`
	res, err = ParseSES(snsNotification(t, trans))
	require.NoError(t, err)
	assert.Empty(t, res.Events)
}

func TestParseSES_Complaint(t *testing.T) {
	c := `{"notificationType":"Complaint","complaint":{"complainedRecipients":[{"emailAddress":"angry@example.com"}]}}`
	res, err := ParseSES(snsNotification(t, c))
	require.NoError(t, err)
	assert.Equal(t, []Event{{Email: "angry@example.com", Type: EventComplained}}, res.Events)
}

func TestParseSES_Delivery(t *testing.T) {
	d := `{"notificationType":"Delivery","delivery":{"recipients":["ok@example.com"]}}`
	res, err := ParseSES(snsNotification(t, d))
	require.NoError(t, err)
	assert.Equal(t, []Event{{Email: "ok@example.com", Type: EventDelivered}}, res.Events)
}

func TestParseSES_EventTypeOpen(t *testing.T) {
	// Configuration-set event destinations use eventType (not notificationType).
	o := `{"eventType":"Open","mail":{"destination":["reader@example.com"]}}`
	res, err := ParseSES(snsNotification(t, o))
	require.NoError(t, err)
	assert.Equal(t, []Event{{Email: "reader@example.com", Type: EventEngaged}}, res.Events)
}

func TestParseSES_SubscriptionConfirmation(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"Type":         "SubscriptionConfirmation",
		"SubscribeURL": "https://sns.us-east-1.amazonaws.com/?Action=ConfirmSubscription&Token=abc",
	})
	require.NoError(t, err)
	res, err := ParseSES(body)
	require.NoError(t, err)
	assert.True(t, res.SubscriptionConfirmation)
	assert.Contains(t, res.SubscribeURL, "ConfirmSubscription")
	assert.Empty(t, res.Events)
}

func TestParseSES_RawEventWithoutEnvelope(t *testing.T) {
	// A direct forwarder may POST the SES event without the SNS envelope.
	raw := []byte(`{"notificationType":"Complaint","complaint":{"complainedRecipients":[{"emailAddress":"x@example.com"}]}}`)
	res, err := ParseSES(raw)
	require.NoError(t, err)
	assert.Equal(t, []Event{{Email: "x@example.com", Type: EventComplained}}, res.Events)
}

func TestParseSES_MalformedEnvelope(t *testing.T) {
	_, err := ParseSES([]byte(`{not json`))
	assert.Error(t, err)
}

func TestParseSmartlead(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []Event
	}{
		{"bounce", `{"event_type":"EMAIL_BOUNCE","to_email":"bad@example.com"}`, []Event{{Email: "bad@example.com", Type: EventBounced}}},
		{"reply engaged", `{"event_type":"EMAIL_REPLY","to_email":"lead@example.com"}`, []Event{{Email: "lead@example.com", Type: EventEngaged}}},
		{"open engaged", `{"event_type":"EMAIL_OPEN","to_email":"lead@example.com"}`, []Event{{Email: "lead@example.com", Type: EventEngaged}}},
		{"click engaged", `{"event_type":"EMAIL_LINK_CLICK","to_email":"lead@example.com"}`, []Event{{Email: "lead@example.com", Type: EventEngaged}}},
		{"sent delivered", `{"event_type":"EMAIL_SENT","to_email":"lead@example.com"}`, []Event{{Email: "lead@example.com", Type: EventDelivered}}},
		{"recipient in lead_email", `{"event_type":"EMAIL_BOUNCE","lead_email":"alt@example.com"}`, []Event{{Email: "alt@example.com", Type: EventBounced}}},
		{"unknown event ignored", `{"event_type":"LEAD_UNSUBSCRIBED","to_email":"u@example.com"}`, nil},
		{"no recipient ignored", `{"event_type":"EMAIL_BOUNCE"}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSmartlead([]byte(tc.body))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseSmartlead_Malformed(t *testing.T) {
	_, err := ParseSmartlead([]byte(`{bad`))
	assert.Error(t, err)
}

// TestAdapters_FeedIntoStore proves the parsed events fold into per-domain
// reputation exactly like the generic endpoint's events do.
func TestAdapters_FeedIntoStore(t *testing.T) {
	s := New()
	bounce := `{"notificationType":"Bounce","bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"a@d.com"}]}}`
	res, err := ParseSES(snsNotification(t, bounce))
	require.NoError(t, err)
	for _, e := range res.Events {
		s.Record(e)
	}
	sl, err := ParseSmartlead([]byte(`{"event_type":"EMAIL_SENT","to_email":"b@d.com"}`))
	require.NoError(t, err)
	for _, e := range sl {
		s.Record(e)
	}
	rep, ok := s.Domain("d.com")
	require.True(t, ok)
	assert.Equal(t, 1, rep.Bounced)
	assert.Equal(t, 1, rep.Delivered)
	assert.InDelta(t, 0.5, rep.BounceRate(), 0.0001)
}
