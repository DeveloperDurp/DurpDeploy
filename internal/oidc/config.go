package oidc

import (
	"errors"
	"net/url"
	"os"
	"strings"
)

var ErrInvalidConfig = errors.New("invalid OIDC configuration")

type Config struct {
	Enabled              bool
	Issuer               string
	ClientID             string
	ClientSecret         string
	CallbackURL          string
	Scopes               []string
	DisplayName          string
	GroupClaim           string
	AdminGroup           string
	DeployerGroup        string
	ViewerGroup          string
	AllowUnverifiedEmail bool
}

func LoadConfig() (Config, error) {
	values := oidcEnvValues()
	if !values.anySet() {
		return Config{}, nil
	}
	if values.hasEmptyRequiredValue() {
		return Config{}, ErrInvalidConfig
	}

	issuer, err := parseIssuer(values.issuer.value)
	if err != nil {
		return Config{}, ErrInvalidConfig
	}
	publicURL, err := parsePublicURL(values.publicURL.value)
	if err != nil {
		return Config{}, ErrInvalidConfig
	}
	if !groupsAreDistinct(
		values.adminGroup.value,
		values.deployerGroup.value,
		values.viewerGroup.value,
	) {
		return Config{}, ErrInvalidConfig
	}

	displayName := values.displayName.value
	allowUnverifiedEmail := false
	if values.requireEmailVerified.set {
		switch values.requireEmailVerified.value {
		case "true":
		case "false":
			allowUnverifiedEmail = true
		default:
			return Config{}, ErrInvalidConfig
		}
	}
	if displayName == "" {
		displayName = "SSO"
	}
	groupClaim := values.groupClaim.value
	if groupClaim == "" {
		groupClaim = "groups"
	}

	return Config{
		Enabled:              true,
		Issuer:               issuer,
		ClientID:             values.clientID.value,
		ClientSecret:         values.clientSecret.value,
		CallbackURL:          publicURL + "/login/oidc/callback",
		Scopes:               []string{"openid", "profile", "email"},
		DisplayName:          displayName,
		GroupClaim:           groupClaim,
		AdminGroup:           values.adminGroup.value,
		DeployerGroup:        values.deployerGroup.value,
		ViewerGroup:          values.viewerGroup.value,
		AllowUnverifiedEmail: allowUnverifiedEmail,
	}, nil
}

type envValue struct {
	value string
	set   bool
}

type oidcEnv struct {
	publicURL            envValue
	issuer               envValue
	clientID             envValue
	clientSecret         envValue
	adminGroup           envValue
	deployerGroup        envValue
	viewerGroup          envValue
	displayName          envValue
	groupClaim           envValue
	requireEmailVerified envValue
}

func oidcEnvValues() oidcEnv {
	return oidcEnv{
		publicURL:            readEnv("DURPDEPLOY_URL"),
		issuer:               readEnv("DURPDEPLOY_OIDC_ISSUER"),
		clientID:             readEnv("DURPDEPLOY_OIDC_CLIENT_ID"),
		clientSecret:         readEnv("DURPDEPLOY_OIDC_CLIENT_SECRET"),
		adminGroup:           readEnv("DURPDEPLOY_OIDC_ADMIN_GROUP"),
		deployerGroup:        readEnv("DURPDEPLOY_OIDC_DEPLOYER_GROUP"),
		viewerGroup:          readEnv("DURPDEPLOY_OIDC_VIEWER_GROUP"),
		displayName:          readEnv("DURPDEPLOY_OIDC_DISPLAY_NAME"),
		groupClaim:           readEnv("DURPDEPLOY_OIDC_GROUP_CLAIM"),
		requireEmailVerified: readEnv("DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED"),
	}
}

func readEnv(key string) envValue {
	value, set := os.LookupEnv(key)
	return envValue{value: value, set: set}
}

func (e oidcEnv) anySet() bool {
	return e.issuer.set || e.clientID.set || e.clientSecret.set ||
		e.adminGroup.set || e.deployerGroup.set || e.viewerGroup.set ||
		e.displayName.set || e.groupClaim.set || e.requireEmailVerified.set
}

func (e oidcEnv) hasEmptyRequiredValue() bool {
	return !e.publicURL.set || e.publicURL.value == "" ||
		!e.issuer.set || e.issuer.value == "" ||
		!e.clientID.set || e.clientID.value == "" ||
		!e.clientSecret.set || e.clientSecret.value == "" ||
		!e.adminGroup.set || strings.TrimSpace(e.adminGroup.value) == "" ||
		!e.deployerGroup.set || strings.TrimSpace(e.deployerGroup.value) == "" ||
		!e.viewerGroup.set || strings.TrimSpace(e.viewerGroup.value) == "" ||
		(e.displayName.set && strings.TrimSpace(e.displayName.value) == "") ||
		(e.groupClaim.set && strings.TrimSpace(e.groupClaim.value) == "")
}

func parseIssuer(raw string) (string, error) {
	issuer, err := url.Parse(raw)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" ||
		issuer.User != nil || issuer.RawQuery != "" || issuer.ForceQuery ||
		issuer.Fragment != "" {
		return "", ErrInvalidConfig
	}
	return issuer.String(), nil
}

func parsePublicURL(raw string) (string, error) {
	publicURL, err := url.Parse(raw)
	if err != nil || publicURL.Scheme != "https" || publicURL.Host == "" ||
		publicURL.User != nil || publicURL.Path != "" ||
		publicURL.RawQuery != "" || publicURL.ForceQuery ||
		publicURL.Fragment != "" {
		return "", ErrInvalidConfig
	}
	return publicURL.String(), nil
}

func groupsAreDistinct(groups ...string) bool {
	seen := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		if _, ok := seen[group]; ok {
			return false
		}
		seen[group] = struct{}{}
	}
	return true
}
