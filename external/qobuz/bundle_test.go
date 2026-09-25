package qobuz

import (
	"strings"
	"testing"
)

func TestBundlePrivateKeyScraped(t *testing.T) {
	b := &bundle{content: `foo privateKey: "scrapedKey123" bar`}
	// A configured fallback must not shadow the scraped value.
	got, err := b.privateKey("fromConfig456")
	if err != nil {
		t.Fatalf("privateKey() error = %v", err)
	}
	if got != "scrapedKey123" {
		t.Fatalf("privateKey() = %q, want scraped value %q", got, "scrapedKey123")
	}
}

func TestBundlePrivateKeyConfigFallback(t *testing.T) {
	b := &bundle{content: `no key here at all`}
	got, err := b.privateKey("fromConfig456")
	if err != nil {
		t.Fatalf("privateKey() error = %v", err)
	}
	if got != "fromConfig456" {
		t.Fatalf("privateKey() = %q, want fallback %q", got, "fromConfig456")
	}
}

func TestBundlePrivateKeyMissing(t *testing.T) {
	b := &bundle{content: `no key here at all`}
	_, err := b.privateKey("")
	if err == nil {
		t.Fatal("privateKey() = nil error, want error when nothing scraped and no fallback")
	}
	// The error must tell the user which config key to set and where the
	// value comes from.
	for _, want := range []string{"[qobuz] private_key", "play.qobuz.com"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestBundleAppID(t *testing.T) {
	b := &bundle{content: `x=production:{api:{appId:"798273057",appSecret:"05a4851e74ee47fda346f50cfdfc4f09"}}`}
	got, err := b.appID()
	if err != nil {
		t.Fatalf("appID() error = %v", err)
	}
	if got != "798273057" {
		t.Fatalf("appID() = %q, want 798273057", got)
	}
}
