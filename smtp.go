package emailverifier

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// SMTP stores all information for SMTP verification lookup
type SMTP struct {
	HostExists  bool `json:"host_exists"` // is the host exists?
	FullInbox   bool `json:"full_inbox"`  // is the email account's inbox full?
	CatchAll    bool `json:"catch_all"`   // does the domain have a catch-all email address? (true only when confirmed)
	Deliverable bool `json:"deliverable"` // can send an email to the email server? (true only when accepted)
	Disabled    bool `json:"disabled"`    // is the email blocked or disabled by the provider?

	// Additive Syncore recipient evidence.
	RecipientResult string `json:"recipient_result"` // accepted|rejected|temporary|blocked|unknown|not_checked
	RecipientReason string `json:"recipient_reason"` // normalized recipient reason (see reason* constants)
	SMTPCode        int    `json:"smtp_code"`        // sanitized numeric RCPT reply code (0 when none)
	CatchAllResult  string `json:"catch_all_result"` // confirmed|not_catch_all|unknown|not_checked
	Source          string `json:"source"`           // verification source: smtp|api
}

// CheckSMTP performs an email verification on the passed domain via SMTP
//   - the domain is the passed email domain
//   - username is used to check the deliverability of specific email address,
//
// if server is catch-all server, username will not be checked
func (v *Verifier) CheckSMTP(domain, username string) (*SMTP, error) {
	if !v.smtpCheckEnabled {
		return nil, nil
	}

	ret := SMTP{
		RecipientResult: recipientNotChecked,
		CatchAllResult:  catchAllNotChecked,
		Source:          sourceSMTP,
	}
	var err error
	email := fmt.Sprintf("%s@%s", username, domain)

	// Dial any SMTP server that will accept a connection
	client, mx, err := v.newSMTPClient(domain)
	if err != nil {
		return &ret, ParseSMTPError(err)
	}

	// Defer quit the SMTP connection
	defer client.Close()

	// Check by api when enabled and host recognized.
	for _, apiVerifier := range v.apiVerifiers {
		if apiVerifier.isSupported(strings.ToLower(mx.Host)) {
			return apiVerifier.check(domain, username)
		}
	}

	// Sets the HELO/EHLO hostname
	if err = client.Hello(v.helloName); err != nil {
		return &ret, ParseSMTPError(err)
	}

	// Sets the from email
	if err = client.Mail(v.fromEmail); err != nil {
		return &ret, ParseSMTPError(err)
	}

	// Host exists if we've successfully formed a connection
	ret.HostExists = true

	if v.catchAllCheckEnabled {
		// Probe a randomly generated address to determine tri-state catch-all
		// evidence. Do NOT default CatchAll to true: only a confirmed accept
		// makes it a catch-all; a timeout / temporary / policy reply is unknown.
		randomEmail := GenerateRandomEmail(domain)
		result, reason, _, transportErr := classifyRecipientReply(client.Rcpt(randomEmail))
		switch {
		case result == recipientAccepted:
			ret.CatchAllResult = catchAllConfirmed
			ret.CatchAll = true
		case result == recipientRejected && reason == reasonMailboxNotFound:
			// A clean nonexistent-recipient rejection proves the server is not a
			// catch-all; continue to probe the real recipient.
			ret.CatchAllResult = catchAllNot
		default:
			// Temporary rejection, policy block, timeout or ambiguous reply: we
			// cannot claim the domain is catch-all.
			ret.CatchAllResult = catchAllUnknown
		}

		// A confirmed catch-all server accepts everything; no need to calibrate
		// deliverability on a specific user.
		if ret.CatchAllResult == catchAllConfirmed {
			return &ret, nil
		}
		// A transport failure (timeout / refused) during the probe compromises the
		// connection; surface it as an identifiable error rather than continuing.
		if transportErr != nil {
			return &ret, transportErr
		}
	}

	// If no username provided,
	// no need to calibrate deliverable on a specific user
	if username == "" {
		return &ret, nil
	}

	// Capture the real-recipient RCPT result as normalized evidence.
	result, reason, code, transportErr := classifyRecipientReply(client.Rcpt(email))
	ret.RecipientResult = result
	ret.RecipientReason = reason
	ret.SMTPCode = code
	switch result {
	case recipientAccepted:
		ret.Deliverable = true
	case recipientRejected, recipientTemporary:
		if reason == reasonFullInbox {
			ret.FullInbox = true
		}
		if reason == reasonMailboxDisabled {
			ret.Disabled = true
		}
	}
	if transportErr != nil {
		// Preserve the partial evidence gathered above alongside the identifiable
		// transport error (e.g. a real-recipient timeout).
		return &ret, transportErr
	}

	return &ret, nil
}

