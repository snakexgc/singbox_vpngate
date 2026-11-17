package vpngateruntime

import (
	"embed"
	"os"
	"path/filepath"
)

//go:embed rules/*.srs
var bundledRules embed.FS

func seedRules(dir string) error {
	target := filepath.Join(dir, "rules")
	if err := os.MkdirAll(target, 0700); err != nil {
		return err
	}
	for _, name := range []string{"cn.srs", "gfw.srs"} {
		path := filepath.Join(target, name)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		data, err := bundledRules.ReadFile("rules/" + name)
		if err != nil {
			return err
		}
		if err = atomicWrite(path, data); err != nil {
			return err
		}
	}
	return nil
}
