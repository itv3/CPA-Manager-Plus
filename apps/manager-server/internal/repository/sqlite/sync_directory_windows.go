//go:build windows

package sqlite

import (
	"errors"
	"os"
)

// Windows 的 os.File.Sync 不支持目录句柄；这里仍校验目标必须是现有目录。
func syncDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("sync target is not a directory")
	}
	return nil
}
