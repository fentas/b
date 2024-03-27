package binary

import "os"

func GetBinaryPath() string {
	var path string

	if os.Getenv("PATH_BIN") != "" {
		path = os.Getenv("PATH_BIN")
	} else if os.Getenv("PATH_BASE") != "" {
		path = os.Getenv("PATH_BASE")
	} else if gitRoot, err := GetGitRootDirectory(); err == nil {
		path = gitRoot + "/.bin"
	}

	return path
}
