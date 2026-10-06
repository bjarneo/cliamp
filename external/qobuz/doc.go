// Package qobuz implements a cliamp music provider for Qobuz.
//
// It authenticates via the interactive OAuth browser flow, scrapes the
// app_id / signing secrets / OAuth private key from the Qobuz web player
// bundle.js, and gives tracks qobuz://track/<id> paths. When a track starts,
// ResolveSource gets a signed CDN stream URL through the legacy
// track/getFileUrl endpoint. Those URLs are routed through cliamp's
// buffer-while-playing + ffmpeg pipeline (see the Buffered source that
// providers.go registers), the same path used by the Navidrome, Jellyfin,
// Emby and Plex providers.
//
// Source material consulted for the reverse-engineered API surface:
//
//   - Aeneaj/qobuz-dl-go: Go client (primary template for signing,
//     bundle scraping and OAuth).
//   - DashLt/spoofbuz: secret/seed extraction from bundle.js.
//   - SofusA/qobine, qobuz-player-controls/examples/qobuz-api.md: a
//     comprehensive reverse-engineered Qobuz API reference used to
//     cross-check signing, the OAuth flow, format IDs and the
//     legacy-vs-segmented (/file/url) streaming distinction.
package qobuz
