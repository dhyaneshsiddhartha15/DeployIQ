package rules

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/dockerfile"
)

// aptListsMB is what `apt-get update` leaves in /var/lib/apt/lists when the
// same layer does not remove it.
const aptListsMB = 40

func fileExists(root, name string) bool {
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
	return err == nil && info.Mode().IsRegular()
}

func dirExists(root, name string) bool {
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
	return err == nil && info.IsDir()
}

// cacheCleaned reports whether any RUN in the analysis contains one of the
// given cleanup commands.
func cacheCleaned(a *dockerfile.Analysis, cmds ...string) bool {
	if a == nil {
		return false
	}
	for _, r := range a.Runs {
		for _, c := range r.Commands {
			for _, want := range cmds {
				if strings.Contains(c.Text, want) {
					return true
				}
			}
		}
	}
	return false
}
