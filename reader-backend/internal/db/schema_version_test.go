package db

import "testing"

func TestRequiredSchemaVersion(t *testing.T) {
	if RequiredSchemaVersion != 2026111940 {
		t.Fatalf("RequiredSchemaVersion = %d, want 2026111940", RequiredSchemaVersion)
	}
}