// mxDialResult carries the outcome of a single MX dial attempt.
type mxDialResult struct {
	client *smtp.Client
	mx     *net.MX
	err    error
}

// newSMTPClient dials the domain's mail exchangers concurrently and returns the
// first client that connects.
//
// Concurrency contract: the result channel is buffered to the number of
// candidates, so every dial goroutine can deliver its single result without ever
// blocking — even after a winner has been chosen. There is no shared mutable
// completion flag (hence no data race and no mutex). Once a winner is selected,
// a background drainer consumes the remaining results and closes any additional
// successful clients, so no goroutine and no connection leaks.
func (v *Verifier) newSMTPClient(domain string) (*smtp.Client, *net.MX, error) {
	domain = domainToASCII(domain)
	mxRecords, _, nullMX, err := v.resolveMailHosts(domain)
	if err != nil {
		return nil, nil, err
	}
	if nullMX {
		return nil, nil, errors.New("Null MX: domain does not accept mail")
	}

	if len(mxRecords) == 0 {
		return nil, nil, errors.New("No MX records found")
	}

	// Buffered to len(mxRecords): guarantees every goroutine's send succeeds
	// without a reader, so sends never block and goroutines never leak.
	results := make(chan mxDialResult, len(mxRecords))

	// Attempt to connect to all SMTP servers concurrently.
	for _, r := range mxRecords {
		r := r
		addr := r.Host + smtpPort
		go func() {
			c, err := v.dial(addr, v.proxyURI, v.connectTimeout, v.operationTimeout)
			results <- mxDialResult{client: c, mx: r, err: err}
		}()
	}

	var firstErr error
	for i := 0; i < len(mxRecords); i++ {
		res := <-results
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
			}
			continue
		}

		// First successful connection wins. Drain the remaining results in the
		// background, closing any later successful clients so neither goroutines
		// nor connections leak. The buffered channel guarantees the drain (and
		// every producer) completes without blocking.
		if remaining := len(mxRecords) - i - 1; remaining > 0 {
			go drainMXResults(results, remaining)
		}
		return res.client, res.mx, nil
	}

	// Every candidate failed. All goroutines have delivered their result to the
	// buffered channel, so nothing is left blocked.
	return nil, nil, firstErr
}

// drainMXResults consumes n further dial results, closing any successful clients
// that were not selected as the winner.
func drainMXResults(results <-chan mxDialResult, n int) {
	for i := 0; i < n; i++ {
		res := <-results
		if res.err == nil && res.client != nil {
			_ = res.client.Close()
		}
	}
}

// dialSMTP is a timeout wrapper for smtp.Dial. It attempts to dial an
// SMTP server (socks5 proxy supported) and fails with a timeout if timeout is reached while
// attempting to establish a new connection
func dialSMTP(addr, proxyURI string, connectTimeout, operationTimeout time.Duration) (*smtp.Client, error) {
	// Dial the new smtp connection
	var conn net.Conn
	var err error

	if proxyURI != "" {
		conn, err = establishProxyConnection(addr, proxyURI, connectTimeout)
	} else {
		conn, err = establishConnection(addr, connectTimeout)
	}
	if err != nil {
		return nil, err
	}

	// Set specific timeouts for writing and reading
	err = conn.SetDeadline(time.Now().Add(operationTimeout))
	if err != nil {
		return nil, err
	}

	host, _, _ := net.SplitHostPort(addr)
	return smtp.NewClient(conn, host)
}

// GenerateRandomEmail generates a random email address using the domain passed. Used
// primarily for checking the existence of a catch-all address
func GenerateRandomEmail(domain string) string {
	r := make([]byte, 32)
	for i := 0; i < 32; i++ {
		r[i] = alphanumeric[rand.Intn(len(alphanumeric))] //nolint:gosec
	}
	return fmt.Sprintf("%s@%s", string(r), domain)

}

// establishConnection connects to the address on the named network address.
func establishConnection(addr string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("tcp", addr, timeout)
}

// establishProxyConnection connects to the address on the named network address
// via proxy protocol
func establishProxyConnection(addr, proxyURI string, timeout time.Duration) (net.Conn, error) {
	u, err := url.Parse(proxyURI)
	if err != nil {
		return nil, err
	}
	dialer, err := proxy.FromURL(u, nil)
	if err != nil {
		return nil, err
	}

	// https://github.com/golang/go/issues/37549#issuecomment-1178745487
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return dialer.(proxy.ContextDialer).DialContext(ctx, "tcp", addr)
}
