package license

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/agent-fault-check/afc/internal/config"
)

const MaxFreeScenarios = 3

// UpgradeURL is the checkout URL printed in entitlement error messages.
// Can be overridden at build time via -ldflags.
var UpgradeURL = "https://afc.evhu.net/#pricing"

// Tier represents the active entitlement level.
type Tier string

const (
	TierFree Tier = "free"
	TierPro  Tier = "pro"
	TierTeam Tier = "team"
)

// ValidatorFunc validates a license key and returns the resolved tier.
type ValidatorFunc func(ctx context.Context, key string) (Tier, error)

var (
	paidValidator ValidatorFunc
	paidActivator ValidatorFunc
)

// RegisterPaidValidator registers the commercial license validator (called by -tags pro builds).
func RegisterPaidValidator(fn ValidatorFunc) {
	paidValidator = fn
}

// RegisterPaidActivator registers the commercial license activator (called by -tags pro builds).
func RegisterPaidActivator(fn ValidatorFunc) {
	paidActivator = fn
}

// ResolveKey returns the license key from flag (precedence) or AFC_LICENSE_KEY env var.
func ResolveKey(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	return os.Getenv("AFC_LICENSE_KEY")
}

// Preflight validates entitlements for the requested spec and report outputs.
func Preflight(ctx context.Context, spec *config.Spec, scenarioFilter string, sarifPath, junitPath, licenseKey string) (Tier, error) {
	needsPaid, reason := requiresPaidTier(spec, scenarioFilter, sarifPath, junitPath)

	if licenseKey == "" {
		if needsPaid {
			return TierFree, fmt.Errorf("entitlement required: %s (Free tier is limited to %d scenarios, Postgres/budget assertions, and console output; upgrade at %s and set AFC_LICENSE_KEY)", reason, MaxFreeScenarios, UpgradeURL)
		}
		return TierFree, nil
	}

	if paidValidator == nil {
		if !needsPaid {
			return TierFree, nil
		}
		return TierFree, fmt.Errorf("license validation is not included in the Apache-2.0 open-source build; download the official Pro distribution at %s", UpgradeURL)
	}

	tier, err := paidValidator(ctx, licenseKey)
	if err != nil {
		return TierFree, fmt.Errorf("license validation failed: %w", err)
	}
	if tier == TierFree && needsPaid {
		return TierFree, fmt.Errorf("active tier %q does not include: %s", tier, reason)
	}
	return tier, nil
}

// Activate validates and caches a license key immediately.
func Activate(ctx context.Context, licenseKey string) (Tier, error) {
	if licenseKey == "" {
		return TierFree, errors.New("no license key provided (set AFC_LICENSE_KEY or pass --license-key)")
	}
	fn := paidActivator
	if fn == nil {
		fn = paidValidator
	}
	if fn == nil {
		return TierFree, errors.New("license activation is not included in the Apache-2.0 open-source build; use the official Pro distribution")
	}
	return fn(ctx, licenseKey)
}

func requiresPaidTier(spec *config.Spec, scenarioFilter, sarifPath, junitPath string) (bool, string) {
	if sarifPath != "" {
		return true, "SARIF report output"
	}
	if junitPath != "" {
		return true, "JUnit report output"
	}
	if scenarioFilter == "" && len(spec.Scenarios) > MaxFreeScenarios {
		return true, fmt.Sprintf("%d scenarios in spec (Free limit is %d)", len(spec.Scenarios), MaxFreeScenarios)
	}
	for _, sc := range spec.Scenarios {
		if scenarioFilter != "" && sc.ID != scenarioFilter {
			continue
		}
		for _, a := range sc.Assertions {
			switch a.Type {
			case "redis_key_count", "redis_key_absent", "http_state_count":
				return true, fmt.Sprintf("assertion type %q", a.Type)
			}
		}
	}
	return false, ""
}
