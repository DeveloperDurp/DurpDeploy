package oidc

import (
	"bytes"
	"encoding/json"
	"io"
	"net/mail"
	"strings"
	"time"
)

const (
	maxSubjectLength = 255
	maxEmailLength   = 320
	maxNameLength    = 255
)

type Role string

const (
	RoleAdmin    Role = "admin"
	RoleDeployer Role = "deployer"
	RoleViewer   Role = "viewer"
)

type ClaimErrorReason string

const (
	ClaimMissing    ClaimErrorReason = "missing"
	ClaimWrongType  ClaimErrorReason = "wrong type"
	ClaimInvalid    ClaimErrorReason = "invalid"
	ClaimUnverified ClaimErrorReason = "unverified"
	ClaimUnmapped   ClaimErrorReason = "unmapped"
)

// ClaimError identifies an invalid claim without retaining untrusted content.
type ClaimError struct {
	Field  string
	Reason ClaimErrorReason
}

func (e *ClaimError) Error() string {
	return "invalid oidc claim: " + e.Field + " (" + string(e.Reason) + ")"
}

// GroupMapping maps the configured claim and exact group names to local roles.
type GroupMapping struct {
	ClaimName string
	Admin     string
	Deployer  string
	Viewer    string
}

// ClaimIdentity is the validated, provider-neutral identity from an ID token.
// Subject is intentionally preserved exactly; it pairs with the configured
// issuer as the durable identity rather than the mutable email address.
type ClaimIdentity struct {
	Issuer  string
	Subject string
	Email   string
	Name    string
	Role    Role
}

// ParseClaims parses verified ID-token claims without provider-specific types.
func ParseClaims(
	raw []byte,
	issuer string,
	mapping GroupMapping,
	allowUnverifiedEmail bool,
) (ClaimIdentity, error) {
	claims, err := claimDocument(raw)
	if err != nil {
		return ClaimIdentity{}, err
	}

	subject, err := requiredString(claims, "sub")
	if err != nil {
		return ClaimIdentity{}, err
	}
	if !validSubject(subject) {
		return ClaimIdentity{}, claimError("sub", ClaimInvalid)
	}

	email, err := requiredString(claims, "email")
	if err != nil {
		return ClaimIdentity{}, err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if !validEmail(email) {
		return ClaimIdentity{}, claimError("email", ClaimInvalid)
	}

	if err := verifiedEmail(claims, allowUnverifiedEmail); err != nil {
		return ClaimIdentity{}, err
	}

	name, err := optionalName(claims, email)
	if err != nil {
		return ClaimIdentity{}, err
	}

	groups, err := requiredGroups(claims, mapping.ClaimName)
	if err != nil {
		return ClaimIdentity{}, err
	}
	role, err := MapRole(groups, mapping)
	if err != nil {
		return ClaimIdentity{}, err
	}

	return ClaimIdentity{
		Issuer:  issuer,
		Subject: subject,
		Email:   email,
		Name:    name,
		Role:    role,
	}, nil
}

func reauthenticationTime(raw []byte) (time.Time, error) {
	claims, err := claimDocument(raw)
	if err != nil {
		return time.Time{}, err
	}
	value, ok := claims["auth_time"]
	if !ok {
		return time.Time{}, claimError("auth_time", ClaimMissing)
	}
	var seconds int64
	if err := json.Unmarshal(value, &seconds); err != nil ||
		bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return time.Time{}, claimError("auth_time", ClaimWrongType)
	}
	return time.Unix(seconds, 0).UTC(), nil
}

func claimDocument(raw []byte) (map[string]json.RawMessage, error) {
	claims := map[string]json.RawMessage{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&claims); err != nil || claims == nil {
		return nil, claimError("claims", ClaimInvalid)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, claimError("claims", ClaimInvalid)
	}
	return claims, nil
}

// MapRole returns the highest local role represented by exact configured groups.
func MapRole(groups []string, mapping GroupMapping) (Role, error) {
	matchedAdmin := false
	matchedDeployer := false
	matchedViewer := false
	for _, group := range groups {
		switch group {
		case mapping.Admin:
			matchedAdmin = true
		case mapping.Deployer:
			matchedDeployer = true
		case mapping.Viewer:
			matchedViewer = true
		}
	}
	if matchedAdmin {
		return RoleAdmin, nil
	}
	if matchedDeployer {
		return RoleDeployer, nil
	}
	if matchedViewer {
		return RoleViewer, nil
	}
	return "", claimError(mapping.ClaimName, ClaimUnmapped)
}

func requiredString(
	claims map[string]json.RawMessage,
	field string,
) (string, error) {
	raw, ok := claims[field]
	if !ok {
		return "", claimError(field, ClaimMissing)
	}
	var value string
	if !isJSONString(raw) || json.Unmarshal(raw, &value) != nil {
		return "", claimError(field, ClaimWrongType)
	}
	return value, nil
}

func verifiedEmail(
	claims map[string]json.RawMessage,
	allowUnverifiedEmail bool,
) error {
	raw, ok := claims["email_verified"]
	if !ok {
		return claimError("email_verified", ClaimMissing)
	}
	trimmed := bytes.TrimSpace(raw)
	if !bytes.Equal(trimmed, []byte("true")) &&
		!bytes.Equal(trimmed, []byte("false")) {
		return claimError("email_verified", ClaimWrongType)
	}
	if !allowUnverifiedEmail && bytes.Equal(trimmed, []byte("false")) {
		return claimError("email_verified", ClaimUnverified)
	}
	return nil
}

func optionalName(
	claims map[string]json.RawMessage,
	fallback string,
) (string, error) {
	raw, ok := claims["name"]
	if !ok {
		return fallback, nil
	}
	var name string
	if !isJSONString(raw) || json.Unmarshal(raw, &name) != nil {
		return "", claimError("name", ClaimWrongType)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fallback, nil
	}
	if len(name) > maxNameLength {
		return "", claimError("name", ClaimInvalid)
	}
	return name, nil
}

func requiredGroups(
	claims map[string]json.RawMessage,
	field string,
) ([]string, error) {
	raw, ok := claims[field]
	if !ok {
		return nil, claimError(field, ClaimMissing)
	}
	var rawGroups []json.RawMessage
	if err := json.Unmarshal(raw, &rawGroups); err != nil || rawGroups == nil {
		return nil, claimError(field, ClaimWrongType)
	}
	var groups []string
	for _, rawGroup := range rawGroups {
		var group string
		if !isJSONString(rawGroup) || json.Unmarshal(rawGroup, &group) != nil {
			return nil, claimError(field, ClaimWrongType)
		}
		groups = append(groups, group)
	}
	return groups, nil
}

func validSubject(subject string) bool {
	if subject == "" || len(subject) > maxSubjectLength {
		return false
	}
	for i := range len(subject) {
		if subject[i] > 0x7f {
			return false
		}
	}
	return true
}

func validEmail(email string) bool {
	if email == "" || len(email) > maxEmailLength {
		return false
	}
	parsed, err := mail.ParseAddress(email)
	return err == nil && parsed.Address == email
}

func claimError(field string, reason ClaimErrorReason) error {
	return &ClaimError{Field: field, Reason: reason}
}

func isJSONString(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) >= 2 && trimmed[0] == '"' &&
		trimmed[len(trimmed)-1] == '"'
}
