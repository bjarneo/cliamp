# Themes

cliamp ships with 20 built-in color themes and supports custom themes via simple TOML files.

Press `t` during playback to open the theme picker. Navigate with `↑`/`↓`, preview live as you move, confirm with `Enter`, or cancel with `Esc`.

Your selection is saved automatically and restored on next launch.

## Built-in themes

ayu-mirage-dark, catppuccin, catppuccin-latte, ethereal, everforest, flexoki-light, gruvbox, hackerman, kanagawa, matte-black, miasma, nord, osaka-jade, ristretto, rose-pine, tokyo-night, vantablack

## Creating a custom theme

Create a `.toml` file in `~/.config/cliamp/themes/`:

```
mkdir -p ~/.config/cliamp/themes
```

Each file needs 6 hex color values. The filename (minus `.toml`) becomes the theme name.

### Example: `~/.config/cliamp/themes/dracula.toml`

```toml
accent = "#bd93f9"
bright_fg = "#f8f8f2"
fg = "#6272a4"
green = "#50fa7b"
yellow = "#f1fa8c"
red = "#ff5555"
```

That's it. Press `t` and your theme appears in the list immediately.

### Color reference

| Key         | What it colors                                    |
|-------------|---------------------------------------------------|
| `accent`    | Title, track name, seek bar, selected items       |
| `bright_fg` | Primary text, time display                        |
| `fg`        | Muted/secondary text, help bar, inactive elements |
| `green`     | Playing indicator, volume bar, spectrum low        |
| `yellow`    | Spectrum middle                                   |
| `red`       | Spectrum top, error messages                      |

All values are hex strings (e.g. `"#ff5733"` or `"#F00"`).

## Overriding a built-in theme

If your custom file has the same name as a built-in theme, yours takes priority. For example, creating `~/.config/cliamp/themes/catppuccin.toml` replaces the built-in catppuccin.

## Using Omarchy themes

If you use [Omarchy](https://github.com/nicholasgasior/omarchy), you can copy any `colors.toml` directly:

```
cp ~/.config/omarchy/themes/catppuccin/colors.toml ~/.config/cliamp/themes/my-catppuccin.toml
```

The file format is compatible. cliamp reads only the keys it needs and ignores the rest.

## Setting a default theme

Add a `theme` line to `~/.config/cliamp/config.toml`:

```toml
theme = "catppuccin"
```

Use the filename without `.toml`. Leave empty or omit for terminal default colors.
