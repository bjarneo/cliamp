# Themes

cliamp ships with 20 built-in color themes and supports custom themes via simple TOML files.

Press `t` during playback to open the theme picker. Navigate with `↑`/`↓`, preview live as you move, confirm with `Enter`, or cancel with `Esc`.

Your selection is saved automatically and restored on next launch.

## Built-in themes

ayu-mirage-dark, catppuccin, catppuccin-latte, dracula, ember, ethereal, everforest, flexoki-light, gruvbox, hackerman, kanagawa, matte-black, miasma, neon-blade-runner, nord, osaka-jade, ristretto, rose-pine, tokyo-night, vantablack

## Creating a custom theme

Create a `.toml` file in `~/.config/cliamp/themes/`:

```
mkdir -p ~/.config/cliamp/themes
```

Each file needs 6 hex color values. The filename (minus `.toml`) becomes the theme name.

### Example: `~/.config/cliamp/themes/solarized.toml`

```toml
accent = "#268bd2"
bright_fg = "#eee8d5"
fg = "#839496"
green = "#859900"
yellow = "#b58900"
red = "#dc322f"
```

That's it. Press `t` and your theme appears in the list immediately.

### Color reference

| Key         | What it colors                                    |
|-------------|---------------------------------------------------|
| `accent`    | Title, track name, seek bar, selected items       |
| `bright_fg` | Primary text, time display, help key pill text     |
| `fg`        | Muted/secondary text, help bar, inactive elements, help key pill background |
| `green`     | Playing indicator, volume bar, spectrum low        |
| `yellow`    | Spectrum middle                                   |
| `red`       | Spectrum top, error messages                      |

All values are hex strings (e.g. `"#ff5733"` or `"#F00"`).

## Overriding a built-in theme

If your custom file has the same name as a built-in theme, yours takes priority. For example, creating `~/.config/cliamp/themes/catppuccin.toml` replaces the built-in catppuccin.

## Setting a default theme

Add a `theme` line to `~/.config/cliamp/config.toml`:

```toml
theme = "catppuccin"
```

Use the filename without `.toml`. Leave empty or omit for terminal default colors.
