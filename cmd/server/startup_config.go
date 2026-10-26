package main

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"regexp"
	"strings"

	"durpdeploy/internal/config"
	"durpdeploy/internal/dbconfig"
	"durpdeploy/internal/maintenance"
	"durpdeploy/internal/mfa"
	"durpdeploy/internal/oidc"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
)

type serverStartup struct {
	settings config.Config
	mfa      mfa.Config
	oidc     oidc.Config
	box      *secret.Box
	backup   maintenance.CheckConfig
}

// loadServerStartup validates settings before identity creation, migrations,
// listeners, or background work. Runtime availability is checked by its owner.
func loadServerStartup() (serverStartup, error) {
	return loadServerStartupWithLookup(os.LookupEnv)
}

func loadServerStartupWithLookup(lookup config.Lookup) (serverStartup, error) {
	settings, err := config.LoadWithLookup(lookup)
	if err != nil {
		return serverStartup{}, err
	}
	if err := validateStartupAddresses(settings.LookupEnv); err != nil {
		return serverStartup{}, err
	}
	if err := validateStartupProxies(settings.TrustedProxies); err != nil {
		return serverStartup{}, err
	}
	if err := validateConfiguredDSN(settings.LookupEnv); err != nil {
		return serverStartup{}, err
	}
	mfaConfig, err := mfa.LoadConfigWithLookup(settings.LookupEnv)
	if err != nil {
		return serverStartup{}, err
	}
	oidcConfig, err := oidc.LoadConfigWithLookup(settings.LookupEnv)
	if err != nil {
		return serverStartup{}, err
	}
	if _, err := runner.LoadContainerConfig(settings.LookupEnv); err != nil {
		return serverStartup{}, err
	}
	if err := validateScrubPatterns(settings.ExtraScrubPatterns); err != nil {
		return serverStartup{}, err
	}
	backup, err := maintenance.LoadCheckConfig(settings.LookupEnv)
	if err != nil {
		return serverStartup{}, err
	}
	key, err := secret.LoadKeyWithLookup(settings.LookupEnv)
	if err != nil {
		return serverStartup{}, err
	}
	box, err := secret.NewBox(key)
	if err != nil {
		return serverStartup{}, err
	}
	return serverStartup{settings: settings, mfa: mfaConfig, oidc: oidcConfig,
		box: box, backup: backup}, nil
}

func validateConfiguredDSN(lookup config.Lookup) error {
	if _, err := dbconfig.InspectConnectionSecurity(loadDSNWithLookup(lookup)); err != nil {
		return errors.New("invalid DURPDEPLOY_DB configuration")
	}
	return nil
}

func validateStartupAddresses(lookup config.Lookup) error {
	_, port, err := net.SplitHostPort(loadAddrWithLookup(lookup))
	if err != nil {
		return errors.New("DURPDEPLOY_ADDR must contain a host and port")
	}
	if _, err := net.LookupPort("tcp", port); err != nil {
		return errors.New("DURPDEPLOY_ADDR contains an invalid port")
	}
	return nil
}

func validateScrubPatterns(raw string) error {
	for _, pattern := range strings.Split(raw, ",") {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return errors.New(
				"DURPDEPLOY_EXTRA_SCRUB_PATTERNS must contain valid regular expressions",
			)
		}
	}
	return nil
}

func validateStartupProxies(raw string) error {
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, err := netip.ParseAddr(item); err == nil {
			continue
		}
		if _, err := netip.ParsePrefix(item); err != nil {
			return errors.New(
				"DURPDEPLOY_TRUSTED_PROXIES must contain IP addresses or CIDR prefixes",
			)
		}
	}
	return nil
}
