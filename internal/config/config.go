package config

// Mode selects how an app's queue worker is invoked.
type Mode string

const (
	ModePerJob Mode = "per_job"
	ModeBatch  Mode = "batch"
)

// Config is the top-level shape of the YAML config file (PLAN.md §7).
type Config struct {
	Global GlobalConfig `yaml:"global"`
	Apps   []AppConfig  `yaml:"apps"`
}

type GlobalConfig struct {
	SocketPath        string  `yaml:"socket_path"`
	CheckInterval     int     `yaml:"check_interval"`
	MaxSystemLoad     float64 `yaml:"max_system_load"`
	MaxIOWait         int     `yaml:"max_iowait"`
	MaxConcurrentJobs int     `yaml:"max_concurrent_jobs"`
	LogLevel          string  `yaml:"log_level"`

	// Nice is the OS scheduling niceness (-20 highest .. 19 lowest
	// priority) every queue worker runs at unless an app sets its own
	// (AppConfig.Nice). Zero (the Go/YAML default) means "don't touch
	// it" — same as never running `nice` at all. This is unrelated to
	// AppConfig.Priority, which only affects lqg's own fair-dispatch
	// ordering, not OS-level CPU scheduling.
	Nice int `yaml:"nice"`
}

type AppConfig struct {
	Name          string   `yaml:"name"`
	Path          string   `yaml:"path"`
	User          string   `yaml:"user"`
	PHPBinary     string   `yaml:"php_binary"`
	Connection    string   `yaml:"connection"`
	Queues        []string `yaml:"queues"`
	Mode          Mode     `yaml:"mode"`
	BatchSize     int      `yaml:"batch_size"`
	MaxLoad       float64  `yaml:"max_load"` // this app's counterpart to GlobalConfig.MaxSystemLoad
	MaxIOWait     int      `yaml:"max_iowait"`
	MaxConcurrent int      `yaml:"max_concurrent"` // this app's counterpart to GlobalConfig.MaxConcurrentJobs
	MinInterval   int      `yaml:"min_interval"`
	SpikeCooldown int      `yaml:"spike_cooldown"`
	Priority      int      `yaml:"priority"`

	// JSONOutput adds --json to this app's queue:work invocation, so the
	// runner can parse real per-job lifecycle events (job class, status)
	// off stdout instead of guessing from run duration. Opt-in per app,
	// not a default: --json is a relatively recent queue:work flag, so
	// an app on an older Laravel that doesn't support it must not get it
	// added unconditionally.
	JSONOutput bool `yaml:"json_output"`

	// Nice overrides global.nice for just this app when set. A pointer
	// (not a plain int) because 0 is itself a meaningful, explicit nice
	// value — "run this app at normal priority even though global.nice
	// is set" — distinct from "not configured, inherit global.nice".
	Nice *int `yaml:"nice"`

	// ResolvedUID/ResolvedGID are populated by Validate, never by YAML —
	// CLAUDE.md: "uid/gid come only from validated config," so the runner
	// never looks the user up itself.
	ResolvedUID uint32 `yaml:"-"`
	ResolvedGID uint32 `yaml:"-"`
}
