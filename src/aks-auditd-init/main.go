package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"golang.org/x/sys/unix"
)

// Log level mappings from string to logrus.Level
type Level int

var levelMap = map[string]log.Level{
	"panic": log.PanicLevel,
	"fatal": log.FatalLevel,
	"error": log.ErrorLevel,
	"warn":  log.WarnLevel,
	"info":  log.InfoLevel,
	"debug": log.DebugLevel,
	"trace": log.TraceLevel,
}

const chrootMount = "/node"                    // Container mount point where the host file system is mounted.
const audispdPluginsMount = "/audispd-plugins" // Container mount point where the audispd plugin config is mounted.

// UID of the user the main container will run as - restarting the auditd service and copying config files to the node.
// Account is created with --system flag, which requires a UID between SYS_UID_MIN and SYS_UID_MAX, defined in /etc/login.defs
const aksauditdUID = 807

// GID of the audit admins group
const auditadminsGID = 808

// Location of where the aks-auditd-monitor binary and service file will be copied to on the host file system
const aksAuditdMonitorBinaryPath = "/usr/sbin/aks-auditd-monitor"
const aksAuditdMonitorServicePath = "/etc/systemd/system/aks-auditd-monitor.service"

const hostRulesDirectory = "/etc/audit/rules.d"     // rules directory on the host file system. No trailing slash.
const hostPluginsDirectory = "/etc/audit/plugins.d" // plugins directory on the host file system. No trailing slash.

