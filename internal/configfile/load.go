// Package configfile parses the YAML config file into internal/config's
// plain structs. It is deliberately its own package, separate from
// internal/config: config.Config/AppConfig/GlobalConfig plus Validate are
// stdlib-only so hot-loop packages (metrics, scheduler, appstate, runner)
// can depend on them directly, but gopkg.in/yaml.v3 must never be
// reachable from that graph (CLAUDE.md: yaml parsing is "startup/reload
// only, never in the loop"). Only cmd/lqg imports this package.
package configfile

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/DevDevil/laravel-queue-governor/internal/config"
)

// Load parses the YAML config at path. It does not validate the result —
// that's config.Validate; Load only reports parse errors.
func Load(path string) (*config.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	return &cfg, nil
}
