package main

import (
	"net"
	"strings"
)

// spamhausDBLLookup reports whether a domain is on the Spamhaus Domain Block List
// (a domain-based blocklist of spam/phishing/malware domains).
//
// A DBL listing resolves "<domain>.dbl.spamhaus.org" to a 127.0.1.x address.
// A DNS error (NXDOMAIN) means "not listed". 127.255.255.x are Spamhaus
// error/blocked return codes (e.g. public-resolver or over-usage blocks) and are
// treated as not-listed so they never produce a false positive.
func spamhausDBLLookup(domain string) (bool, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return false, nil
	}
	addrs, err := net.LookupHost(domain + ".dbl.spamhaus.org")
	if err != nil {
		return false, nil
	}
	for _, a := range addrs {
		if strings.HasPrefix(a, "127.0.1.") {
			return true, nil
		}
	}
	return false, nil
}
