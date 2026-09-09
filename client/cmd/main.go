package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"usbridge-client/internal/api"
	"usbridge-client/internal/gui"
	"usbridge-client/internal/gui/i18n"
	"usbridge-client/internal/gui/view"
	"usbridge-client/internal/models"
	"usbridge-client/internal/update"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const (
	appName = "usbridge-client"
)

var (
	version = "1.0.0"
)

// embeddedVersion mirrors the repo-root VERSION file (kept in sync at build
// time — see each scripts/build_*.sh's copy step) baked in via go:embed as
// a fallback for build tools that can't inject -ldflags -X main.version=...
// the way a plain `go build` invocation does: build_ios.sh packages via
// `fyne package`, which has no ldflags passthrough at all, so iOS builds
// silently kept the "1.0.0" placeholder above forever instead of the real
// version. Only used when ldflags didn't already override `version` (the
// linker patches that in before any init() runs, so this check reliably
// tells the two cases apart).
//
//go:embed VERSION
var embeddedVersion string

func init() {
	if version == "1.0.0" {
		if v := strings.TrimSpace(embeddedVersion); v != "" {
			version = v
		}
	}
}

func main() {
	defer func() {
		if recovered := recover(); recovered != nil {
			writeStartupPanicTrace(recovered)
			logrus.Errorf("🔥 FATAL PANIC: %v\n%s", recovered, string(debug.Stack()))
			// Flushes the panic line (and anything queued before it) out
			// through the real writer before this process actually dies
			// via the re-panic below -- see asyncLogWriter's own doc
			// comment for why a plain `defer asyncWriter.Close(...)`
			// elsewhere wouldn't reliably cover this specific path: Go
			// runs deferred functions LIFO, so a flush-defer registered
			// after this one would run *before* this handler's own
			// logrus.Errorf call above, flushing nothing new. Calling it
			// explicitly, right here, guarantees the panic message itself
			// is what gets flushed, not just whatever was queued earlier.
			// nil-safe: this can run before setupLogging ever assigns it,
			// e.g. a panic during flag parsing.
			if asyncLog != nil {
				asyncLog.Close(2 * time.Second)
			}
			panic(recovered)
		}
	}()

	var (
		configFile  = flag.String("config", "", "Path to the configuration file")
		logLevel    = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
		showVersion = flag.Bool("version", false, "Show version")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s version %s\n", appName, version)
		os.Exit(0)
	}

	setupLogging(*logLevel)
	// Normal-exit counterpart to the panic-recovery defer above -- flushes
	// whatever's still queued in asyncLog before the process actually
	// exits on any ordinary return from main(). Registered right after
	// setupLogging assigns asyncLog, not up at the top of main(): Go's
	// LIFO defer order means this needs to be the *first* one to run on
	// the way out (so its own flush happens last, after everything else
	// in this function -- including that panic-recovery defer's body --
	// has finished logging), which registering it second (after the
	// already-declared panic-recovery defer) achieves for free.
	defer func() {
		if asyncLog != nil {
			asyncLog.Close(2 * time.Second)
		}
	}()
	startPprofIfEnabled()

	logrus.Infof("Starting %s version %s", appName, version)

	i18n.Init("en")

	config, err := loadConfig(*configFile)
	if err != nil {
		logrus.Fatalf("Failed to load configuration: %v", err)
	}

	logrus.Infof("Configuration loaded")
	logrus.Infof("NBD port: %d", config.NBDPort)

	// Opt-in local ui.parse offload (ONNX Runtime on this machine's CPU/
	// Intel iGPU instead of the device's NPU) -- no-op unless
	// LocalUIParseEnabled is set, see internal/api/local_ui_init.go.
	api.InitLocalUIParseFromConfig(config)

	gui.SetAppVersion(version)
	view.SetAppVersion(version)
	mainWindow := gui.NewMainWindow(config)

	// Mandatory startup update check, gated on user confirmation: a forced
	// silent update was jarring for a window the user is actively looking
	// at, so this asks first via a "new version available, update now?"
	// dialog instead of relaunching out from under them. Runs once the
	// window is ready (onReadyCallback already fires on its own goroutine),
	// so the network round-trip in update.Check never delays first paint.
	// See internal/update's package doc for the MITM-resistance design
	// (Ed25519-signed manifest, then SHA-256-verified artifact) — that part
	// is unchanged, only whether an approved update applies automatically.
	mainWindow.SetOnReadyCallback(func() {
		manifest := update.Check(context.Background(), version)
		if manifest == nil {
			return
		}
		mainWindow.ShowUpdateAvailableDialog(manifest.Version, version, func(confirmed bool) {
			if !confirmed {
				logrus.Info("update declined by user")
				return
			}
			// A progress dialog while this downloads — a silent update
			// that just sits there with no visible activity is
			// indistinguishable from having frozen.
			progress := mainWindow.ShowUpdateProgressDialog(manifest.Version)
			go func() {
				err := update.DownloadAndApply(context.Background(), manifest, progress.Update)
				// A successful apply on desktop relaunches the app and
				// never returns here at all — reaching this point means
				// it didn't, so the dialog needs to be dismissed instead
				// of sitting there looking stuck.
				progress.Close()
				if err != nil {
					logrus.Errorf("failed to apply update: %v", err)
					mainWindow.ShowUpdateFailedDialog(err)
				}
			}()
		})
	})

	logrus.Info("Starting GUI")
	mainWindow.Show()
}

