package auth

import (
	"errors"
	"reflect"
	"testing"
)

func TestPermissionRegistryIntegrity(t *testing.T) {
	catalog := PermissionCatalog()
	if len(catalog.Groups) != 7 {
		t.Fatalf("expected 7 nav groups, got %d", len(catalog.Groups))
	}

	codes := AllPermissionCodes()
	if len(codes) != 39 {
		t.Fatalf("expected 39 permission codes, got %d", len(codes))
	}

	// Verify exact super_admin_only flags.
	for _, code := range codes {
		wantSuperOnly := code == PermSystemAccountsWrite || code == PermSystemRolesWrite
		if got := IsSuperAdminOnlyPermission(code); got != wantSuperOnly {
			t.Fatalf("permission %s super_admin_only=%v, want %v", code, got, wantSuperOnly)
		}
	}

	// Verify catalog returns a defensive copy.
	catalog.Groups[0].Label = "mutated"
	catalog.Groups[0].Modules[0].Read.Code = "mutated"
	fresh := PermissionCatalog()
	if fresh.Groups[0].Label != "首页总览" || fresh.Groups[0].Modules[0].Read.Code != PermDashboardRead {
		t.Fatal("PermissionCatalog returned shared mutable references")
	}
}

func TestNormalizePermissions(t *testing.T) {
	got, err := NormalizePermissions([]string{
		PermPaymentsRefundsWrite,
		PermDashboardRead,
		PermAdsPixelsWrite,
		PermPaymentsRefundsWrite, // duplicate
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		PermDashboardRead,
		PermPaymentsRefundsRead,
		PermPaymentsRefundsWrite,
		PermAdsPixelsRead,
		PermAdsPixelsWrite,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizePermissions() = %v, want %v", got, want)
	}

	empty, err := NormalizePermissions(nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("expected non-nil empty slice, got %#v, err=%v", empty, err)
	}

	if _, err := NormalizePermissions([]string{"unknown.module:read"}); !errors.Is(err, ErrInvalidPermission) {
		t.Fatalf("expected ErrInvalidPermission, got %v", err)
	}
}

func TestValidateAssignablePermissions(t *testing.T) {
	// system.accounts:read and system.roles:read are assignable to non-super-admin roles.
	got, err := ValidateAssignablePermissions([]string{PermSystemAccountsRead, PermSystemRolesRead})
	if err != nil {
		t.Fatalf("expected read permissions to be assignable, got: %v", err)
	}
	want := []string{PermSystemAccountsRead, PermSystemRolesRead}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	// system.accounts:write and system.roles:write must be rejected.
	for _, forbidden := range []string{PermSystemAccountsWrite, PermSystemRolesWrite} {
		_, err := ValidateAssignablePermissions([]string{PermDashboardRead, forbidden})
		if !errors.Is(err, ErrSuperAdminOnlyPermission) {
			t.Fatalf("expected ErrSuperAdminOnlyPermission for %s, got %v", forbidden, err)
		}
	}
}
