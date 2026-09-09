# Podcasts

The **Podcasts** provider is always available. No `enabled` setting, API key,
or account is required. Press `Shift+O` (`O`) in the player to open it, or start
directly from the CLI:

```sh
cliamp --provider podcast
```

To open it by default, set the top-level `provider = "podcast"` in
`config.toml`.

## Discover Shows

- **Top Shows (US)** lists Apple's top 100 chart in ranking order, omitting duplicate feeds and shows Apple cannot resolve to a feed.
- In the main Podcasts provider list, press `/`, type a show name, and press `Enter` to search Apple for up to 100 shows. Typing alone does not send search requests. `Esc` clears the search and restores discovery and subscriptions.
- Open **Browse Categories**, then choose a **Genre**, then a **Show**. The 19 categories use Apple's genre-name search, not genre charts. Inside these lists, `/` filters the visible entries.

Press `Enter` on a show in the provider list or category browser to replace the
main playlist with its episodes, without starting playback. Then select an
episode and press `Enter` to play, or `a` to toggle its play-next queue entry.
`Esc` or `b` returns from the playlist to the provider list.

Feeds load the first 300 playable episodes in feed order. Episode titles, show
names, durations, artwork, and episode numbers are retained when available.
Items without playable audio are skipped.

`Ctrl+R` reloads a show opened from the provider list or category browser. In
the provider list with no show open, it refreshes the top chart. Reopen a show
from `Ctrl+F` results to fetch that feed again without replacing a mixed queue.

## Search Overlay

With Podcasts active, `Ctrl+F` searches for shows as collections, not individual
episodes. Type a query and press `Enter`; this overlay shows up to 20 results.
Its actions differ from opening a show in the main provider list:

| Key | Action on a show result |
| --- | --- |
| `Enter` | Append the feed's episodes to the playlist and start its first episode |
| `a` | Append the feed's episodes; start its first episode if the playlist was empty or nothing is playing |
| `q` | Append the feed's episodes and queue them in feed order, after any already queued tracks; start queued playback if nothing is playing |
| `f` | Subscribe or unsubscribe |
| `Esc` | Return to the search input; press again to close |

## Subscriptions

Press `f` on a show in the provider list, category show list, or `Ctrl+F` results
to subscribe or unsubscribe. Subscribed shows appear under **Subscriptions**
and are marked `[subscribed]` in the provider list. On an episode in the main
playlist, `f` still toggles a bookmark, not a subscription.

Subscriptions are saved atomically in `podcast_subscriptions.json` in the
[config directory](configuration.md#config-directory), normally
`~/.config/cliamp/podcast_subscriptions.json`. They remain available when Apple's
directory is offline, but fetching feeds and playing episodes still requires
access to the publisher. This provider has no offline episode download cache
or per-episode progress, resume, or played-state tracking.

## Publisher RSS URLs

In the main Podcasts `/` search, type a publisher's RSS URL and press `Enter`.
cliamp fetches the feed directly without searching Apple, including URLs with
no `.xml` or `.rss` extension. A feed becomes a show result only if it contains
at least one playable episode. Press `f` to subscribe, or `Enter` to load it.

Existing RSS URL playback from the CLI or the `u` URL prompt still works:

```sh
cliamp https://example.com/podcast/feed.xml
```

See [Streaming](streaming.md#podcasts) and
[RSS feed playlists](playlists.md#podcast--rss-feed-playlists).

## Chart Country

Optionally select another country's top chart in `config.toml`:

```toml
[podcast]
country = "no"
```

`country` is a two-letter country code, defaulting to `"us"`. cliamp does not
detect your location. It affects charts only, not show searches, categories,
subscriptions, or publisher feeds.