func main() {
	// Set default config values
	viper.SetDefault("logLevel", "info")

	// Environment variable settings
	// NOTE: When using BindEnv with multiple, SetEnvPrefix does not apply and we must set it explicitly
	viper.SetEnvPrefix("AA")
	viper.BindEnv("logLevel", "AA_LOG_LEVEL")

	// Set the file name of the configuration file without the extension
	viper.SetConfigName("config")
	viper.AddConfigPath("/etc/aks-auditd")
	viper.SetConfigType("yaml")
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			log.Debug("Config file not found. Using default values.")
		} else {
			log.Fatalf("Error reading config file: %v", err)
		}
	}

	// Output the configuration settings
	log.Info("Rules Directory: ", hostRulesDirectory)
	log.Info("Plugins Directory: ", hostPluginsDirectory)
	log.Info("AKS Auditd UID (system user): ", aksauditdUID)

	level, ok := levelMap[strings.ToLower(viper.GetString("logLevel"))]
	if !ok {
		log.Warn(fmt.Sprintf("Invalid log level: %s. Falling back to 'info' level logging.", viper.GetString("logLevel")))
		level = log.InfoLevel
	}
	log.Info("Log Level: ", viper.GetString("logLevel"))
	log.SetLevel(level)

	// Chroot to the host file system
	exit, err := Chroot(chrootMount)
	if err != nil {
		panic(err)
	}

	// Run `apt-get update` inside chroot
	log.Info("Updating apt cache.")
	runCommand("apt-get", "update")

	// Run `apt-get install auditd -y` inside chroot
	log.Info("Installing auditd.")
	runCommand("apt-get", "install", "auditd", "audispd-plugins", "-y")

	// Add the audit-admins group
	log.Info("Creating the audit-admins group.")
	runCommand("groupadd", "audit-admins", "--gid", strconv.Itoa(auditadminsGID))

	// Add the root user to the audit admins group. This may be unnecessary.
	log.Info("Adding the root user to the audit-admins group.")
	runCommand("usermod", "-aG", "audit-admins", "root")

	// When creating a system user, the uid is between 100 and 999, and the shell is /bin/false. Thus, we need to
	log.Info("Creating aks-auditd user in the audit-admins group.")
	runCommand("useradd", "--system", "--shell", "/usr/sbin/nologin", "--uid", strconv.Itoa(aksauditdUID), "--gid", "808", "--home", "/nonexistent", "aks-auditd")

	// Set /etc/audit to 751 permissions so the aks-audit group members can traverse to subdirectories, such as /etc/audit/rules.d
	// and /etc/audit/plugins.d, but not modify any other /etc/audit files.
	log.Info("Setting permissions on /etc/audit.")
	err = syscall.Chmod("/etc/audit", 0751)
	if err != nil {
		log.Fatal(err)
	}

	// Change ownership on the rules directory and all subfiles
	log.Info("Changing rules.d directory permissions.")
	runCommand("chgrp", "-R", "audit-admins", hostRulesDirectory) // Change the group to audit-admins
	runCommand("chmod", "-R", "g+rw", hostRulesDirectory)         // Give the group read/write/execute permissions TODO: I might not need execute permissions.
	runCommand("chmod", "g+s", hostRulesDirectory)                // Set the setgid bit so that new files inherit the group
	runCommand("rm", "-f", hostRulesDirectory+"/*")               // Clear out the rules directory

	runCommand("rm", "-f", hostPluginsDirectory+"/*") // Clear out the plugins directory. Only use those supplied by the container.

	// Check if the aks-auditd-monitor service is running. If we get an "active" response back, we want to stop the service so our binaries can be updated in later steps.
	aksMonitorServiceStatus := exec.Command("systemctl", "is-active", "aks-auditd-monitor")
	output, err := aksMonitorServiceStatus.CombinedOutput()
	if err != nil {
		log.Errorf("Failed to check if aks-auditd-monitor service is running. Updating aks-auditd-monitor may fail. Error: %v", err)
	} else if strings.TrimSpace(string(output)) == "active" {
		log.Info("Stopping aks-auditd-monitor service.")
		runCommand("systemctl", "stop", "aks-auditd-monitor")
	} else {
		log.Debug("aks-auditd-monitor service is not running or does not exist.")
	}

	// Exit from the chroot
	if err := exit(); err != nil {
		panic(err)
	}

	// Copy the aks-auditd-monitor binary and service file to the host file system. We do this outside the chroot because we need to copy from the container to the host.
	// When we restart a deployment, the updated binaries are copied over to the host file system. We check to make sure the associated service is stopped above.
	// Copy over the aks-audit-monitor binary to the host file system
	aksMonitorBinaryContainerPath := chrootMount + aksAuditdMonitorBinaryPath
	log.Info("Copying aks-auditd-monitor binary and service file to the host file system.")
	if err := copyFile("/app/aks-auditd-monitor", aksMonitorBinaryContainerPath); err != nil {
		log.Error(fmt.Sprintf("Failed to copy file: %s to %s, error: %v", "/app/aks-auditd-monitor", aksMonitorBinaryContainerPath, err))
	}
	if err := os.Chmod(aksMonitorBinaryContainerPath, 0755); err != nil {
		log.Error(fmt.Sprintf("Failed to set permissions on file: %s, error: %v", aksMonitorBinaryContainerPath, err))
	}
	if err := os.Chown(aksMonitorBinaryContainerPath, 0, 0); err != nil {
		log.Error(fmt.Sprintf("Failed to set ownership on file: %s, error: %v", aksMonitorBinaryContainerPath, err))
	}

	// Copy over the aks-auditd-monitor service file to the host file system
	aksMonitorServiceContainerPath := chrootMount + aksAuditdMonitorServicePath
	if err := copyFile("/app/aks-auditd-monitor.service", aksMonitorServiceContainerPath); err != nil {
		log.Error(fmt.Sprintf("Failed to copy file: %s to %s, error: %v", "/app/aks-auditd-monitor.service", aksMonitorServiceContainerPath, err))
	}
	if err := os.Chmod(aksMonitorServiceContainerPath, 0644); err != nil {
		log.Error(fmt.Sprintf("Failed to set permissions on file: %s, error: %v", aksMonitorServiceContainerPath, err))
	}
	if err := os.Chown(aksMonitorServiceContainerPath, 0, 0); err != nil {
		log.Error(fmt.Sprintf("Failed to set ownership on file: %s, error: %v", aksMonitorServiceContainerPath, err))
	}

	// Copy over the syslog.conf file to the host file system and set the appropriate permissions.
	// auditd is sensitive about the permissions and ownership on this file.
	pluginsContainerPath := chrootMount + hostPluginsDirectory
	syslogConfPath := pluginsContainerPath + "/syslog.conf"
	if err := copyFile(syslogConfPath, pluginsContainerPath); err != nil {
		log.Error(fmt.Sprintf("Failed to copy file: %s to %s, error: %v", syslogConfPath, aksMonitorServiceContainerPath, err))
	}
	if err := os.Chmod(syslogConfPath, 0600); err != nil {
		log.Error(fmt.Sprintf("Failed to set permissions on file: %s, error: %v", aksMonitorServiceContainerPath, err))
	}
	if err := os.Chown(syslogConfPath, 0, 0); err != nil {
		log.Error(fmt.Sprintf("Failed to set ownership on file: %s, error: %v", aksMonitorServiceContainerPath, err))
	}

	// Chroot to the host file system to configure the aks-auditd-monitor service and start it
	exit, err = Chroot(chrootMount)
	if err != nil {
		panic(err)
	}

	// At this point, we can configure the aksAuditdMonitorService to start on boot and start the service.
	runCommand("systemctl", "daemon-reload")
	runCommand("systemctl", "enable", "aks-auditd-monitor")
	runCommand("systemctl", "start", "aks-auditd-monitor")

	// Exit from the chroot
	if err := exit(); err != nil {
		panic(err)
	}

}

// Chroot changes the root directory of the current process to the specified path
func Chroot(path string) (func() error, error) {
	root, err := os.Open("/")
	if err != nil {
		return nil, err
	}

	if err := unix.Chroot(path); err != nil {
		root.Close()
		return nil, err
	}

	return func() error {
		defer root.Close()
		if err := root.Chdir(); err != nil {
			return err
		}
		return unix.Chroot(".")
	}, nil
}

// runCommand runs a command and logs the output
func runCommand(cmd string, args ...string) {

	// Create the command
	command := exec.Command(cmd, args...)

	log.Debugf("Running command: %s %s", cmd, strings.Join(args, " "))

	// Run the command and capture the output
	output, err := command.CombinedOutput()
	if err != nil {
		log.Errorf("Command failed with error: %s  Output: %s", err, string(output))
	}

	// Print the output
	log.Debugf("Command Output: %s", string(output))
}

// copyFile copies a file from src to dst
func copyFile(sourcePath, targetPath string) error {
	srcFile, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	targetFile, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer targetFile.Close()

	_, err = io.Copy(targetFile, srcFile)
	return err
}
