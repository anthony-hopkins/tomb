# Project assets

Brand and documentation art: the README banner, logos, screenshots.

These are **not** served by the running site. `docs/` is excluded from the
Docker build context, so nothing here reaches the image or the binary — which
is the point: drop in a multi-megabyte banner without a thought.

Assets the site itself serves (a favicon, a logo rendered in the page header)
belong in `internal/platform/static/` instead. That directory is compiled into
the binary with `go:embed` and served at `/static/`, so keep it small, and note
the site's CSP is `img-src 'self'` — an externally hosted image is blocked.

## Current assets

| File | Dimensions | Size | Used by |
|---|---|---|---|
| `banner.jpg` | 1200x800 | 120 KB | The README header, displayed at 640px wide |

Keep committed art small. `banner.jpg` came from a 1536x1024 / 2.0 MB PNG
master; resized to 1200px and saved as quality-88 progressive JPEG it is 17x
smaller with no visible difference at display size. Keep your master copy
outside the repository.
