package emailverifier

import "net"

// Mx is detail about the Mx host
type Mx struct {
	HasMXRecord bool      // whether has 1 or more usable explicit MX record
	Records     []*net.MX // represent DNS MX records (synthesized {Host: domain} when implicit)

	// Additive Syncore evidence.
	NullMX         bool   // RFC 7505: a sole MX target of "." means the domain refuses mail
	ImplicitMX     bool   // no MX records, but the domain's A/AAAA record is used as an implicit mail exchanger
	MailHostSource string // how the mail host was resolved: mx | a | aaaa | null | none
}

// CheckMX will return the DNS MX records for the given domain name sorted by preference.
//
// It additionally detects RFC 7505 Null MX (a sole MX target of ".") and, when no
// MX records exist, falls back to implicit mail exchange via the domain's A/AAAA
// record. A Null MX never falls back to A/AAAA.
func (v *Verifier) CheckMX(domain string) (*Mx, error) {
	domain = domainToASCII(domain)
	records, source, nullMX, err := v.resolveMailHosts(domain)
	if err != nil {
		return &Mx{MailHostSource: source, NullMX: nullMX}, err
	}

	mx := &Mx{MailHostSource: source, NullMX: nullMX}
	switch source {
	case mailHostSourceMX:
		mx.HasMXRecord = true
		mx.Records = records
	case mailHostSourceA, mailHostSourceAAAA:
		mx.ImplicitMX = true
		mx.Records = records
	}
	return mx, nil
}

// resolveMailHosts resolves the dialable mail exchangers for a domain and reports
// how they were resolved. It is shared by CheckMX (evidence) and newSMTPClient
// (dialing) and uses the instance-scoped resolvers so it is race-safe under tests.
//
// Returns:
//   - records: dialable MX records (usable explicit MX, or a synthesized implicit host); nil for null/none
//   - source:  mx | a | aaaa | null | none
//   - nullMX:  true when the domain publishes a Null MX
//   - err:     a DNS lookup error when the domain does not resolve at all
func (v *Verifier) resolveMailHosts(domain string) (records []*net.MX, source string, nullMX bool, err error) {
	mxRecords, err := v.lookupMX(domain)
	if err != nil && len(mxRecords) == 0 {
		return nil, mailHostSourceNone, false, err
	}

	if isNullMX(mxRecords) {
		return nil, mailHostSourceNull, true, nil
	}

	usable := usableMXRecords(mxRecords)
	if len(usable) > 0 {
		return usable, mailHostSourceMX, false, nil
	}

	// No usable MX records: fall back to implicit mail exchange via A/AAAA.
	ips, ipErr := v.lookupIP(domain)
	if ipErr == nil && len(ips) > 0 {
		if hasIPv4(ips) {
			return []*net.MX{{Host: domain}}, mailHostSourceA, false, nil
		}
		return []*net.MX{{Host: domain}}, mailHostSourceAAAA, false, nil
	}

	return nil, mailHostSourceNone, false, nil
}

// isNullMX reports whether the records describe an RFC 7505 Null MX: a single
// record whose target is the root ("." or empty).
func isNullMX(records []*net.MX) bool {
	if len(records) != 1 {
		return false
	}
	host := records[0].Host
	return host == "." || host == ""
}

// usableMXRecords filters out empty and root ("." ) targets that cannot be dialed.
func usableMXRecords(records []*net.MX) []*net.MX {
	usable := make([]*net.MX, 0, len(records))
	for _, r := range records {
		if r == nil || r.Host == "" || r.Host == "." {
			continue
		}
		usable = append(usable, r)
	}
	return usable
}

// hasIPv4 reports whether any of the IPs is an IPv4 address.
func hasIPv4(ips []net.IP) bool {
	for _, ip := range ips {
		if ip.To4() != nil {
			return true
		}
	}
	return false
}
