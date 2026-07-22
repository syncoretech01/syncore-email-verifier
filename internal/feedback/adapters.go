package feedback

import (
	"encoding/json"
	"fmt"
	"strings"
)

// This file normalizes provider-specific webhook payloads (Amazon SES via SNS,
// and Smartlead) into feedback Events. The functions are pure and
// deterministic — no network, no clock — so they are fully unit-testable. The
// HTTP layer handles auth and calls Store.Record on the results.

// SNSEnvelope is the outer Amazon SNS message wrapping an SES event delivered
// over an HTTPS subscription.
type SNSEnvelope struct {
	Type         string `json:"Type"`
	Message      string `json:"Message"`
	SubscribeURL string `json:"SubscribeURL"`
	TopicArn     string `json:"TopicArn"`
}

// SESResult is the outcome of parsing an SES-over-SNS request body.
type SESResult struct {
	// Events are the normalized outcomes to record (empty for confirmations).
	Events []Event
	// SubscriptionConfirmation is true when the body is an SNS
	// SubscriptionConfirmation; SubscribeURL then holds the one-time URL an
	// operator (or an auto-confirm step) must GET to activate the subscription.
	SubscriptionConfirmation bool
	SubscribeURL             string
}

// sesMessage is the SES event carried in the SNS "Message" string. SES publishes
// two shapes: the classic SNS notification (notificationType) and configuration-
// set event destinations (eventType). We accept either.
type sesMessage struct {
	NotificationType string `json:"notificationType"`
	EventType        string `json:"eventType"`
	Bounce           struct {
		BounceType        string `json:"bounceType"`
		BouncedRecipients []struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"bouncedRecipients"`
	} `json:"bounce"`
	Complaint struct {
		ComplainedRecipients []struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"complainedRecipients"`
	} `json:"complaint"`
	Delivery struct {
		Recipients []string `json:"recipients"`
	} `json:"delivery"`
	Mail struct {
		Destination []string `json:"destination"`
	} `json:"mail"`
}

// ParseSES normalizes an Amazon SES event delivered over SNS into feedback
// events. It transparently handles the SNS envelope and SNS
// SubscriptionConfirmation messages.
//
// Mapping: permanent Bounce → bounced, Complaint → complained, Delivery →
// delivered, Open/Click → engaged. Transient bounces and Send/Reject are
// ignored (a transient failure is not a permanent signal).
func ParseSES(body []byte) (SESResult, error) {
	var env SNSEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return SESResult{}, fmt.Errorf("parse SNS envelope: %w", err)
	}

	switch env.Type {
	case "SubscriptionConfirmation":
		return SESResult{SubscriptionConfirmation: true, SubscribeURL: env.SubscribeURL}, nil
	case "Notification":
		// The SES event is a JSON string inside Message.
		return SESResult{Events: sesEventsFromMessage([]byte(env.Message))}, nil
	case "":
		// Not an SNS envelope — treat the body itself as a raw SES event
		// (e.g. delivered via a direct HTTP forwarder rather than SNS).
		return SESResult{Events: sesEventsFromMessage(body)}, nil
	default:
		// Unknown SNS message type (e.g. UnsubscribeConfirmation): nothing to record.
		return SESResult{}, nil
	}
}

func sesEventsFromMessage(msg []byte) []Event {
	var m sesMessage
	if err := json.Unmarshal(msg, &m); err != nil {
		return nil
	}
	kind := m.NotificationType
	if kind == "" {
		kind = m.EventType
	}

	switch strings.ToLower(kind) {
	case "bounce":
		// Only permanent (hard) bounces are a durable negative signal.
		if !strings.EqualFold(m.Bounce.BounceType, "Permanent") {
			return nil
		}
		return eventsFor(recipientsOf(m.Bounce.BouncedRecipients), EventBounced)
	case "complaint":
		return eventsFor(recipientsOf(m.Complaint.ComplainedRecipients), EventComplained)
	case "delivery":
		return eventsFor(m.Delivery.Recipients, EventDelivered)
	case "send":
		// SES accepted the message for the destination.
		return eventsFor(m.Mail.Destination, EventDelivered)
	case "open", "click":
		return eventsFor(m.Mail.Destination, EventEngaged)
	default:
		return nil
	}
}

// recipientsOf extracts the addresses from SES bounced/complained recipient
// lists (both share this identical anonymous struct element type).
func recipientsOf(rs []struct {
	EmailAddress string `json:"emailAddress"`
}) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.EmailAddress)
	}
	return out
}

func eventsFor(emails []string, t EventType) []Event {
	out := make([]Event, 0, len(emails))
	for _, e := range emails {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, Event{Email: e, Type: t})
		}
	}
	return out
}

// smartleadPayload is a Smartlead webhook event. Field names follow Smartlead's
// documented webhook format; the recipient is read from the first non-empty of
// several candidate fields for robustness.
type smartleadPayload struct {
	EventType string `json:"event_type"`
	ToEmail   string `json:"to_email"`
	LeadEmail string `json:"lead_email"`
	Email     string `json:"email"`
	To        string `json:"to"`
}

// ParseSmartlead normalizes a Smartlead webhook event into feedback events.
//
// Mapping: EMAIL_BOUNCE → bounced, EMAIL_REPLY/OPEN/(LINK_)CLICK → engaged,
// EMAIL_SENT → delivered. Unsubscribes and unknown events are ignored.
func ParseSmartlead(body []byte) ([]Event, error) {
	var p smartleadPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parse smartlead payload: %w", err)
	}
	email := firstNonEmpty(p.ToEmail, p.LeadEmail, p.Email, p.To)
	if email == "" {
		return nil, nil
	}

	var t EventType
	switch strings.ToUpper(strings.TrimSpace(p.EventType)) {
	case "EMAIL_BOUNCE", "EMAIL_BOUNCED", "BOUNCE":
		t = EventBounced
	case "EMAIL_REPLY", "EMAIL_OPEN", "EMAIL_CLICK", "EMAIL_LINK_CLICK", "REPLY", "OPEN", "CLICK":
		t = EventEngaged
	case "EMAIL_SENT", "SENT", "EMAIL_DELIVERED", "DELIVERED":
		t = EventDelivered
	default:
		return nil, nil
	}
	return []Event{{Email: email, Type: t}}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
