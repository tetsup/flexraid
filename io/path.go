package io

import (
	"path/filepath"

	"github.com/tetsup/flexraid/storage"
)

func (layer *IO) filePath(key storage.FileKey) (string, error) {
	relative, err := key.Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(layer.root, filepath.FromSlash(relative)), nil
}
