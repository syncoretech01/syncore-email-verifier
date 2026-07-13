package emailverifier

import (
	"bufio"
	"errors"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// fakeSMTPServer is a minimal, in-process SMTP server used to exercise CheckSMTP
// deterministically without contacting any public service. It replies to RCPT
// commands in order (replies[0] for the 1st RCPT, replies[1] for the 2nd, ...),
// and can hang on a given RCPT index to simulate an operation timeout.
type fakeSMTPServer struct {
	ln       net.Listener
	replies  []string      // reply per RCPT, in order; "" or missing → "250 OK"
	hangRCPT map[int]bool  // 1-based RCPT index → withhold the reply (force a timeout)
	quit     chan struct{} // closed on shutdown to release any hung handler

	mu        sync.Mutex // guards the captured conversation fields below
	helloLine string     // last EHLO/HELO line seen
	mailLine  string     // last MAIL FROM line seen
}

func (fs *fakeSMTPServer) lastHello() string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.helloLine
}

func (fs *fakeSMTPServer) lastMailFrom() string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.mailLine
}

func newFakeSMTPServer(t *testing.T, replies []string, hang map[int]bool) *fakeSMTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake SMTP server: %v", err)
	}
	fs := &fakeSMTPServer{
		ln:       ln,
		replies:  replies,
		hangRCPT: hang,
		quit:     make(chan struct{}),
	}
	go fs.serve()
	return fs
}

func (fs *fakeSMTPServer) serve() {
	for {
		conn, err := fs.ln.Accept()
		if err != nil {
			return
		}
		go fs.handle(conn)
	}
}

func (fs *fakeSMTPServer) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	writeLine := func(s string) {
		_, _ = w.WriteString(s + "\r\n")
		_ = w.Flush()
	}

	writeLine("220 fake ESMTP ready")
	rcptCount := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			fs.mu.Lock()
			fs.helloLine = strings.TrimSpace(line)
			fs.mu.Unlock()
			writeLine("250 fake")
		case strings.HasPrefix(cmd, "MAIL"):
			fs.mu.Lock()
			fs.mailLine = strings.TrimSpace(line)
			fs.mu.Unlock()
			writeLine("250 OK")
		case strings.HasPrefix(cmd, "RCPT"):
			rcptCount++
			if fs.hangRCPT[rcptCount] {
				// Withhold the reply; the client hits its operation deadline.
				<-fs.quit
				return
			}
			reply := "250 OK"
			if rcptCount-1 < len(fs.replies) && fs.replies[rcptCount-1] != "" {
				reply = fs.replies[rcptCount-1]
			}
			writeLine(reply)
		case strings.HasPrefix(cmd, "QUIT"):
			writeLine("221 Bye")
			return
		default:
			writeLine("250 OK")
		}
	}
}

func (fs *fakeSMTPServer) close() {
	close(fs.quit)
	_ = fs.ln.Close()
}

// newFakeVerifier builds a Verifier wired to the fake server via instance-scoped
// dependencies. Each test owns its own verifier, so there is no shared mutable
// global and the suite is race-clean.
func newFakeVerifier(fs *fakeSMTPServer, opTimeout time.Duration) *Verifier {
	v := NewVerifier().EnableSMTPCheck()
	v.connectTimeout = 2 * time.Second
	v.operationTimeout = opTimeout
	v.lookupMX = func(string) ([]*net.MX, error) {
		return []*net.MX{{Host: "fake.invalid.", Pref: 10}}, nil
	}
	v.lookupIP = func(string) ([]net.IP, error) { return nil, nil }

	addr := fs.ln.Addr().String()
	host, _, _ := net.SplitHostPort(addr)
	v.dial = func(_, _ string, connectTimeout, operationTimeout time.Duration) (*smtp.Client, error) {
		conn, err := net.DialTimeout("tcp", addr, connectTimeout)
		if err != nil {
			return nil, err
		}
		_ = conn.SetDeadline(time.Now().Add(operationTimeout))
		return smtp.NewClient(conn, host)
	}
	return v
}

// --- Real-recipient scenarios (catch-all check disabled so RCPT #1 is the real recipient) ---

