package claude

import (
	"os"
	"path/filepath"
	"strings"
)

// ProjectRootUnqualified refuses a broad or credential-bearing read boundary.
const ProjectRootUnqualified ResponderFailure = "project_root_unqualified"

var responderCredentialPaths = []string{
	".ssh", ".aws", ".kube", ".docker", ".netrc", ".git-credentials", ".codex", ".claude",
	".config/gh", ".config/gcloud", ".gnupg", ".azure", ".npmrc", ".pypirc",
	".local/share/keyrings", ".mozilla", ".config/google-chrome", ".config/chromium", ".peerturn",
	".local/share/peerturn", ".config/peerturn", ".cache/peerturn", ".zsh_sessions",
	"keyrings", "Library/Keychains",
	".bash_sessions", ".history", ".local/share/fish",
}

// QualifyResponderProjectRoot returns the canonical existing parent cwd. Home,
// its ancestors, system roots and credential directories cannot become a project
// read boundary. A specific temporary project remains valid; /tmp itself does not.
func QualifyResponderProjectRoot(project, home string) (string, error) {
	failure := &ResponderError{Code: ProjectRootUnqualified}
	if !validResponderPath(project) || !validResponderPath(home) {
		return "", failure
	}
	root, err := filepath.EvalSymlinks(project)
	if err != nil || !validResponderPath(root) {
		return "", failure
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", failure
	}
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil || !validResponderPath(canonicalHome) || responderPathContains(root, canonicalHome) {
		return "", failure
	}
	info, err = os.Stat(canonicalHome)
	if err != nil || !info.IsDir() {
		return "", failure
	}
	for _, system := range []string{
		"/", "/home", "/Users", "/Volumes", "/var", "/tmp", "/opt", "/private", "/mnt", "/media", "/run",
	} {
		canonical, resolveErr := responderResolvedPath(system)
		if root == system || resolveErr == nil && root == canonical {
			return "", failure
		}
	}
	for _, system := range []string{
		"/etc", "/usr", "/root", "/bin", "/sbin", "/lib", "/lib64", "/dev", "/proc", "/sys", "/boot",
		"/System", "/Library", "/var/lib", "/var/log", "/var/run", "/var/db", "/var/cache", "/var/spool",
	} {
		canonical, resolveErr := responderResolvedPath(system)
		if responderPathContains(system, root) || resolveErr == nil && responderPathContains(canonical, root) {
			return "", failure
		}
	}
	for _, protected := range responderCredentialPaths {
		if strings.Contains(filepath.ToSlash(root)+"/", "/"+protected+"/") {
			return "", failure
		}
		declared := filepath.Join(home, protected)
		canonical, resolveErr := responderResolvedPath(declared)
		if resolveErr != nil || !validResponderPath(canonical) {
			return "", failure
		}
		for _, target := range []string{declared, canonical} {
			if responderPathContains(root, target) || responderPathContains(target, root) {
				return "", failure
			}
		}
	}
	return root, nil
}

func responderPathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func responderProjectDenies(root string) []string {
	// ** also matches zero directories, covering the root itself and every
	// nested copy. Absolute rules bind the policy to the qualified cwd.
	root = "/" + filepath.ToSlash(root)
	deny := make([]string, 0, 2*len(responderCredentialPaths)+12)
	for _, path := range responderCredentialPaths {
		deny = append(deny, "Read("+root+"/**/"+path+")", "Read("+root+"/**/"+path+"/**)")
	}
	for _, pattern := range []string{".env*", ".*_history", "fish_history", ".config/fish/fish_history", ".claude.json*", ".git/config", "*.pem", "*.key", "id_rsa*", "id_ed25519*", "id_ecdsa*"} {
		deny = append(deny, "Read("+root+"/**/"+pattern+")")
	}
	return deny
}
