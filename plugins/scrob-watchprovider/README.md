# Scrob Watch Provider (vendored)

Watch provider plugin for Scrob (self-hosted watch history / progress tracker),
vendored from [Joloxx9/silo-plugin-watchprovider-scrob](https://github.com/Joloxx9/silo-plugin-watchprovider-scrob)
v0.1.3. Binaries are the upstream release artifacts (checksum-verified);
only the manifest is copied here so the repo index can reference it.

- Connects each Vio profile to a self-hosted Scrob instance.
- Imports movie/episode watch history and resume positions.
- Exports completed watches / unwatches and ratings (movies, series, episodes).
- Reports live playback to Scrob's Now Playing.

Setup: in Scrob, Connections page → copy the API key; in Vio's
watch-provider settings, connect each profile with the Scrob URL
(as Vio reaches it) plus the key. Needs Scrob 2.17.0+.
