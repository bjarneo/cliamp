# Themes

cliamp includes 22 color themes that pass contrast checks. You can add custom themes with TOML files.

Press `t` during playback to open the theme picker. Use `↑`/`↓` to navigate and preview each theme. Press `Enter` to select it or `Esc` to cancel.

cliamp saves your selection and restores it at the next start.

## Built-in themes

alucard, ayu-mirage-dark, catppuccin, catppuccin-latte, dracula, ember, ethereal, everforest, flexoki-light, gruvbox, hackerman, kanagawa, matte-black, miasma, neon-blade-runner, nord, osaka-jade, ristretto, rose-pine, tokyo-night, vantablack, winamp

## Creating a custom theme

Create a `.toml` file in `~/.config/cliamp/themes/`:

```
mkdir -p ~/.config/cliamp/themes
```

Each file needs all six foreground colors as `#RRGGBB` hex values. Add `bg` to
set a background. Omit it to keep the terminal background. The file name
without `.toml` is the theme name. The name `default` selects the terminal
colors, so you cannot use it for a custom theme.

cliamp skips an incomplete or malformed custom theme, and a theme named
`default`. It writes the file name and the reason to
`~/.config/cliamp/cliamp.log` once per run, for example:

```
time=2026-09-29T21:00:00.000+02:00 level=WARN msg="theme: skip mytheme.toml in /home/you/.config/cliamp/themes: theme \"mytheme\": accent must be #RRGGBB"
```

cliamp writes this line at the warn level. With `log_level = "error"`, the log
does not show it.

### Example: `~/.config/cliamp/themes/solarized.toml`

```toml
bg = "#002b36"
accent = "#268bd2"
bright_fg = "#eee8d5"
fg = "#839496"
green = "#859900"
yellow = "#b58900"
red = "#dc322f"
```

Press `t` to show the theme in the list immediately.

### Color reference

| Key         | Color use                                   |
|-------------|---------------------------------------------|
| `bg`        | Optional application background             |
| `accent`    | Title, track name, seek bar, selected items |
| `bright_fg` | Primary text and time display               |
| `fg`        | Muted text, help bar, inactive elements     |
| `green`     | Playing, success, volume, spectrum low      |
| `yellow`    | Warnings and spectrum middle               |
| `red`       | Errors, favorite heart, and spectrum top    |

All values are six-digit hex strings, for example `"#ff5733"`. Help-key pill
text switches between black and white for readable contrast.

A line that starts with `#` is a comment. A `#` after a value and a space also
starts a comment, for example `red = "#dc322f" # errors`.

Important UI states also use stable text markers: `>`, `Q`, `♥`, `!`, `WARN:`,
and `ERR:`. These markers keep state and feedback distinct in monochrome
terminals.

## Overriding a built-in theme

If a custom file has the same name as a built-in theme, the custom file takes
priority. For example, `~/.config/cliamp/themes/catppuccin.toml` replaces the
built-in catppuccin theme.

## Setting a default theme

Add a `theme` line to `~/.config/cliamp/config.toml`:

```toml
theme = "catppuccin"
```

Use the file name without `.toml`. Leave the value empty or omit it to use
terminal default colors.
