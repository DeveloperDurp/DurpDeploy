package oidc

import (
	"errors"
	"testing"
)

func TestMapRole(t *testing.T) {
	tests := []struct {
		name     string
		groups   []string
		wantRole Role
		wantErr  ClaimErrorReason
	}{
		{name: "viewer", groups: []string{"durp-viewer"}, wantRole: RoleViewer},
		{
			name:     "deployer beats viewer",
			groups:   []string{"durp-viewer", "durp-deployer"},
			wantRole: RoleDeployer,
		},
		{
			name:     "admin beats all",
			groups:   []string{"durp-viewer", "durp-admin", "durp-deployer"},
			wantRole: RoleAdmin,
		},
		{
			name:     "duplicate group remains mapped",
			groups:   []string{"durp-viewer", "durp-viewer"},
			wantRole: RoleViewer,
		},
		{name: "unmapped", groups: []string{"other"}, wantErr: ClaimUnmapped},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			got, err := MapRole(tt.groups, testGroupMapping)

			// Then
			if tt.wantErr != "" {
				var claimErr *ClaimError
				if !errors.As(err, &claimErr) || claimErr.Reason != tt.wantErr {
					t.Fatalf(
						"MapRole() error = %v, want reason %q",
						err,
						tt.wantErr,
					)
				}
				return
			}
			if err != nil {
				t.Fatalf("MapRole() error = %v", err)
			}
			if got != tt.wantRole {
				t.Fatalf("MapRole() = %q, want %q", got, tt.wantRole)
			}
		})
	}
}
