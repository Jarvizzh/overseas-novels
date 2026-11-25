package main

import (
	"context"
	"strings"
	"testing"

	"conversion-backend/internal/delivery"
)

func TestOperatorForCommand(t *testing.T) {
	retention := operatorForCommand("retention", "root-1")
	if retention.ID != "root-1" || !retention.RequireSuperAdmin || retention.Permission != "" {
		t.Fatalf("retention must strictly require SuperAdmin: %+v", retention)
	}

	for _, cmd := range []string{
		"review-legacy-environment",
		"repair-missing",
		"retry-delivery",
		"retry-partner-report",
		"append-target",
	} {
		op := operatorForCommand(cmd, "optimizer-1")
		if op.ID != "optimizer-1" || op.RequireSuperAdmin || op.Permission != delivery.PermissionAdsConversionsWrite {
			t.Fatalf("command %s must require %s without SuperAdmin-only lock: %+v", cmd, delivery.PermissionAdsConversionsWrite, op)
		}
	}
}

func TestRunRequiresCommandAndValidEnv(t *testing.T) {
	if err := run(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "usage: conversion-ops") {
		t.Fatalf("expected usage error, got %v", err)
	}
	t.Setenv("DATABASE_URL", "")
	t.Setenv("CONVERSION_ENVIRONMENT", "")
	if err := run(context.Background(), []string{"retention", "--apply", "--operator=root", "--reason=test"}); err == nil ||
		!strings.Contains(err.Error(), "DATABASE_URL and CONVERSION_ENVIRONMENT are required") {
		t.Fatalf("expected missing env error, got %v", err)
	}
}