func TestFakeSMTP_RealRecipient(t *testing.T) {
	cases := []struct {
		name            string
		reply           string
		wantResult      string
		wantReason      string
		wantCode        int
		wantDeliverable bool
		wantFullInbox   bool
		wantDisabled    bool
	}{
		{"accepted", "250 2.1.5 OK", recipientAccepted, "", 250, true, false, false},
		{"nonexistent", "550 5.1.1 user unknown", recipientRejected, reasonMailboxNotFound, 550, false, false, false},
		{"ambiguous_550", "550 5.0.0 mailbox unavailable", recipientBlocked, reasonPolicyBlock, 550, false, false, false},
		{"broad_address_rejected", "550 5.7.1 recipient rejected", recipientBlocked, reasonPolicyBlock, 550, false, false, false},
		{"temporary_421", "421 4.7.0 try again later", recipientTemporary, reasonTemporaryFailure, 421, false, false, false},
		{"temporary_450", "450 4.2.0 mailbox busy", recipientTemporary, reasonTemporaryFailure, 450, false, false, false},
		{"rate_limited_451", "451 4.7.1 rate limited", recipientTemporary, reasonRateLimited, 451, false, false, false},
		{"full_inbox_452", "452 4.2.2 over quota", recipientTemporary, reasonFullInbox, 452, false, true, false},
		{"disabled_554", "554 5.7.1 account disabled", recipientRejected, reasonMailboxDisabled, 554, false, false, true},
		{"disabled_550", "550 5.2.1 mailbox is suspended", recipientRejected, reasonMailboxDisabled, 550, false, false, true},
		{"generic_554", "554 5.7.1 transaction failed", recipientBlocked, reasonPolicyBlock, 554, false, false, false},
		{"policy_554", "554 5.7.1 blocked by spamhaus", recipientBlocked, reasonPolicyBlock, 554, false, false, false},
	}

	for _, c := range cases {
		tc := c
		t.Run(tc.name, func(t *testing.T) {
			fs := newFakeSMTPServer(t, []string{tc.reply}, nil)
			defer fs.close()
			v := newFakeVerifier(fs, 5*time.Second).DisableCatchAllCheck()

			res, err := v.CheckSMTP("example.com", "user")
			assert.NoError(t, err)
			assert.Equal(t, tc.wantResult, res.RecipientResult)
			assert.Equal(t, tc.wantReason, res.RecipientReason)
			assert.Equal(t, tc.wantCode, res.SMTPCode)
			assert.Equal(t, tc.wantDeliverable, res.Deliverable)
			assert.Equal(t, tc.wantFullInbox, res.FullInbox)
			assert.Equal(t, tc.wantDisabled, res.Disabled)
			assert.Equal(t, sourceSMTP, res.Source)
			assert.Equal(t, catchAllNotChecked, res.CatchAllResult)
			assert.True(t, res.HostExists)
		})
	}
}

// --- Catch-all scenarios (catch-all check enabled → RCPT #1 is the random probe) ---

func TestFakeSMTP_ConfirmedCatchAll(t *testing.T) {
	fs := newFakeSMTPServer(t, []string{"250 OK"}, nil) // random probe accepted
	defer fs.close()
	v := newFakeVerifier(fs, 5*time.Second)

	res, err := v.CheckSMTP("example.com", "user")
	assert.NoError(t, err)
	assert.Equal(t, catchAllConfirmed, res.CatchAllResult)
	assert.True(t, res.CatchAll)
	// A confirmed catch-all short-circuits before the real recipient is probed.
	assert.Equal(t, recipientNotChecked, res.RecipientResult)
}

func TestFakeSMTP_CleanNonCatchAllThenAccepted(t *testing.T) {
	// RCPT #1 (random) = clean nonexistent, RCPT #2 (real) = accepted.
	fs := newFakeSMTPServer(t, []string{"550 5.1.1 no such user", "250 OK"}, nil)
	defer fs.close()
	v := newFakeVerifier(fs, 5*time.Second)

	res, err := v.CheckSMTP("example.com", "user")
	assert.NoError(t, err)
	assert.Equal(t, catchAllNot, res.CatchAllResult)
	assert.False(t, res.CatchAll)
	assert.Equal(t, recipientAccepted, res.RecipientResult)
	assert.True(t, res.Deliverable)
}

