// Package qobuz implements a cliamp music provider for Qobuz.
//
// It authenticates via the interactive OAuth browser flow, scrapes the
// app_id / signing secrets / OAuth private key from the Qobuz web player
// bundle.js, and resolves streams through Qobuz's session-based qbz-1 CMAF
// API. Encrypted segments are decrypted and reconstructed into FLAC bytes
// before they are passed to cliamp's ffmpeg pipeline.
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
