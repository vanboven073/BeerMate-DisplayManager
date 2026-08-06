# Branding assets

Every asset here is derived from the official BeerMate brandbook
(`BeerMate_Brandbook_FINAL.pdf`). Logos and the favicon are rebuilt directly from
the brandbook's vector geometry rather than approximated.

## Asset inventory

### Logos — `assets/brand/logo/`

| File | Format | Use | Light/dark |
|---|---|---|---|
| `beermate-mark-full.svg` | SVG (2 KB) | The B mark: orange glass + navy B | On light |
| `beermate-mark-light.svg` | SVG | Orange glass + ivory B | On dark |
| `beermate-mark-mono-navy.svg` | SVG | Single-colour navy | On light |
| `beermate-mark-mono-ivory.svg` | SVG | Single-colour ivory | On dark |
| `beermate-wordmark-full.svg` | SVG (8 KB) | "BeerMate." wordmark, navy + orange dot | On light |
| `beermate-wordmark-light.svg` | SVG | Wordmark, ivory + orange dot | On dark |
| `beermate-wordmark-mono-ivory.svg` | SVG | Single-colour ivory | On dark |
| `beermate-wordmark-mono-navy.svg` | SVG | Single-colour navy | On light |
| `beermate-favicon.svg` | SVG | Navy rounded-square app icon with the mark | Either |
| `beermate-icon-orange.svg` | SVG | Orange rounded-square variant | Either |

The SVGs are tight-viewBox, use correct even-odd knockouts (the foam wave and the
B counters are transparent), and carry a `<title>` for accessibility.

### Icons — `assets/brand/icons/`

`favicon.ico` (16/32/48), `favicon-{16,32,48,64,128,180,192,256,512}.png`,
`apple-touch-icon.png`. All rendered from `beermate-favicon.svg`.

### Fonts — `assets/brand/fonts/`

| File | Family | Role (brandbook 4.x) |
|---|---|---|
| `PlusJakartaSans-Variable-latin.woff2` | Plus Jakarta Sans | Headings, titles, prominent text |
| `PlusJakartaSans-Variable-latin-ext.woff2` | Plus Jakarta Sans | Extended latin |
| `Epilogue-Variable-latin.woff2` | Epilogue | Body / running text |
| `Epilogue-Variable-latin-ext.woff2` | Epilogue | Extended latin |

Both are variable WOFF2 (~117 KB total). They are served locally — no Google
Fonts or CDN — and preloaded in `index.html` so the first paint is in-brand.

## Font licensing

Plus Jakarta Sans and Epilogue are both under the **SIL Open Font License 1.1**.
The licence permits bundling and redistribution with the application. The licence
text ships alongside the fonts:

- `assets/brand/fonts/OFL-PlusJakartaSans.txt`
- `assets/brand/fonts/OFL-Epilogue.txt`

No missing font files: the required weights are covered by the variable fonts
(weight axis 200–800 for Plus Jakarta Sans, 100–900 for Epilogue). The CSS
fallback stack is `"Plus Jakarta Sans", Inter, Arial, Helvetica, sans-serif`.

## Colour tokens (brandbook 3.1)

| Token | Hex | RGB | Name |
|---|---|---|---|
| `--bm-navy` | `#0E1821` | 14/24/33 | Midnight Navy |
| `--bm-navy-60` | `#1A2C3C` | 26/44/60 | Midnight Navy (light) |
| `--bm-ivory` | `#F6F1EF` | 246/241/239 | Soft Ivory |
| `--bm-white` | `#FFFFFF` | 255/255/255 | White |
| `--bm-orange` | `#E86514` | 232/101/20 | Burnt Orange (signature) |
| `--bm-gold` | `#FCC010` | 252/192/16 | Goudgeel (gradient accent, used sparingly) |

Status colours sit beside the palette and are always paired with an icon or text
label so meaning never rests on colour alone.

## Typography tokens (brandbook 4.5)

| Style | Weight | Size | Line height |
|---|---|---|---|
| Headline 1 | ExtraBold (800) | 52 px | 60 |
| Headline 2 | Bold (700) | 48 px | 60 |
| Headline 3 | Bold (700) | 26 px | 33 |
| Headline 4 | SemiBold (600) | 24 px | 30 |
| Body | Regular (400) | 18 px | 28 |

## Shape tokens (brandbook 5.3)

- Cards, images and text boxes: **radius 30** (`--bm-radius-card`).
- Buttons: **radius 10** (`--bm-radius-button`).
- The orange dot after the wordmark is a signature element; in the UI it is drawn
  with a pseudo-element and hidden from assistive technology.

## Brandbook restrictions honoured

- Only the two primaries (navy, ivory) and two secondaries (orange, gold); gold
  is used minimally, as specified.
- The player uses a dark (navy) canvas so orange reads as the accent; the admin
  uses the ivory background per the web-design note in the brandbook.
- Not a generic Bootstrap look: the character comes from the brandbook's own
  radii, type scale and restraint, not added ornament.

## Regenerating assets

The extraction scripts used to derive these assets from the PDF are not committed
(they need PyMuPDF and the brandbook). The committed SVGs and fonts are the
canonical source; do not approximate or re-trace them.
