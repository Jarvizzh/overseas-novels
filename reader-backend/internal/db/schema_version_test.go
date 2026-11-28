package db

import "testing"

func TestRequiredSchemaVersion(t *testing.T) {
	if RequiredSchemaVersion != 2026112802 {
		t.Fatalf("RequiredSchemaVersion = %d, want 2026112802", RequiredSchemaVersion)
	}
}
