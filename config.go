package appconfig

import (
	_ "embed"
	"encoding/json"
)

// Compiled into the executable; production never reads identity from runtime input.
//
//go:embed app.json
var manifest []byte

type Config struct {
	ID      string            `json:"id"`
	Version string            `json:"version"`
	Name    map[string]string `json:"name"`
}

func Load() Config {
	var config Config
	if err := json.Unmarshal(manifest, &config); err != nil {
		panic(err)
	}
	return config
}
