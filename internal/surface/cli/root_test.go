package cli

import "testing"

// TestPersistentPreRunEValidatesTimezone ensures --timezone fails fast with a
// clear error instead of silently degrading stealth when the IANA zone name
// is invalid (time.LoadLocation is the same check the JS Intl timezone
// override ultimately depends on).
func TestPersistentPreRunEValidatesTimezone(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()

	oldTimezone, oldFormat, oldProfile := flagTimezone, flagFormat, flagProfile
	oldPolicy, oldAllowedDomains, oldHuman := flagPolicy, flagAllowedDomains, flagHuman
	defer func() {
		flagTimezone, flagFormat, flagProfile = oldTimezone, oldFormat, oldProfile
		flagPolicy, flagAllowedDomains, flagHuman = oldPolicy, oldAllowedDomains, oldHuman
	}()

	flagFormat = "text"
	flagProfile = "auto"
	flagPolicy = ""
	flagAllowedDomains = ""

	flagTimezone = "Definitely/Not_A_Real_Zone"
	if err := rootCmd.PersistentPreRunE(rootCmd, nil); err == nil {
		t.Fatal("expected an error for an invalid --timezone")
	}

	flagTimezone = "Europe/Paris"
	if err := rootCmd.PersistentPreRunE(rootCmd, nil); err != nil {
		t.Fatalf("expected a valid IANA timezone to pass, got %v", err)
	}

	flagTimezone = ""
	if err := rootCmd.PersistentPreRunE(rootCmd, nil); err != nil {
		t.Fatalf("expected an unset --timezone (locale-derived default) to pass, got %v", err)
	}
}