// startPprofIfEnabled starts a loopback-only net/http/pprof server when
// USBRIDGE_PPROF_ADDR is set (e.g. "127.0.0.1:6060"), for profiling freezes
// with `go tool pprof http://127.0.0.1:6060/debug/pprof/profile?seconds=10`
// or capturing a goroutine dump mid-stutter via /debug/pprof/goroutine.
func startPprofIfEnabled() {
	addr := os.Getenv("USBRIDGE_PPROF_ADDR")
	if addr == "" {
		return
	}
	go func() {
		logrus.Warnf("pprof debug server listening on http://%s/debug/pprof/", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			logrus.Errorf("pprof server failed: %v", err)
		}
	}()
}

func setupLogging(level string) {
	logLevel, err := logrus.ParseLevel(level)
	if err != nil {
		logrus.Warnf("Invalid log level %s, using info", level)
		logLevel = logrus.InfoLevel
	}
	logrus.SetLevel(logLevel)

	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})

	logFilePath := appLogPath()
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		logrus.SetOutput(os.Stdout)
		logrus.Warnf("Failed to open log file: %v", err)
		return
	}

	output, err := setupPlatformLogOutput(logFile)
	if err != nil {
		logrus.SetOutput(logFile)
		logrus.Warnf("Failed to redirect standard output to log file: %v", err)
		return
	}

	// Wraps `output` (stdout, the log file, or both) in a background-
	// goroutine writer so a `logrus.*` call never blocks on the actual
	// write(2)/console-write itself -- see asyncLogWriter's own doc
	// comment for why this matters concretely (a real network-loss burst
	// firing well over a hundred log lines in a couple of seconds,
	// confirmed live, on the same CGO video/depacketizer thread that's
	// simultaneously trying to recover the stream). Stored in the
	// package-level `asyncLog` so main()'s shutdown paths (normal return
	// and the panic-recovery handler) can flush it before the process
	// actually exits -- see those two call sites' own doc comments.
	asyncLog = newAsyncLogWriter(output)
	logrus.SetOutput(asyncLog)
	logrus.Infof("Logging initialized: %s", logFilePath)
}

// asyncLog is nil until setupLogging assigns it (so a panic before that --
// e.g. during flag parsing -- has something safe to nil-check against in
// main()'s panic-recovery handler) and, once assigned, lives for the rest
// of the process: exactly one asyncLogWriter is ever created, matching
// logrus's own single-global-logger design that setupLogging already
// leans on throughout this file.
var asyncLog *asyncLogWriter

func loadConfig(configFile string) (*models.AppConfig, error) {
	config := models.DefaultConfig()

	if configFile != "" {
		if err := loadConfigFromFile(config, configFile); err != nil {
			return nil, fmt.Errorf("failed to load configuration file: %v", err)
		}
		logrus.Infof("Configuration loaded from %s", configFile)
	} else {
		configPaths := []string{
			"./config.yaml",
			"./config.yml",
			"./config.json",
			"./config.toml",
			filepath.Join(os.Getenv("HOME"), ".config", appName, "config.yaml"),
			filepath.Join(os.Getenv("HOME"), ".config", appName, "config.yml"),
			"/etc/" + appName + "/config.yaml",
		}

		for _, path := range configPaths {
			if _, err := os.Stat(path); err == nil {
				if err := loadConfigFromFile(config, path); err == nil {
					logrus.Infof("Configuration loaded from %s", path)
					break
				}
			}
		}
	}

	return config, nil
}

func loadConfigFromFile(config *models.AppConfig, filename string) error {
	viper.SetConfigFile(filename)
	viper.SetConfigType(filepath.Ext(filename)[1:])

	if err := viper.ReadInConfig(); err != nil {
		return err
	}

	viper.AutomaticEnv()
	viper.SetEnvPrefix("USBRIDGE")

	if err := viper.Unmarshal(config); err != nil {
		return err
	}

	return nil
}
