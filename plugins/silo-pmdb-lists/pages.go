package main

import (
	"fmt"
	"html"
	"strconv"
	"strings"
)

const pageCSS = `body{font-family:system-ui,-apple-system,sans-serif;max-width:1100px;margin:0 auto;padding:1.5rem;color:#e5e9f0;background:#0b0e14}
a{color:#88c0d0;text-decoration:none}a:hover{text-decoration:underline}
header{display:flex;align-items:center;gap:1rem;margin-bottom:1.5rem;flex-wrap:wrap}
header h1{margin:0;font-size:1.4rem}
.meta{color:#8b93a7;font-size:.85rem}
button{background:#3b82f6;color:#fff;border:0;border-radius:6px;padding:.5rem 1rem;font-size:.9rem;cursor:pointer}
button:disabled{opacity:.5}
.cards{display:grid;grid-template-columns:repeat(auto-fill,minmax(220px,1fr));gap:1rem}
.card{background:#151a24;border:1px solid #2a3346;border-radius:10px;padding:1.25rem}
.card h2{margin:.25rem 0 .5rem;font-size:1.1rem}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(150px,1fr));gap:1rem;margin-top:1rem}
.tile{background:#151a24;border:1px solid #2a3346;border-radius:8px;overflow:hidden;position:relative}
.tile img{width:100%;aspect-ratio:2/3;object-fit:cover;display:block;background:#1e2532}
.tile .t{padding:.5rem .6rem}
.tile .t .name{font-size:.85rem;font-weight:600;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.tile .t .sub{font-size:.75rem;color:#8b93a7}
.badge{position:absolute;top:.4rem;left:.4rem;font-size:.68rem;font-weight:700;padding:.15rem .45rem;border-radius:4px}
.badge.in{background:#2d6a4f;color:#d8f3dc}
.badge.out{background:#6b3a2d;color:#ffd6c9}
.filters{margin:1rem 0;display:flex;gap:.5rem;align-items:center}
.filters a{padding:.35rem .8rem;border-radius:6px;border:1px solid #2a3346;font-size:.85rem}
.filters a.on{background:#2a3346;color:#fff}
.err{background:#3a1f1f;border:1px solid #6b2d2d;border-radius:8px;padding:.75rem 1rem;margin-bottom:1rem;font-size:.85rem}`

func pageShell(title, body string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` +
		html.EscapeString(title) + `</title><style>` + pageCSS + `</style></head><body>` + body +
		`<script>
document.querySelectorAll('[data-sync]').forEach(b=>b.addEventListener('click',async()=>{
 b.disabled=true;b.textContent='Syncing…';
 try{const r=await fetch('/pmdb-lists/api/sync',{method:'POST'});const j=await r.json();
 if(j.error){alert('Sync failed: '+j.error)}else{location.reload()}}catch(e){alert('Sync failed: '+e)}
 b.disabled=false;b.textContent='Sync now'}));
</script></body></html>`
}

func esc(s string) string { return html.EscapeString(s) }

func renderIndex(cache *listCache, lastErr string) string {
	var b strings.Builder
	b.WriteString(`<header><h1>PMDB Lists</h1><button data-sync>Sync now</button>`)
	if !cache.SyncedAt.IsZero() {
		fmt.Fprintf(&b, `<span class="meta">Last synced %s</span>`, esc(cache.SyncedAt.Format("Jan 2, 2006 15:04")))
	}
	b.WriteString(`</header>`)
	if lastErr != "" {
		fmt.Fprintf(&b, `<div class="err">%s</div>`, esc(lastErr))
	}
	if len(cache.Lists) == 0 {
		b.WriteString(`<p class="meta">No lists synced yet. Press <b>Sync now</b> to fetch your PublicMetaDB lists.</p>`)
		return pageShell("PMDB Lists", b.String())
	}
	b.WriteString(`<div class="cards">`)
	for _, id := range sortedKeys(cache.Lists) {
		cl := cache.Lists[id]
		movies, shows := 0, 0
		for _, it := range cl.Items {
			if it.MediaType == "tv" {
				shows++
			} else {
				movies++
			}
		}
		fmt.Fprintf(&b, `<div class="card"><div class="meta">%s</div><h2><a href="/pmdb-lists/%s">%s</a></h2><div class="meta">%d titles · %d movies · %d series</div></div>`,
			esc(id), esc(id), esc(cl.Name), len(cl.Items), movies, shows)
	}
	b.WriteString(`</div>`)
	return pageShell("PMDB Lists", b.String())
}

func renderListDetail(cl *cachedList, presence map[string]bool, filter string, syncedAt string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<header><h1><a href="/pmdb-lists">PMDB Lists</a> / %s</h1><button data-sync>Sync now</button><span class="meta">%d titles</span></header>`,
		esc(cl.Name), len(cl.Items))
	b.WriteString(`<div class="filters"><span class="meta">Show:</span>`)
	for _, f := range []struct{ key, label string }{{"all", "All"}, {"missing", "Missing from library"}} {
		on := ""
		if filter == f.key {
			on = " on"
		}
		fmt.Fprintf(&b, `<a class="%s" href="/pmdb-lists/%s?filter=%s">%s</a>`, strings.TrimSpace(on), esc(cl.ID), f.key, f.label)
	}
	b.WriteString(`</div><div class="grid">`)
	shown := 0
	for _, it := range cl.Items {
		inLib := presence[strconv.Itoa(it.TMDBID)]
		if filter == "missing" && inLib {
			continue
		}
		shown++
		badge := `<span class="badge out">Missing</span>`
		if inLib {
			badge = `<span class="badge in">In library</span>`
		}
		poster := it.PosterURL
		img := `<div style="aspect-ratio:2/3;background:#1e2532"></div>`
		if poster != "" {
			img = fmt.Sprintf(`<img loading="lazy" src="%s" alt="">`, esc(poster))
		}
		year := ""
		if it.Year > 0 {
			year = fmt.Sprintf(" · %d", it.Year)
		}
		kind := "Movie"
		if it.MediaType == "tv" {
			kind = "Series"
		}
		fmt.Fprintf(&b, `<div class="tile">%s%s<div class="t"><div class="name" title="%s">%s</div><div class="sub">%s%s</div></div></div>`,
			badge, img, esc(it.Title), esc(it.Title), kind, year)
	}
	b.WriteString(`</div>`)
	if shown == 0 {
		b.WriteString(`<p class="meta">Everything on this list is already in your library. 🎉</p>`)
	}
	return pageShell(cl.Name+" — PMDB Lists", b.String())
}

func sortedKeys(m map[string]*cachedList) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Deterministic order: sort by list name, then id.
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			ni, nj := m[keys[i]].Name, m[keys[j]].Name
			if ni > nj || (ni == nj && keys[i] > keys[j]) {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}
