# Themes

cliamp ships with 21 contrast-checked color themes and supports custom themes via simple TOML files.

Press `t` during playback to open the theme picker. Navigate with `↑`/`↓`, preview live as you move, confirm with `Enter`, or cancel with `Esc`.

Your selection is saved automatically and restored on next launch.

## Built-in themes

ayu-mirage-dark, catppuccin, catppuccin-latte, dracula, ember, ethereal, everforest, flexoki-light, gruvbox, hackerman, kanagawa, matte-black, miasma, neon-blade-runner, nord, osaka-jade, ristretto, rose-pine, tokyo-night, vantablack, winamp

## Creating a custom theme

Create a `.toml` file in `~/.config/cliamp/themes/`:

```
mkdir -p ~/.config/cliamp/themes
```

Each file needs all 6 foreground colors as `#RRGGBB` hex values. Add `bg` to set
a matching background; omit it to keep your terminal background. Incomplete or
malformed custom themes are ignored. The filename (minus `.toml`) becomes the
theme name.

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

That's it. Press `t` and your theme appears in the list immediately.

### Color reference

| Key         | What it colors                              |
|-------------|---------------------------------------------|
| `bg`        | Optional application background             |
| `accent`    | Title, track name, seek bar, selected items |
| `bright_fg` | Primary text and time display               |
| `fg`        | Muted text, help bar, inactive elements     |
| `green`     | Playing, success, volume, spectrum low      |
| `yellow`    | Warnings and spectrum middle               |
| `red`       | Errors and spectrum top                    |

All values are six-digit hex strings (for example, `"#ff5733"`). Help-key
pill text automatically switches between black and white for readable contrast.

Important UI states also use stable text markers such as `>`, `Q`, `★`, `!`,
`WARN:`, and `ERR:`, so state and feedback remain distinguishable in monochrome
terminals.

## Overriding a built-in theme

If your custom file has the same name as a built-in theme, yours takes priority. For example, creating `~/.config/cliamp/themes/catppuccin.toml` replaces the built-in catppuccin.

## Setting a default theme

Add a `theme` line to `~/.config/cliamp/config.toml`:

```toml
theme = "catppuccin"
```

Use the filename without `.toml`. Leave empty or omit for terminal default colors.