func TestFakeSMTP_RandomProbe_Ambiguous(t *testing.T) {
	// Temporary / policy replies to the random probe must yield catch_all_result
	// = unknown, never catch_all = true.
	cases := []struct {
		name  string
		reply string
	}{
		{"random_421", "421 4.7.0 slow down"},
		{"random_554_policy", "554 5.7.1 blocked by policy"},
	}
	for _, c := range cases {
		tc := c
		t.Run(tc.name, func(t *testing.T) {
			fs := newFakeSMTPServer(t, []string{tc.reply}, nil)
			defer fs.close()
			v := newFakeVerifier(fs, 5*time.Second)

			// username empty → returns right after the probe.
			res, err := v.CheckSMTP("example.com", "")
			assert.NoError(t, err)
			assert.Equal(t, catchAllUnknown, res.CatchAllResult)
			assert.False(t, res.CatchAll)
		})
	}
}

func TestFakeSMTP_RandomProbe_Timeout(t *testing.T) {
	fs := newFakeSMTPServer(t, nil, map[int]bool{1: true}) // hang on the random probe
	defer fs.close()
	v := newFakeVerifier(fs, 500*time.Millisecond)

	res, err := v.CheckSMTP("example.com", "user")
	assert.Equal(t, catchAllUnknown, res.CatchAllResult)
	assert.False(t, res.CatchAll)
	le := asLookupError(t, err)
	assert.Equal(t, ErrTimeout, le.Message)
}

func TestFakeSMTP_RealRecipient_Timeout(t *testing.T) {
	fs := newFakeSMTPServer(t, nil, map[int]bool{1: true}) // hang on the (only) real RCPT
	defer fs.close()
	v := newFakeVerifier(fs, 500*time.Millisecond).DisableCatchAllCheck()

	res, err := v.CheckSMTP("example.com", "user")
	assert.Equal(t, recipientUnknown, res.RecipientResult)
	le := asLookupError(t, err)
	assert.Equal(t, ErrTimeout, le.Message)
}

func TestFakeSMTP_ConnectionRefused(t *testing.T) {
	v := NewVerifier().EnableSMTPCheck()
	v.lookupMX = func(string) ([]*net.MX, error) {
		return []*net.MX{{Host: "fake.invalid.", Pref: 10}}, nil
	}
	v.lookupIP = func(string) ([]net.IP, error) { return nil, nil }
	v.dial = func(_, _ string, _, _ time.Duration) (*smtp.Client, error) {
		return nil, errors.New("dial tcp 127.0.0.1:25: connect: connection refused")
	}

	_, err := v.CheckSMTP("example.com", "user")
	le := asLookupError(t, err)
	assert.Equal(t, ErrConnRefused, le.Message)
}

func asLookupError(t *testing.T, err error) *LookupError {
	t.Helper()
	assert.Error(t, err)
	le, ok := err.(*LookupError)
	if !ok {
		t.Fatalf("expected *LookupError, got %T: %v", err, err)
	}
	return le
}

// --- Deterministic coverage for behaviors whose original tests were relocated
// to the live suite (FromEmail, HelloName, EnableSMTPCheck/DisableSMTPCheck). ---

func TestFakeSMTP_FromEmailAndHelloName(t *testing.T) {
	fs := newFakeSMTPServer(t, []string{"250 OK"}, nil)
	defer fs.close()
	v := newFakeVerifier(fs, 5*time.Second).
		DisableCatchAllCheck().
		FromEmail("sender@syncore.test").
		HelloName("syncore.test")

	res, err := v.CheckSMTP("example.com", "user")
	assert.NoError(t, err)
	assert.True(t, res.Deliverable)

	// The configured HELO name and MAIL FROM address must reach the server.
	assert.Contains(t, fs.lastHello(), "syncore.test")
	assert.Contains(t, fs.lastMailFrom(), "sender@syncore.test")
}

func TestFakeSMTP_SMTPCheckToggle(t *testing.T) {
	fs := newFakeSMTPServer(t, []string{"250 OK"}, nil)
	defer fs.close()
	v := newFakeVerifier(fs, 5*time.Second).DisableCatchAllCheck()

	// Enabled (newFakeVerifier calls EnableSMTPCheck): a real check occurs.
	res, err := v.CheckSMTP("example.com", "user")
	assert.NoError(t, err)
	assert.NotNil(t, res)
	assert.True(t, res.HostExists)

	// Disabled: CheckSMTP short-circuits to (nil, nil) without dialing.
	v.DisableSMTPCheck()
	res2, err2 := v.CheckSMTP("example.com", "user")
	assert.NoError(t, err2)
	assert.Nil(t, res2)
}
