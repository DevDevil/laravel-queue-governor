package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/DevDevil/laravel-queue-governor/internal/clock"
	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/configfile"
	"github.com/DevDevil/laravel-queue-governor/internal/daemon"
	"github.com/DevDevil/laravel-queue-governor/internal/metrics"
	"github.com/DevDevil/laravel-queue-governor/internal/monitor"
	"github.com/DevDevil/laravel-queue-governor/internal/runner"
	"github.com/DevDevil/laravel-queue-governor/internal/scheduler"
)

// metricSampleInterval is the fixed, independent cadence MetricSampler
// polls at — decoupled from global.check_interval so `lqg watch`'s graphs
// stay smooth regardless of how slow the decision tick is configured.
const metricSampleInterval = time.Second

// loadAndValidate reads and validates the config at path, printing any
// non-fatal warnings to warnOut (prefixed with warnPrefix) before
// returning — shared by runDaemon's startup load, watchForReload's SIGHUP
// reload, and `lqg validate`, so the load-then-validate-then-warn sequence
// exists in exactly one place.
func loadAndValidate(path string, warnOut io.Writer, warnPrefix string) (*config.Config, error) {
	cfg, err := configfile.Load(path)
	if err != nil {
		return nil, err
	}

	warnings, err := config.Validate(cfg)
	for _, w := range warnings {
		fmt.Fprintf(warnOut, "%s: %s\n", warnPrefix, w)
	}
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func newRunCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the daemon in the foreground (what systemd invokes)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemon(configPath)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "/etc/lqg/config.yaml", "path to config file")
	return cmd
}

func runDaemon(configPath string) error {
	cfg, err := loadAndValidate(configPath, os.Stderr, "warning")
	if err != nil {
		return err
	}

	sched := scheduler.New(cfg.Global, cfg.Apps, metrics.NewProcSource())

	// Independent of sched's own metrics.Source: ProcSource does stateful
	// iowait delta-sampling between calls, so the scheduler's tick-paced
	// sampling and this fixed ~1s sampling need their own instances, or
	// they'd corrupt each other's deltas (internal/monitor.MetricSampler).
	sampler := monitor.NewMetricSampler(metrics.NewProcSource(), clock.RealClock{}, metricSampleInterval, runtime.NumCPU(), monitor.DefaultMetricHistorySize, monitor.Ceilings{MaxLoad: cfg.Global.MaxSystemLoad, MaxIOWait: float64(cfg.Global.MaxIOWait)})
	sampler.Stderr = os.Stderr

	srv := &monitor.Server{
		Snapshots:     monitor.NewSnapshotStore(),
		Bus:           monitor.NewBus(),
		MetricHistory: sampler.History(),
		MetricBus:     sampler.Bus(),
		Refresh:       monitor.NewRefreshBus(),
	}
	srv.SetGlobalInfo(runtime.NumCPU(), cfg.Global)
	if err := srv.Listen(cfg.Global.SocketPath); err != nil {
		return err
	}
	defer srv.Close()

	go func() {
		if err := srv.Serve(); err != nil {
			fmt.Fprintf(os.Stderr, "socket server: %v\n", err)
		}
	}()

	loop := daemon.New(
		sched,
		runner.ExecRunner{Stderr: os.Stderr},
		srv.Bus,
		srv.Snapshots,
		clock.RealClock{},
		os.Stderr,
		time.Duration(cfg.Global.CheckInterval)*time.Second,
		srv.Refresh,
	)
	// loop.Tune has no reference to sampler/srv, so a successful global-scope
	// tune still needs applyGlobalSideEffects run here explicitly, same as
	// the SIGHUP path below does.
	srv.Tune = func(scope, app, field, value string) ([]string, error) {
		warnings, err := loop.Tune(scope, app, field, value)
		if err == nil && scope == "global" {
			// loop.Tune only returns once its own Reload has actually
			// landed (internal/daemon/tune.go's waitApplied), so this
			// ConfigSnapshot is guaranteed to reflect the just-applied
			// change, not a stale pre-tune value.
			applyGlobalSideEffects(loop.ConfigSnapshot().Global, sampler, srv)
		}
		return warnings, err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go watchForReload(ctx, configPath, loop, sampler, srv)
	go sampler.Run(ctx)

	fmt.Fprintf(os.Stderr, "lqg: listening on %s, %d app(s), check_interval=%ds\n",
		cfg.Global.SocketPath, len(cfg.Apps), cfg.Global.CheckInterval)
	loop.Run(ctx)
	return nil
}

// watchForReload re-reads and validates configPath on every SIGHUP
// (PLAN.md §7/§14) and hands the result to loop.Reload only if it's
// valid — an invalid config is logged and the daemon keeps running on
// whatever it already had (CLAUDE.md: never run on a half-parsed
// config). Runs on its own goroutine; the actual Scheduler/Machine
// mutation happens on Loop.Run's own goroutine via the reload channel,
// so this never touches daemon state directly. Also refreshes the
// sampler's ceiling (ticket 16 Phase 3) via its own mutex-guarded
// setter, so "% of ceiling" coloring stays correct post-reload without
// the sampler reading Scheduler.Global from its own goroutine.
func watchForReload(ctx context.Context, configPath string, loop *daemon.Loop, sampler *monitor.MetricSampler, srv *monitor.Server) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
			cfg, err := loadAndValidate(configPath, os.Stderr, "SIGHUP warning")
			if err != nil {
				fmt.Fprintf(os.Stderr, "SIGHUP: %v (keeping previous config)\n", err)
				continue
			}

			loop.Reload(cfg.Global, cfg.Apps)
			applyGlobalSideEffects(cfg.Global, sampler, srv)
		}
	}
}

// applyGlobalSideEffects refreshes the two pieces of state that mirror
// global config outside the scheduler itself — the sampler's ceiling (so
// "% of ceiling" graph coloring stays correct) and the dashboard's
// hello-frame info (cores/ceilings/check_interval/nice, TUI.md §12.1.1) —
// after global config actually changes. Shared by both triggers that can
// change it: a SIGHUP reload (above) and a live tune (this file's srv.Tune
// wiring) — same two calls either way, so this stays the one place that
// has to remember to make them.
func applyGlobalSideEffects(global config.GlobalConfig, sampler *monitor.MetricSampler, srv *monitor.Server) {
	sampler.SetCeiling(global.MaxSystemLoad, float64(global.MaxIOWait))
	srv.SetGlobalInfo(runtime.NumCPU(), global)
}
