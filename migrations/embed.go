package migrations

import (
	"embed"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

// FS contains the SQL migration files from the sql directory.
//
//go:embed all:sql
var FS embed.FS

func LatestVersion() (uint, error) {
	files, err := fs.ReadDir(FS, "sql")
	if err != nil {
		return 0, fmt.Errorf("read embedded migration directory: %w", err)
	}

	var latest uint64
	for _, file := range files {
		name := file.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		separator := strings.IndexByte(name, '_')
		if separator < 1 {
			return 0, fmt.Errorf("invalid migration filename %q", name)
		}
		version, err := strconv.ParseUint(name[:separator], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse migration version from %q: %w", name, err)
		}
		if version > latest {
			latest = version
		}
	}
	if latest == 0 {
		return 0, fmt.Errorf("no embedded up migrations found")
	}
	return uint(latest), nil
}
