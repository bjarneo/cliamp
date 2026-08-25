package ytmusic

import "testing"

func TestClassificationCacheRejectsDifferentAccount(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	saveClassification("account-a", map[string]bool{"private": true})
	matching := loadClassification("account-a")
	if !matching["private"] {
		t.Fatal("matching classification cache was not loaded")
	}
	if mismatched := loadClassification("account-b"); mismatched != nil {
		t.Fatalf("classification cache crossed accounts: %v", mismatched)
	}
}
