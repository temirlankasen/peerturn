package claude

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// CaptureResponderConfiguration is called only after the helper's direct
// parent PID has been authenticated against the native messaging socket.
// Linux proc supplies executable/argv facts; other platforms remain unqualified.
func CaptureResponderConfiguration(parentPID int, project string, environment []string) (*ResponderConfiguration, error) {
	failure := &ResponderError{Code: ConfigurationUnavailable}
	if runtime.GOOS != "linux" || parentPID <= 0 {
		return nil, failure
	}
	base := "/proc/" + strconv.Itoa(parentPID)
	executable, err := os.Readlink(base + "/exe")
	if err != nil || strings.HasSuffix(executable, " (deleted)") {
		return nil, failure
	}
	arguments, err := configurationProcessArguments(base + "/cmdline")
	if err != nil {
		return nil, err
	}
	sources, err := configurationParentSources(arguments)
	if err != nil {
		return nil, err
	}
	var home string
	for _, entry := range environment {
		if strings.HasPrefix(entry, "HOME=") {
			if home != "" {
				return nil, failure
			}
			home = strings.TrimPrefix(entry, "HOME=")
		}
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return nil, failure
	}
	project, err = filepath.EvalSymlinks(project)
	if err != nil {
		return nil, failure
	}
	parentDirectory, err := os.Readlink(base + "/cwd")
	if err != nil || parentDirectory != project {
		return nil, failure
	}
	configuration, err := responderConfiguration(executable, project, home, sources, environment)
	if err != nil {
		return nil, err
	}
	// Hash the running image too: hashing its former pathname alone could
	// otherwise accept an executable replaced after this parent started.
	// proc's executable is an absolute magic link, intentionally outside a
	// per-process os.Root. Its path is fixed from an authenticated positive PID.
	file, err := os.Open(filepath.Clean(base + "/exe"))
	if err != nil {
		return nil, failure
	}
	runningHash, hashErr := configurationBinaryFileHash(file)
	closeErr := file.Close()
	if hashErr != nil || closeErr != nil || runningHash != configuration.BinaryHash {
		return nil, failure
	}
	return &configuration, nil
}

func configurationProcessArguments(path string) ([]string, error) {
	failure := &ResponderError{Code: ConfigurationUnavailable}
	file, err := openResponderFile(path)
	if err != nil {
		return nil, failure
	}
	data, readErr := io.ReadAll(io.LimitReader(file, (32<<10)+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(data) == 0 || len(data) > 32<<10 || data[len(data)-1] != 0 {
		return nil, failure
	}
	var arguments []string
	for argument := range bytes.SplitSeq(data[:len(data)-1], []byte{0}) {
		arguments = append(arguments, string(argument))
	}
	return arguments, nil
}

func configurationParentSources(arguments []string) (string, error) {
	sources := "user,project,local"
	failure := &ResponderError{Code: ConfigurationUnavailable}
	if len(arguments) == 0 || arguments[0] == "" {
		return "", failure
	}
	seenSources := false
	for index := 1; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			break
		}
		if !strings.HasPrefix(argument, "-") {
			continue
		}
		key, value, inline := strings.Cut(argument, "=")
		switch key {
		case "--setting-sources", "--model", "--effort", "--resume", "-r", "--permission-mode", "--allowedTools",
			"--allowed-tools", "--disallowedTools", "--disallowed-tools", "--tools", "--add-dir", "--plugin-dir":
			if !inline {
				index++
				if index == len(arguments) {
					return "", failure
				}
				value = arguments[index]
			}
			if key == "--setting-sources" {
				if seenSources || (value != "" && value != "user,project,local") {
					return "", failure
				}
				seenSources, sources = true, value
			}
		case "--dangerously-skip-permissions", "--allow-dangerously-skip-permissions", "--continue", "-c", "--verbose",
			"--debug", "--no-chrome", "--chrome", "--disable-slash-commands":
			if inline {
				return "", failure
			}
		default:
			// In particular --settings, --fallback-model and provider beta
			// switches cannot silently change provider/account selection.
			return "", failure
		}
	}
	return sources, nil
}
