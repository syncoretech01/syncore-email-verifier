package emailverifier

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

// These tests exercise MX resolution deterministically via the instance-scoped
// resolvers — no network, race-safe.

func TestCheckMX_ExplicitMX(t *testing.T) {
	v := NewVerifier()
	v.lookupMX = func(string) ([]*net.MX, error) {
		return []*net.MX{{Host: "mail.example.com.", Pref: 10}}, nil
	}
	mx, err := v.CheckMX("example.com")
	assert.NoError(t, err)
	assert.True(t, mx.HasMXRecord)
	assert.False(t, mx.ImplicitMX)
	assert.False(t, mx.NullMX)
	assert.Equal(t, mailHostSourceMX, mx.MailHostSource)
}

func TestCheckMX_NullMX(t *testing.T) {
	v := NewVerifier()
	v.lookupMX = func(string) ([]*net.MX, error) {
		return []*net.MX{{Host: ".", Pref: 0}}, nil
	}
	// A/AAAA must NOT be consulted when a Null MX is published.
	v.lookupIP = func(string) ([]net.IP, error) {
		t.Fatalf("lookupIP must not be called when Null MX is present")
		return nil, nil
	}
	mx, err := v.CheckMX("no-mail.example")
	assert.NoError(t, err)
	assert.True(t, mx.NullMX)
	assert.False(t, mx.HasMXRecord)
	assert.False(t, mx.ImplicitMX)
	assert.Equal(t, mailHostSourceNull, mx.MailHostSource)
}

func TestCheckMX_ImplicitA(t *testing.T) {
	v := NewVerifier()
	v.lookupMX = func(string) ([]*net.MX, error) { return nil, nil }
	v.lookupIP = func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}
	mx, err := v.CheckMX("a-only.example")
	assert.NoError(t, err)
	assert.False(t, mx.HasMXRecord)
	assert.True(t, mx.ImplicitMX)
	assert.Equal(t, mailHostSourceA, mx.MailHostSource)
}

func TestCheckMX_ImplicitAAAA(t *testing.T) {
	v := NewVerifier()
	v.lookupMX = func(string) ([]*net.MX, error) { return nil, nil }
	v.lookupIP = func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("2606:2800:220:1:248:1893:25c8:1946")}, nil
	}
	mx, err := v.CheckMX("aaaa-only.example")
	assert.NoError(t, err)
	assert.False(t, mx.HasMXRecord)
	assert.True(t, mx.ImplicitMX)
	assert.Equal(t, mailHostSourceAAAA, mx.MailHostSource)
}

func TestCheckMX_NoUsableMailHost(t *testing.T) {
	v := NewVerifier()
	v.lookupMX = func(string) ([]*net.MX, error) { return nil, nil }
	v.lookupIP = func(string) ([]net.IP, error) { return nil, nil }
	mx, err := v.CheckMX("no-host.example")
	assert.NoError(t, err)
	assert.False(t, mx.HasMXRecord)
	assert.False(t, mx.ImplicitMX)
	assert.False(t, mx.NullMX)
	assert.Equal(t, mailHostSourceNone, mx.MailHostSource)
}
