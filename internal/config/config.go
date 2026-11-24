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

	// LogDir, when set, moves logging to files (PLAN.md §8.1): the
	// daemon's own log to <log_dir>/lqg.log and each app's log — its
	// workers' stdout/stderr plus the daemon's decisions about it — to
	// <log_dir>/<name>.log. Empty (the default) keeps everything on
	// stderr (the systemd journal), as before.
	LogDir string `yaml:"log_dir"`

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

	// Scheduler optionally has lqg also run this app's Laravel scheduler
	// (`artisan schedule:run`) once a minute (PLAN.md §6.1). The zero value
	// (enabled: false) is exactly the pre-scheduler behaviour.
	Scheduler SchedulerConfig `yaml:"scheduler"`

	// LogFile overrides this app's log path (default <log_dir>/<name>.log).
	// Also works without global.log_dir: then only this app gets a file.
	LogFile string `yaml:"log_file"`

	// ResolvedUID/ResolvedGID are populated by Validate, never by YAML —
	// CLAUDE.md: "uid/gid come only from validated config," so the runner
	// never looks the user up itself.
	ResolvedUID uint32 `yaml:"-"`
	ResolvedGID uint32 `yaml:"-"`
}

// SchedulerConfig is an app's optional `schedule:run` setup (PLAN.md §6.1).
type SchedulerConfig struct {
	// Enabled runs `<php_binary> artisan schedule:run` once a minute, as
	// the app's user, through the same exec path as queue:work.
	Enabled bool `yaml:"enabled"`

	// RespectLoad subjects schedule:run to the same load/iowait ceilings
	// (own, then global) and the global concurrency cap as queue workers.
	// Off by default: the scheduler is time-critical, so by default only
	// the no-overlap rule can skip a minute.
	RespectLoad bool `yaml:"respect_load"`

	// Stagger offsets this app's run by a fixed number of seconds within
	// the minute (derived from a hash of the app name), so many apps
	// don't all start schedule:run at :00.
	Stagger bool `yaml:"stagger"`
}
