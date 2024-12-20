package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// Container mount point where the host auditd rules.d directory is mounted.
const chrootRulesMount = "/auditd-rules-target"

// Container mount point where auditd rules are stored.
const rulesMount = "/auditd-rules"

// Map of source to target directories for copying files
type DirectoryPair struct {
	SourceDirectory string
	TargetDirectory string
}

func main() {

	// Set default config values
	viper.SetDefault("pollInterval", "30s")
	viper.SetDefault("logLevel", "info")

	// Environment variable settings
	// NOTE: When using BindEnv with multiple, SetEnvPrefix does not apply and we must set it explicitly
	viper.SetEnvPrefix("AA")
	viper.BindEnv("logLevel", "AA_LOG_LEVEL")
	viper.BindEnv("pollInterval", "AA_POLL_INTERVAL")

	// Output the configuration settings
	duration, err := time.ParseDuration(viper.GetString("pollInterval"))
	if err != nil {
		fmt.Println("Error parsing duration:", err)
		return
	}
	log.Info("Polling interval: ", duration)

	level, ok := levelMap[strings.ToLower(viper.GetString("logLevel"))]
	if !ok {
		log.Warn(fmt.Sprintf("Invalid log level: %s. Falling back to 'info' level logging.", viper.GetString("logLevel")))
		level = log.InfoLevel
	}
	log.Info("Log Level: ", viper.GetString("logLevel"))
	log.SetLevel(level)

	// Compare and sync the rules and plugins directories
	directories := []DirectoryPair{
		{
			SourceDirectory: rulesMount,
			TargetDirectory: chrootRulesMount,
		},
	}

	// Run the main loop
	for {
		for _, pair := range directories {
			sourceDir := pair.SourceDirectory
			targetDir := pair.TargetDirectory
			requiresReload, err := compareAndSyncDirectories(sourceDir, targetDir)
			if err != nil {
				log.Errorf("Error syncing directories: %v", err)
			}

			if requiresReload { // Reload is handled by the aks-auditd-monitor service
				log.Info("Differences found. Auditd rules/plugins require reload.")
			}
		}
		time.Sleep(viper.GetDuration("pollInterval"))
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

func compareAndSyncDirectories(sourceDir, targetDir string) (bool, error) {

	log.Debug("Comparing directories: ", sourceDir, " and ", targetDir)
	requiresReload := false
	hashesSource, err := getFileHashes(sourceDir)
	if err != nil {
		log.Warn(fmt.Sprintf("Error getting file hashes for %s: %v", sourceDir, err))
		return false, err
	}

	// Iterating through the fileHashes map
	for fileName, hashValue := range hashesSource {
		log.Debug(fmt.Sprintf("Source Dir File: %s, Hash: %x", fileName, hashValue))
	}

	hashesTarget, err := getFileHashes(targetDir)
	if err != nil {
		log.Warn(fmt.Sprintf("Error getting file hashes for %s: %v", targetDir, err))
		return false, err
	}

	// Iterating through the fileHashes map
	for fileName, hashValue := range hashesTarget {
		log.Debug(fmt.Sprintf("Target Dir File: %s, Hash: %x", fileName, hashValue))
	}

	if needSync(hashesSource, hashesTarget) {
		log.Info("Directories differ. Syncing...")
		if err := syncDirectories(sourceDir, targetDir); err != nil {
			log.Error(fmt.Sprintf("Error syncing directories: %v", err))
			return false, err
		}
		requiresReload = true
	} else {
		log.Debug("Directories are in sync.")
		requiresReload = false
	}

	return requiresReload, nil
}

// getFileHashes reads the directory and returns a map of file names to their SHA-256 hashes
func getFileHashes(dir string) (map[string][32]byte, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	fileHashes := make(map[string][32]byte)
	for _, file := range files {
		if file.Name() == "..data" {
			continue
		}
		if !file.IsDir() {
			fullFilePath := filepath.Join(dir, file.Name())
			hash, err := getFileHash(fullFilePath)
			if err != nil {
				log.Warn(fmt.Sprintf("Failed calculating hash for file %s: %v", fullFilePath, err))
				continue
			}
			relPath, err := filepath.Rel(dir, fullFilePath)
			if err != nil {
				log.Error(err)
				return nil, err
			}
			// the hash key is the full path to the file, less the base directory.
			fileHashes[relPath] = hash
		}
	}
	return fileHashes, nil
}

// getFileHash calculates the SHA-256 hash of a file
func getFileHash(filePath string) ([32]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return [32]byte{}, err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return [32]byte{}, err
	}

	return sha256.Sum256(hasher.Sum(nil)), nil
}

// needSync determines if the directories need to be synchronized
func needSync(hashesDir1, hashesDir2 map[string][32]byte) bool {
	for file, hash1 := range hashesDir1 {
		fileName := filepath.Base(file)
		if hash2, exists := hashesDir2[fileName]; exists {
			if hash1 != hash2 {
				return true
			}
		} else {
			return true
		}
	}

	for file := range hashesDir2 {
		if _, exists := hashesDir1[file]; !exists {
			return true
		}
	}

	return false
}

// // syncDirectories removes all files from dir2 and copies all files from dir1 to dir2
func syncDirectories(sourceDir, destDir string) error {

	// Remove all files from destDir
	destFiles, err := os.ReadDir(destDir)
	if err != nil {
		return err
	}

	// Iterate through files and delete each one
	for _, file := range destFiles {
		filePath := filepath.Join(destDir, file.Name())
		err := os.Remove(filePath)
		if err != nil {
			log.Warn(fmt.Sprintf("Failed to delete file: %s Error: %v", filePath, err))
			continue
		}
		log.Debug(fmt.Sprintf("Deleted file: %s", filePath))
	}

	// Copy all files from srcDir to destDir
	files, err := os.ReadDir(sourceDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.Name() == "..data" {
			continue
		}

		if file.IsDir() {
			continue
		}

		srcPath := filepath.Join(sourceDir, file.Name())
		destPath := filepath.Join(destDir, file.Name())

		if err := copyFile(srcPath, destPath); err != nil {
			log.Warn(fmt.Sprintf("Failed to copy file: %s to %s, error: %v", srcPath, destPath, err))
		}

		log.Debug(fmt.Sprintf("Copied file %s to %s", srcPath, destPath))
	}

	return nil
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
