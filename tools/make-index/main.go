package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
)

type platformBinary struct {
	URL      string `json:"url"`
	Checksum string `json:"checksum"`
}

type catalogPackage struct {
	Manifest json.RawMessage            `json:"manifest"`
	RepoURL  string                    `json:"repo_url,omitempty"`
	Binaries map[string]platformBinary `json:"binaries,omitempty"`
}

type repositoryIndex struct {
	Plugins []catalogPackage `json:"plugins"`
}

func main() {
	repoRoot := os.Args[1]
	plugins := [][2]string{
		{"vio-pmdb-lists", "0.2.0"},
		{"silo-pmdb-lists", "0.1.0"},
		{"vio-aiostreams-watchsync", "0.1.0"},
		{"scrob-watchprovider", "0.1.3"},
	}
	idx := repositoryIndex{}
	for _, p := range plugins {
		name, verdir := p[0], "v"+p[1]
		m, err := manifest.LoadFromDisk(filepath.Join(repoRoot, "plugins", name, "manifest.json"))
		if err != nil {
			panic(err)
		}
		if err := manifest.Validate(m); err != nil {
			panic(fmt.Sprintf("%s: %v", name, err))
		}
		// encoding/json marshal: numeric enums, exactly what Vio's catalog
		// service decodes (it does NOT use protojson).
		raw, err := json.Marshal(m)
		if err != nil {
			panic(err)
		}
		bins := map[string]platformBinary{}
		for _, arch := range []string{"amd64", "arm64"} {
			fname := filepath.Join(repoRoot, "dist", name, verdir, name+"-linux-"+arch)
			data, err := os.ReadFile(fname)
			if err != nil {
				panic(err)
			}
			sum := sha256.Sum256(data)
			bins["linux/"+arch] = platformBinary{
				URL:      "https://raw.githubusercontent.com/gurgles-1/vio-plugins/main/dist/" + name + "/" + verdir + "/" + name + "-linux-" + arch,
				Checksum: hex.EncodeToString(sum[:]),
			}
		}
		idx.Plugins = append(idx.Plugins, catalogPackage{
			Manifest: raw,
			RepoURL:  "https://github.com/gurgles-1/vio-plugins",
			Binaries: bins,
		})
		fmt.Fprintf(os.Stderr, "%s: manifest validated, %d binaries\n", name, len(bins))
	}
	out, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "index.json"), append(out, '\n'), 0644); err != nil {
		panic(err)
	}
}
