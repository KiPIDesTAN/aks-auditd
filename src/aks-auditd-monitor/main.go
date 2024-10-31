package main

import (
	"os/exec"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"
)

// Mutex and flag to prevent concurrent auditd system service restarts
var (
	mu         sync.Mutex
	restarting bool
)

// Time interval to queue up multiple events before restarting auditd in seconds
const restartDelay = 30 * time.Second

func main() {
	// Directories to monitor
	dirs := []string{"/etc/audit/rules.d", "/etc/audit/plugins.d"}

	// Initialize the watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal("Error initializing watcher: ", err)
	}
	defer watcher.Close()

	// Start listening for events.
	go watchLoop(watcher)

	// Add the directories to the list of watches.
	for _, p := range dirs {
		err = watcher.Add(p)
		if err != nil {
			log.Fatalf("%q: %s", p, err)
		}
		log.Debug("Watching: ", p)
	}

	log.Info("Starting aks-auditd-monitor. Control-C to exit.")
	<-make(chan struct{}) // Block forever
}

// watchLoop
// Watch loop watches for any changes in the directories we are monitoring. If a change is detected, it queues up the event for a
// set amount of time before restarting the auditd service. This code restarts the auditd service by relying on an event timeout
// of 10 seconds. If something continuously writes to any of the monitored directories, the auditd service will never restart.
// Because the rules files are not expected to change frequenty, this should not be an issue.
func watchLoop(w *fsnotify.Watcher) {

	watcherTimeout := 10 * time.Second // Timeout we use to check if no events have occurred, but auditd needs to be restarted.
	var pauseStartTime time.Time       // Time when the first event occurs.

	for {
		select {
		// Read from Errors.
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			log.Error(err)
		case event, ok := <-w.Events:
			if !ok {
				return
			}

			// We care about create, write, and remove events in the directories we are watching.
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove) != 0 {
				log.Infof("Change detected: %s - %s", event.Op, event.Name)
				if pauseStartTime.IsZero() {
					pauseStartTime = time.Now() // Start the "pause" timer.
					log.Infof("Queuing up events for %v seconds before an auditd restart.", restartDelay)
				}
			}

			// Timeout case
		case <-time.After(watcherTimeout):
			log.Debugf("No events received for %v seconds. Executing another process...", watcherTimeout)
			if !pauseStartTime.IsZero() && time.Since(pauseStartTime) > restartDelay {
				log.Info("Pause for events timer expired. Restarting auditd.")
				restartAuditd()              // Block until auditd is restarted
				pauseStartTime = time.Time{} // Reset the "pause" timer.
			}
		}
	}
}

// restartAuditd restarts the auditd service
func restartAuditd() {
	mu.Lock()
	if restarting {
		log.Info("Restart already in progress, skipping...")
		mu.Unlock()
		return
	}
	restarting = true
	mu.Unlock()

	log.Info("Restarting auditd service.")
	cmd := exec.Command("systemctl", "restart", "auditd")
	if err := cmd.Run(); err != nil {
		log.Errorf("Failed to restart auditd: %v", err)
	}

	// Delay to avoid rapid restarts and reset the flag
	time.Sleep(1 * time.Second)
	mu.Lock()
	restarting = false
	mu.Unlock()
}
