package paymentmethods

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"strings"
	"time"
)

// ValidateEmailFormat checks that raw parses as a single, bare email address
// (no "Display Name <addr>" wrapper, no trailing garbage).
func ValidateEmailFormat(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("email: empty")
	}
	if strings.ContainsAny(trimmed, "<>\"") {
		return fmt.Errorf("email: expected a bare address, not %q", trimmed)
	}
	if _, err := mail.ParseAddress(trimmed); err != nil {
		return fmt.Errorf("email: invalid format: %w", err)
	}
	return nil
}

// mxLookup and hostLookup are overridable in tests so domain validation never
// depends on real DNS in CI.
var (
	mxLookup   = net.DefaultResolver.LookupMX
	hostLookup = net.DefaultResolver.LookupHost
)

// ValidateEmailDomain checks that the email's domain can plausibly receive
// mail: an MX record, or (per RFC 5321) a fallback A/AAAA record if no MX is
// published. It does not confirm the mailbox itself exists.
//
// A resolver timeout or transient DNS error is deliberately treated as a
// pass, not a failure: this infra has had DNS/networking hiccups before, and
// a flaky lookup must never lock a user out of saving an account. Only a
// definitive "no such domain" / empty answer is a hard rejection.
func ValidateEmailDomain(ctx context.Context, raw string) error {
	trimmed := strings.TrimSpace(raw)
	at := strings.LastIndex(trimmed, "@")
	if at < 0 || at == len(trimmed)-1 {
		return fmt.Errorf("email: missing domain in %q", raw)
	}
	domain := trimmed[at+1:]

	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	mxRecords, err := mxLookup(lookupCtx, domain)
	if err == nil && len(mxRecords) > 0 {
		return nil
	}
	if err != nil && !isNoSuchHost(err) {
		return nil // resolver trouble: soft-pass
	}

	hosts, err := hostLookup(lookupCtx, domain)
	if err == nil && len(hosts) > 0 {
		return nil
	}
	if err != nil && !isNoSuchHost(err) {
		return nil // resolver trouble: soft-pass
	}

	return fmt.Errorf("email: domain %q has no mail server", domain)
}

// isNoSuchHost reports whether err is a definitive "domain does not exist"
// DNS answer, as opposed to a timeout or other transient resolver failure.
func isNoSuchHost(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsNotFound
	}
	return false
}
