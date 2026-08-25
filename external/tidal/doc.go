// Package tidal implements a cliamp provider for the Tidal streaming service.
//
// It uses Tidal's private client API (api.tidal.com/v1) — the same API the
// python-tidal ecosystem uses — because the official developer API only
// allows 30-second previews for third-party clients. Sign-in is an OAuth 2.0
// device flow: the user opens link.tidal.com and enters a short code, which
// fits the TUI without a local redirect server.
//
// Playback resolves a per-track stream URL via the playbackinfopostpaywall
// endpoint. LOW/HIGH/LOSSLESS qualities are delivered as direct CDN URLs
// (Tidal's "BTS" manifest) and routed through the player's buffered ffmpeg
// pipeline, exactly like Qobuz streams. HI_RES_LOSSLESS is DASH-segmented,
// which the pipeline cannot stream yet, so it falls back per track to
// LOSSLESS (FLAC 16-bit/44.1kHz).
package tidal
