# vio-plugins

Evan's personal plugin collection for Vio (and Silo). Add the catalog once; every plugin in this repo shows up.

## Add to Vio

In Vio, open **Admin → Plugins → Catalog**, add this custom repository URL:

```
https://raw.githubusercontent.com/gurgles-1/vio-plugins/main/catalog.json
```

Both plugins below will be listed and installable from there.

## Plugins

| Plugin | ID | What it does |
|---|---|---|
| PMDB Lists (Vio) | `com.gurgles-1.vio-pmdb-lists` | Syncs PublicMetaDB lists into Vio as zero-storage virtual movies and series (full episode metadata). Playback via Vio Virtual Library + AIOStreams. |
| PMDB Lists (Silo) | `com.gurgles-1.silo-pmdb-lists` | Browsable PMDB list pages inside Silo with TMDB posters and "in library / missing" badges. Discovery view only — Silo's plugin API cannot inject library items. |

Each plugin keeps its source under `plugins/<name>/` and its released binaries under `dist/<name>/<version>/`.

## Layout

```
catalog.json                  # the catalog Vio reads — one entry per plugin
plugins/vio-pmdb-lists/       # Go source (drondeseries virtual SDK fork)
plugins/silo-pmdb-lists/      # Go source (upstream Silo-Server SDK)
dist/<plugin>/<version>/      # released linux/amd64 + linux/arm64 binaries
```

## Releasing a new version

1. Build: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/<plugin>/<version>/<plugin>-linux-amd64 ./...` (repeat for arm64) from `plugins/<plugin>/`.
2. `sha256sum dist/<plugin>/<version>/*` and update the `artifacts` entries in `catalog.json`.
3. Commit, tag, push.

## Notes

- Binaries are committed to the repo because GitHub release-asset upload isn't available to the publishing automation used here.
- `gurgles-1/vio-pmdb-lists` and `gurgles-1/silo-pmdb-lists` were the original standalone repos; this monorepo supersedes them.
