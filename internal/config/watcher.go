package config

import (
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"

	mylogger "github.com/taha/myprog/internal/logger"
)

// WatchConfig monitors the directory containing the configuration file for changes.
// It is designed to handle standard file-writes as well as Kubernetes ConfigMap symlink-swaps.
func WatchConfig(configPath string, onReload func(*Config)) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		mylogger.Error("Failed to initialize config file watcher", zap.Error(err))
		return
	}
	defer watcher.Close()

	dir := filepath.Dir(configPath)
	err = watcher.Add(dir)
	if err != nil {
		mylogger.Error("Failed to watch config directory", zap.String("dir", dir), zap.Error(err))
		return
	}

	mylogger.Info("Config watcher started", zap.String("path", configPath))

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// Under Kubernetes, ConfigMap updates trigger atomic symlink swaps on the parent '..data' directory.
			// Depending on the host OS kernel and VFS translation layers, this swap might be reported exclusively
			// as Rename or Remove events instead of standard Write/Create events. We observe all four event flags
			// to guarantee reliable change-detection across various cloud provider platforms.
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) || event.Has(fsnotify.Remove) {
				if filepath.Base(event.Name) == filepath.Base(configPath) || filepath.Base(event.Name) == "..data" {
					mylogger.Info("Configuration modification detected, initiating reload sequence...", zap.String("file", event.Name))

					// Introduce a 100ms debounce window. Although the symlink exchange on '..data' is theoretically atomic, Go's VFS
					// operations can run into a transient cache-coherence window where the target file temporarily returns ENOENT (not found)
					// or an empty read. A 100ms sleep allows the filesystem transaction to settle globally on the host.
					time.Sleep(100 * time.Millisecond)

					newConfig, err := ParseFile(configPath)
					if err != nil {
						mylogger.Warn("Transient error reading updated config file, skipping current reload event", zap.Error(err))
						continue
					}

					GlobalConfig.Store(newConfig)

					mylogger.SetLevel(newConfig.Telemetry.Logging.Level)

					if onReload != nil {
						onReload(newConfig)
					}

					mylogger.Info("Configuration successfully hot-reloaded and atomic log level updated",
						zap.String("new_level", newConfig.Telemetry.Logging.Level))
				}
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			mylogger.Error("Config watcher encountered an error", zap.Error(err))
		}
	}
}
