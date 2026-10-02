# ICAS website

The website of ICAS, the International Conference on Autonomous System. It is written in Go using only the standard library. The page structure follows the IEEE ISCAS 2026 and 2027 sites.

## Run

Requires Go 1.21 or later.

```bash
go run . -dev                # http://localhost:8090; templates and content reload on every request
go build -o icas . && ./icas # single binary: templates, assets and content are embedded
```

| Flag / env | Purpose |
|---|---|
| `-addr` / `ICAS_ADDR` or `PORT` | Listen address (default `:8090`) |
| `-data` / `ICAS_DATA_DIR` | Where submissions, messages and subscribers are stored as JSON Lines (default `data/`) |
| `-base-url` / `ICAS_BASE_URL` | Public origin for canonical links and `sitemap.xml` |
| `-trust-proxy` / `ICAS_TRUST_PROXY=1` | Honour `X-Forwarded-*` headers when running behind a reverse proxy |
| `ICAS_ADMIN_USER`, `ICAS_ADMIN_PASSWORD` | Turn on `/admin`, which lists records and offers CSV exports (including an anonymized export for double-blind review) |

## Test

```bash
gofmt -l .
go vet ./...
go test ./...
```

The tests render every page, follow every internal link and asset, and exercise the submission, contact, newsletter and admin flows. GitHub Actions runs the same checks on every push and pull request.

## Project layout

| Path | Contents |
|---|---|
| `main.go` | Command-line entry point; embeds `web/` and `content/site.json` |
| `internal/server` | HTTP handlers, page registry and navigation, forms, admin, middleware |
| `internal/content` | Data model and validation for `content/site.json` |
| `internal/store` | JSON Lines storage for form records |
| `content/site.json` | All conference facts shown on the site |
| `web/templates` | HTML templates (layout, partials, one file per page) |
| `web/static` | CSS, JavaScript, images and downloadable files |
| `tools/make_assets.py` | Generator for the promotional kit, CFP flyer, slide template and social preview image |

## Editing content

All conference facts live in `content/site.json`. An empty string is shown on the site as **TBA**. This covers dates, venue, committee, keynotes, fees, sponsors, hotels, news, events and calls. Set `conference.submissionOpen` to `false` to close the submission form. Restart the server after adding or removing an event.

## Regenerating downloads

After you change dates, venue or year, regenerate the promotional kit, the CFP flyer PDF, the slide template and the social preview image. This needs Python with Pillow and python-pptx, and Edge or Chrome:

```bash
python tools/make_assets.py
```
