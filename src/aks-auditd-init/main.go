package main

import (
	"fmt"
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

// Container mount point where the host file system is mounted.
const chrootMount = "/node"

// Container mount point where auditd rules are stored.
const rulesMount = "/auditd-rules"

// Container mount point where the audisp-plugins are stored.
const pluginsMount = "/audisp-plugins"

// UID of the user the main container will run as - restarting the auditd service and copying config files to the node.
// Account is created with --system flag, which requires a UID between SYS_UID_MIN and SYS_UID_MAX, defined in /etc/login.defs
const aksauditdUID = 807

// GID of the audit admins group
const auditadminsGID = 808

func main() {
	// TODO: Some of these values can be removed here and from the yaml file as we don't use them all.
	// Set default config values
	viper.SetDefault("logLevel", "info")
	viper.SetDefault("rulesDirectory", "/etc/audit/rules.d")
	viper.SetDefault("pluginsDirectory", "/etc/audit/plugins.d")

	// Environment variable settings
	// NOTE: When using BindEnv with multiple, SetEnvPrefix does not apply and we must set it explicitly
	viper.SetEnvPrefix("AA")
	viper.BindEnv("logLevel", "AA_LOG_LEVEL")
	viper.BindEnv("rulesDirectory", "AA_RULES_DIR")
	viper.BindEnv("pluginsDirectory", "AA_PLUGINS_DIR")

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
	log.Info("Rules Directory: ", viper.GetString("rulesDirectory"))
	log.Info("Plugins Directory: ", viper.GetString("pluginsDirectory"))
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

	// Add the aks-auditd user to the audit-admins group
	log.Info("Adding the aks-auditd user to the audit-admins group.")
	runCommand("usermod", "-aG", "audit-admins", "aks-auditd")

	// Set /etc/audit to 751 permissions so the aks-audit group members can traverse to subdirectories, such as /etc/audit/rules.d
	// and /etc/audit/plugins.d, but not modify any other /etc/audit files.
	log.Info("Setting permissions on /etc/audit.")
	err = syscall.Chmod("/etc/audit", 0751)
	if err != nil {
		log.Fatal(err)
	}

	// TODO: I have some variable clean-up here to do. I also need to better protect these values as they come from a yaml file or environment variable, which can cause things to blow up.
	// Change ownership on the rules directory and all subfiles
	log.Info("Changing rules.d directory permissions.")
	runCommand("chgrp", "-R", "audit-admins", viper.GetString("rulesDirectory")) // Change the group to audit-admins
	runCommand("chmod", "-R", "g+rw", viper.GetString("rulesDirectory"))         // Give the group read/write/execute permissions TODO: I might not need execute permissions.
	runCommand("chmod", "g+s", viper.GetString("rulesDirectory"))                // Set the setgid bit so that new files inherit the group
	runCommand("rm", "-f", "/etc/audit/rules.d/*")                               // Clear out the rules directory

	// Change ownership on the plugins directory and all subfiles
	log.Info("Changing plugins.d directory permissions.")
	runCommand("chgrp", "-R", "audit-admins", viper.GetString("pluginsDirectory")) // Change the group to audit-admins
	runCommand("chmod", "-R", "g+rw", viper.GetString("pluginsDirectory"))         // Give the group read/write/execute permissions TODO: I might not need execute permissions.
	runCommand("chmod", "g+s", viper.GetString("pluginsDirectory"))                // Set the setgid bit so that new files inherit the group
	runCommand("rm", "-f", "/etc/audit/plugins.d/*")                               // Clear out the plugins directory

	// Give the aks-auditd user sudo privileges to restart the auditd service
	log.Info("Give auditd sudo privileges for the auditd service.")
	echo := exec.Command("echo", "aks-auditd ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart auditd")
	tee := exec.Command("tee", "/etc/sudoers.d/aks-auditd")

	// Pipe the echo command to the tee command.
	pipe, _ := echo.StdoutPipe()
	defer pipe.Close()
	tee.Stdin = pipe
	echo.Start()
	res, _ := tee.Output()

	log.Debug("Output: ", string(res))

	// Set proper ownership on the aks-auditd file
	log.Info("Set read-only privileges on the aks-auditd file for owner and group.")
	err = syscall.Chmod("/etc/sudoers.d/aks-auditd", 0440)
	if err != nil {
		log.Fatal(err)
	}

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

	// Run the command and capture the output
	output, err := command.CombinedOutput()
	if err != nil {
		log.Error(fmt.Sprintf("Command failed with error: %s  Output: %s", err, string(output)))
	}

	// Print the output
	log.Debug(fmt.Print(string(output)))
}
