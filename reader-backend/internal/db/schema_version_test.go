package db

import "testing"

func TestRequiredSchemaVersion(t *testing.T) {
	if RequiredSchemaVersion != 2026111942 {
		t.Fatalf("RequiredSchemaVersion = %d, want 2026111942", RequiredSchemaVersion)
	}
}
