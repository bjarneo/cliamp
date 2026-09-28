package qobuz

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// bundleBaseURL is the Qobuz web player origin that ships the JS bundle.
const bundleBaseURL = "https://play.qobuz.com"

// fallbackPrivateKey is the static OAuth code-exchange private_key documented
// for the production environment. It is used only when the value cannot be
// scraped from the bundle (the in-bundle name has changed across releases).
// Source: SofusA/qobine qobuz-api.md reverse-engineering notes.
const fallbackPrivateKey = "6lz8C03UDIC7"

// Regexes that scrape the app_id, signing secrets and OAuth private key from
// the Qobuz web player's bundle.js. Adapted from DashLt's spoofbuz (via the
// qobuz-dl-go project) and cross-checked against the SofusA/qobine
// reverse-engineered Qobuz API reference (qobuz-api.md). Qobuz has shipped
// several bundle formats over time, so the private key has multiple candidate
// patterns.
var (
	reSeedTimezone = regexp.MustCompile(
		`[a-z]\.initialSeed\("(?P<seed>[\w=]+)",window\.utimezone\.(?P<timezone>[a-z]+)\)`,
	)
	reAppID = regexp.MustCompile(
		`production:{api:{appId:"(?P<app_id>\d{9})",appSecret:"\w{32}"`,
	)
	rePrivateKeyPatterns = []*regexp.Regexp{
		regexp.MustCompile(`privateKey:\s*"(?P<key>[A-Za-z0-9+/=_\-]{6,128})"`),
		regexp.MustCompile(`private_key:\s*"(?P<key>[A-Za-z0-9+/=_\-]{6,128})"`),
		regexp.MustCompile(`oauthKey:\s*"(?P<key>[A-Za-z0-9+/=_\-]{6,128})"`),
		regexp.MustCompile(`clientSecret:\s*"(?P<key>[A-Za-z0-9+/=_\-]{6,128})"`),
	}
	reBundleURL = regexp.MustCompile(
		`<script src="(/resources/\d+\.\d+\.\d+-[a-z]\d{3}/bundle\.js)"></script>`,
	)
)

// bundle holds the scraped Qobuz web player JavaScript bundle.
type bundle struct {
	content string
}

// fetchBundle downloads the Qobuz login page and its bundle.js. ctx cancels the
// requests.
func fetchBundle(ctx context.Context) (*bundle, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bundleBaseURL+"/login", nil)
	if err != nil {
		return nil, fmt.Errorf("qobuz: build login request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qobuz: get login page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qobuz: get login page: HTTP %s", resp.Status)
	}
	page, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("qobuz: read login page: %w", err)
	}

	match := reBundleURL.FindSubmatch(page)
	if match == nil {
		return nil, fmt.Errorf("qobuz: bundle URL not found in login page")
	}
	bundlePath := string(match[1])

	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, bundleBaseURL+bundlePath, nil)
	if err != nil {
		return nil, fmt.Errorf("qobuz: build bundle request: %w", err)
	}
	resp2, err := client.Do(req2)
	if err != nil {
		return nil, fmt.Errorf("qobuz: get bundle.js: %w", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qobuz: get bundle.js: HTTP %s", resp2.Status)
	}
	body, err := io.ReadAll(resp2.Body)
	if err != nil {
		return nil, fmt.Errorf("qobuz: read bundle.js: %w", err)
	}

	return &bundle{content: string(body)}, nil
}

// appID extracts the Qobuz application ID from the bundle.
func (b *bundle) appID() (string, error) {
	m := reAppID.FindStringSubmatch(b.content)
	if m == nil {
		return "", fmt.Errorf("qobuz: app_id not found in bundle")
	}

	return m[reAppID.SubexpIndex("app_id")], nil
}

// privateKey extracts the OAuth private key, falling back to the documented
// production value when the bundle pattern cannot be matched.
func (b *bundle) privateKey() string {
	for _, re := range rePrivateKeyPatterns {
		if m := re.FindStringSubmatch(b.content); m != nil {
			return m[re.SubexpIndex("key")]
		}
	}
	return fallbackPrivateKey
}

// capitalizeFirst upper-cases the first byte of s (timezone names are ASCII).
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// secrets extracts the API signing secrets from the bundle. The result maps a
// timezone name to its decoded secret; callers try each until one validates.
func (b *bundle) secrets() (map[string]string, error) {
	seeds := make(map[string][]string)

	for _, m := range reSeedTimezone.FindAllStringSubmatch(b.content, -1) {
		seed := m[reSeedTimezone.SubexpIndex("seed")]
		tz := m[reSeedTimezone.SubexpIndex("timezone")]
		seeds[tz] = append(seeds[tz], seed)
	}
	if len(seeds) == 0 {
		return nil, fmt.Errorf("qobuz: no seeds found in bundle")
	}

	// Replicate the Python OrderedDict + move_to_end ordering used by spoofbuz.
	tzList := make([]string, 0, len(seeds))
	for tz := range seeds {
		tzList = append(tzList, tz)
	}
	if len(tzList) >= 2 {
		tzList[0], tzList[1] = tzList[1], tzList[0]
	}

	capitalised := make([]string, len(tzList))
	for i, tz := range tzList {
		capitalised[i] = capitalizeFirst(tz)
	}
	reInfoExtras := regexp.MustCompile(
		`name:"\w+/(?P<timezone>` + strings.Join(capitalised, "|") + `)",info:"(?P<info>[\w=]+)",extras:"(?P<extras>[\w=]+)"`,
	)

	for _, m := range reInfoExtras.FindAllStringSubmatch(b.content, -1) {
		tz := strings.ToLower(m[reInfoExtras.SubexpIndex("timezone")])
		info := m[reInfoExtras.SubexpIndex("info")]
		extras := m[reInfoExtras.SubexpIndex("extras")]
		seeds[tz] = append(seeds[tz], info, extras)
	}

	secrets := make(map[string]string, len(seeds))
	for tz, parts := range seeds {
		joined := strings.Join(parts, "")
		if len(joined) <= 44 {
			continue
		}
		trimmed := joined[:len(joined)-44]
		// Pad to a multiple of 4 so StdEncoding accepts it (Python's b64decode
		// pads automatically).
		padded := trimmed + strings.Repeat("=", (4-len(trimmed)%4)%4)
		decoded, err := base64.StdEncoding.DecodeString(padded)
		if err != nil {
			continue
		}
		secrets[tz] = string(decoded)
	}
	if len(secrets) == 0 {
		return nil, fmt.Errorf("qobuz: no secrets decoded from bundle")
	}
	return secrets, nil
}

// scrapeCredentials fetches the bundle and returns the app_id, the list of
// candidate signing secrets, and the OAuth private key.
func scrapeCredentials(ctx context.Context) (string, []string, string, error) {
	b, err := fetchBundle(ctx)
	if err != nil {
		return "", nil, "", err
	}
	appID, err := b.appID()
	if err != nil {
		return "", nil, "", err
	}
	secretMap, err := b.secrets()
	if err != nil {
		return "", nil, "", err
	}
	secrets := make([]string, 0, len(secretMap))
	for _, s := range secretMap {
		if s != "" {
			secrets = append(secrets, s)
		}
	}
	return appID, secrets, b.privateKey(), nil
}
