# Radio

cliamp ships with the [Radio Browser](https://www.radio-browser.info/) directory: about 58,000 internet radio stations across 241 countries. Press `R` in the player to open it.

A list that long is only useful if you can cut it down. The main way to do that is by location.

## Quick start

| Do this | To get |
| --- | --- |
| `R`, then `Enter` on "Browse all countries" | The country list, your own regions first |
| `/` in the country list | Filter it: type `japan`, `europe`, `oslo` |
| `f` on a country | Pin it, so it gets its own row in the radio pane |
| `Enter` on a country row | That country's stations as a playlist |
| `Enter` on "Use my location" | Offers to work out your country, once, and only if you say yes |

## The radio pane

```
── Countries ──────────────────────
  Browse all countries
  Norway (near you)
  ★ Germany
  ★ Japan
── Stations ───────────────────────
  cliamp radio
── Favorites ──────────────────────
  ★ Radio Paradise [320k] · United States
── Catalog ────────────────────────
  MANGORADIO [128k] · Germany
  Dance Wave! [128k] · Hungary
```

"Norway (near you)" appears only once you have said yes to [using your location](#using-your-location). Starred rows are places you pinned. Selecting any of them loads that country's or region's stations as a playlist, so `>` and `<` scan between stations of one place.

## Browsing by country

"Browse all countries" opens the country list. Each row shows the country, its station count, and its region:

```
☆ Innlandet (16) — Norway
☆ Oslo (11) — Norway
☆ United States Of America (7975) — Americas
★ Germany (6235) — Europe
☆ Japan (215) — Asia
```

Regions of your own country come first, grouped under its name. Only about 44% of stations record a region at all, so cliamp fetches them for your home country only, where they are worth the request.

`/` filters the list on both the name and the region, so `asia` narrows to Asian countries and `oslo` finds one Norwegian region.

Pick a country and choose how to order its stations:

| Order | Sorts by |
| --- | --- |
| Most Voted | Directory votes, all-time |
| Most Listened | Total clicks |
| Trending | Recent click growth |
| By Name | Station name, A to Z |
| Random | Nothing; a different set each time |

The result replaces your playlist with up to 200 stations from that place.

### Pinning

`f` on a country or region pins it. Pinned places get their own row in the radio pane under "Countries", so the places you listen to are one keypress from anywhere. They are stored in `~/.config/cliamp/radio_countries.toml`:

```toml
[[country]]
code = "DE"
name = "Germany"

[[country]]
code = "NO"
name = "Oslo, Norway"
state = "Oslo"
```

## Using your location

Until you ask for it, cliamp does not work out where you are. A fresh install
shows one row instead:

```
── Countries ──────────────────────
  Browse all countries
  Use my location
```

Selecting it asks:

> Use your country to suggest nearby radio? cliamp would read it from your
> system timezone. Nothing is sent to a location service.

Answer with `y` or `n`. Either answer is remembered, so you are asked once and
the row goes away. Saying yes writes your country to `[radio] country` and adds
a "near you" row for it; saying no writes `country = "none"` and changes
nothing else.

Nothing else in cliamp reads your location, and no other feature is gated on
this. The country browser, pinning, and the catalog all work the same whether
you say yes or no; a yes only saves you finding your own country in a list of
250.

### How it is worked out

Only after you say yes, and never over the network:

1. The system timezone: `TZ`, then the `/etc/localtime` symlink, then `/etc/timezone`.
2. The locale: `LC_ALL`, then `LC_MESSAGES`, then `LANG`.

The timezone is checked before the locale on purpose. A desktop left on
`en_US.UTF-8` while sitting in `Europe/Oslo` is the common case, not the
exception, and the timezone is the setting people keep accurate.

There is no geo-IP lookup. That would be more precise and would also mean
telling a third party where you live every launch, which is not a trade this
feature is worth.

### Setting it yourself

Skip the question entirely with an ISO 3166-1 alpha-2 code:

```toml
[radio]
country = "NO"
```

Or turn it off for good:

```toml
[radio]
country = "none"
```

To be asked again, delete the `country` line.

## Why not "stations near me"

The directory records coordinates for stations, and it would be reasonable to expect a radius search. cliamp does not offer one, because the data does not support it:

| Field | Coverage (1000 top stations) |
| --- | --- |
| `countrycode` | 98.7% |
| `state` | 44.4% |
| `geo_lat` / `geo_long` | 11.9% |

A 200 km radius search would silently hide about 88% of the catalog. The API also ignores distance ordering, so what came back would not even be sorted by how close it is. Country is the axis the data actually supports, so that is the one cliamp uses.

## Custom stations

Stations you add yourself live in `~/.config/cliamp/radios.toml` and appear under "Stations" alongside the built-in cliamp radio. See [configuration.md](configuration.md#custom-radio-stations).

Unlike directory entries, these are not filtered to `http` and `https`. That file is yours, and it carries the same trust as anything else you type.

## Keys

| Key | Action |
| --- | --- |
| `R` | Open the radio provider |
| `/` | Search station names through the directory (Enter runs it, Esc clears) |
| `f` | Favorite the selected station, or pin the selected country |
| `N` | Open the country browser |
| `i` | Show cliamp radio listener statistics |
| `Ctrl+R` | Refresh: re-fetch the country list and the catalog |

See [keybindings.md](keybindings.md) for the rest.

## Track info

For live radio, cliamp shows the current track from inline ICY metadata (`StreamTitle`). See [streaming.md](streaming.md#track-info) for the stations that publish now-playing data through an API instead.

## Run your own station

Run an internet radio station with [cliamp-server](https://github.com/bjarneo/cliamp-server). Point it at a directory of audio files to start broadcasting. It supports multiple stations, live metadata, and on-the-fly transcoding.
