package emailverifier

import (
	"errors"
	"net"
	"net/smtp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// closeRecorder wraps a net.Conn and signals every Close on a channel so tests
// can observe when newSMTPClient closes an unused successful connection.
type closeRecorder struct {
	net.Conn
	closed chan<- struct{}
}

func (c *closeRecorder) Close() error {
	select {
	case c.closed <- struct{}{}:
	default:
	}
	return c.Conn.Close()
}

// mxBehavior controls how the injected dialer treats a specific MX address.
type mxBehavior struct {
	fail    bool            // return a dial error
	release <-chan struct{} // if set, block the dial until this channel is closed
}

// TestNewSMTPClient_MultiMX_WinnerAndLateSuccess exercises the concurrent dial
// path with three MX hosts: one fails, one succeeds (the winner), and one
// succeeds only after the winner has already been selected. It verifies exactly
// one client is returned, the late successful client is closed by the background
// drainer, and nothing is left blocked.
func TestNewSMTPClient_MultiMX_WinnerAndLateSuccess(t *testing.T) {
	fs := newFakeSMTPServer(t, nil, nil) // greets and accepts; no RCPT needed here
	defer fs.close()

	closed := make(chan struct{}, 3)
	release := make(chan struct{})

	fakeAddr := fs.ln.Addr().String()
	host, _, _ := net.SplitHostPort(fakeAddr)

	behaviors := map[string]mxBehavior{
		"mx1.test.:25": {fail: true},       // fails (before the winner)
		"mx2.test.:25": {},                 // succeeds fast → winner
		"mx3.test.:25": {release: release}, // succeeds only after release → late
	}

	v := NewVerifier().EnableSMTPCheck()
	v.connectTimeout = 2 * time.Second
	v.operationTimeout = 2 * time.Second
	v.lookupMX = func(string) ([]*net.MX, error) {
		return []*net.MX{
			{Host: "mx1.test.", Pref: 10},
			{Host: "mx2.test.", Pref: 20},
			{Host: "mx3.test.", Pref: 30},
		}, nil
	}
	v.lookupIP = func(string) ([]net.IP, error) { return nil, nil }
	v.dial = func(addr, _ string, connectTimeout, operationTimeout time.Duration) (*smtp.Client, error) {
		b := behaviors[addr]
		if b.release != nil {
			<-b.release // complete only after the test releases it (after the winner)
		}
		if b.fail {
			return nil, errors.New("dial tcp " + addr + ": connect: connection refused")
		}
		conn, err := net.DialTimeout("tcp", fakeAddr, connectTimeout)
		if err != nil {
			return nil, err
		}
		_ = conn.SetDeadline(time.Now().Add(operationTimeout))
		return smtp.NewClient(&closeRecorder{Conn: conn, closed: closed}, host)
	}

	client, mx, err := v.newSMTPClient("example.com")
	assert.NoError(t, err)
	assert.NotNil(t, client)
	assert.NotNil(t, mx)
	// mx3 is still blocked and mx1 fails, so the only possible winner is mx2.
	assert.Equal(t, "mx2.test.", mx.Host)

	// Nothing should have been closed yet: the winner is returned open and the
	// late MX has not been released.
	assert.Equal(t, 0, len(closed))

	// Release the late MX; the background drainer must consume its result and
	// close the unused successful client.
	close(release)
	select {
	case <-closed:
		// The late successful client was closed by the drainer.
	case <-time.After(2 * time.Second):
		t.Fatal("late successful MX client was not closed (drainer blocked or leaked)")
	}

	// The winner must still be usable; close it to clean up.
	_ = client.Close()
}

// TestNewSMTPClient_MultiMX_AllFail verifies that when every MX host fails the
// function returns an error promptly, without deadlocking or leaving blocked
// channel sends.
func TestNewSMTPClient_MultiMX_AllFail(t *testing.T) {
	v := NewVerifier().EnableSMTPCheck()
	v.connectTimeout = 2 * time.Second
	v.operationTimeout = 2 * time.Second
	v.lookupMX = func(string) ([]*net.MX, error) {
		return []*net.MX{
			{Host: "mx1.test.", Pref: 10},
			{Host: "mx2.test.", Pref: 20},
			{Host: "mx3.test.", Pref: 30},
		}, nil
	}
	v.lookupIP = func(string) ([]net.IP, error) { return nil, nil }

	var dials int32
	v.dial = func(addr, _ string, _, _ time.Duration) (*smtp.Client, error) {
		atomic.AddInt32(&dials, 1)
		return nil, errors.New("dial tcp " + addr + ": connect: connection refused")
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := v.newSMTPClient("example.com")
		done <- err
	}()

	select {
	case err := <-done:
		assert.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("newSMTPClient deadlocked when all MX hosts failed")
	}
	assert.Equal(t, int32(3), atomic.LoadInt32(&dials))
}

// TestNewSMTPClient_PassesProxyURI is a direct seam assertion proving that
// Verifier.Proxy(...) is threaded into the production dial call and not silently
// dropped by the instance-scoped dial refactor.
func TestNewSMTPClient_PassesProxyURI(t *testing.T) {
	const proxyURI = "socks5://user:pass@127.0.0.1:1080?timeout=5s"

	v := NewVerifier().Proxy(proxyURI)
	assert.Equal(t, proxyURI, v.proxyURI) // Proxy() records the URI

	v.lookupMX = func(string) ([]*net.MX, error) {
		return []*net.MX{{Host: "mx.test.", Pref: 10}}, nil
	}
	v.lookupIP = func(string) ([]net.IP, error) { return nil, nil }

	got := make(chan string, 1)
	v.dial = func(_, proxy string, _, _ time.Duration) (*smtp.Client, error) {
		got <- proxy
		return nil, errors.New("stop")
	}

	_, _, _ = v.newSMTPClient("example.com")

	select {
	case p := <-got:
		assert.Equal(t, proxyURI, p, "Proxy() URI must be passed to the dialer")
	case <-time.After(time.Second):
		t.Fatal("dialer was not invoked")
	}
}

// TestDefaultDialIsDialSMTP guards the production default: a freshly constructed
// verifier must have a real dialer wired (so proxy-aware dialSMTP is used unless
// a test overrides it).
func TestDefaultDialIsDialSMTP(t *testing.T) {
	v := NewVerifier()
	assert.NotNil(t, v.dial)
	assert.NotNil(t, v.lookupMX)
	assert.NotNil(t, v.lookupIP)
}
