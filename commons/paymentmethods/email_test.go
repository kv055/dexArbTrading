package paymentmethods

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestValidateEmailFormatValid(t *testing.T) {
	valid := []string{
		"user@example.com",
		"first.last+tag@sub.example.co.uk",
		"a@b.io",
	}
	for _, email := range valid {
		if err := ValidateEmailFormat(email); err != nil {
			t.Errorf("ValidateEmailFormat(%q) = %v, want nil", email, err)
		}
	}
}

func TestValidateEmailFormatInvalid(t *testing.T) {
	invalid := []string{
		"",
		"not-an-email",
		"missing-domain@",
		"@missing-local.com",
		"Display Name <user@example.com>",
		`"quoted"@example.com`,
		"two@addresses.com, another@one.com",
	}
	for _, email := range invalid {
		if err := ValidateEmailFormat(email); err == nil {
			t.Errorf("ValidateEmailFormat(%q) = nil, want error", email)
		}
	}
}

// stubDNSNotFound mimics the definitive "no such domain" answer.
func stubDNSNotFound() error {
	return &net.DNSError{Err: "no such host", Name: "does-not-exist.invalid", IsNotFound: true}
}

func TestValidateEmailDomainWithMX(t *testing.T) {
	origMX, origHost := mxLookup, hostLookup
	defer func() { mxLookup, hostLookup = origMX, origHost }()

	mxLookup = func(ctx context.Context, name string) ([]*net.MX, error) {
		return []*net.MX{{Host: "mail." + name, Pref: 10}}, nil
	}
	hostLookup = func(ctx context.Context, host string) ([]string, error) {
		t.Fatal("hostLookup should not be reached when MX resolves")
		return nil, nil
	}

	if err := ValidateEmailDomain(context.Background(), "user@example.com"); err != nil {
		t.Errorf("ValidateEmailDomain = %v, want nil", err)
	}
}

func TestValidateEmailDomainFallsBackToHost(t *testing.T) {
	origMX, origHost := mxLookup, hostLookup
	defer func() { mxLookup, hostLookup = origMX, origHost }()

	mxLookup = func(ctx context.Context, name string) ([]*net.MX, error) {
		return nil, stubDNSNotFound()
	}
	hostLookup = func(ctx context.Context, host string) ([]string, error) {
		return []string{"1.2.3.4"}, nil
	}

	if err := ValidateEmailDomain(context.Background(), "user@example.com"); err != nil {
		t.Errorf("ValidateEmailDomain = %v, want nil (A-record fallback)", err)
	}
}

func TestValidateEmailDomainNoRecordsAtAll(t *testing.T) {
	origMX, origHost := mxLookup, hostLookup
	defer func() { mxLookup, hostLookup = origMX, origHost }()

	mxLookup = func(ctx context.Context, name string) ([]*net.MX, error) {
		return nil, stubDNSNotFound()
	}
	hostLookup = func(ctx context.Context, host string) ([]string, error) {
		return nil, stubDNSNotFound()
	}

	err := ValidateEmailDomain(context.Background(), "user@does-not-exist.invalid")
	if err == nil {
		t.Fatal("ValidateEmailDomain = nil, want error for domain with no records")
	}
}

func TestValidateEmailDomainSoftPassesOnResolverError(t *testing.T) {
	origMX, origHost := mxLookup, hostLookup
	defer func() { mxLookup, hostLookup = origMX, origHost }()

	transient := errors.New("network unreachable")
	mxLookup = func(ctx context.Context, name string) ([]*net.MX, error) {
		return nil, transient
	}
	hostLookup = func(ctx context.Context, host string) ([]string, error) {
		t.Fatal("hostLookup should not be reached after a soft-pass on MX error")
		return nil, nil
	}

	if err := ValidateEmailDomain(context.Background(), "user@example.com"); err != nil {
		t.Errorf("ValidateEmailDomain = %v, want nil (soft-pass on transient resolver error)", err)
	}
}

func TestValidateEmailDomainMissingAt(t *testing.T) {
	if err := ValidateEmailDomain(context.Background(), "no-at-sign"); err == nil {
		t.Error("ValidateEmailDomain = nil, want error for missing @")
	}
}
