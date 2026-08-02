package unityvhdxfixture

import "testing"

func TestUnityFailureClassificationIgnoresNonfatalLicenseMessages(t *testing.T) {
	log := `Access token is unavailable; failed to update
Successfully resolved entitlement details
License group: Unity Personal
Licensing is initialized`
	if code := classifyUnityFailureCode(log, 1); code == CodeUnityLicenseFailed {
		t.Fatalf("code=%q", code)
	}
}

func TestUnityFailureClassificationDetectsAssetDatabaseOpenFailure(t *testing.T) {
	log := "Licensing is initialized\nCannot open lmdb database Library/SourceAssetDB\nmdb_env_open failed\nCrash!!!"
	if code := classifyUnityFailureCode(log, 0xC0000005); code != CodeUnityAssetDatabaseOpenFailed {
		t.Fatalf("code=%q", code)
	}
}

func TestUnityFailureClassificationDetectsNativeCrash(t *testing.T) {
	if code := classifyUnityFailureCode("Crash!!!", 0xC0000005); code != CodeUnityNativeCrash {
		t.Fatalf("code=%q", code)
	}
}

func TestUnityFailureClassificationUsesTerminalLicenseFailure(t *testing.T) {
	if code := classifyUnityFailureCode("Licensing failed to initialize", 1); code != CodeUnityLicenseFailed {
		t.Fatalf("code=%q", code)
	}
}
