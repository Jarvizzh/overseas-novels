package db

import "testing"

func TestRequiredSchemaVersion(t *testing.T) {
	if RequiredSchemaVersion != 2026111936 {
		t.Fatalf("RequiredSchemaVersion = %d, want 2026111936", RequiredSchemaVersion)
	}
}
