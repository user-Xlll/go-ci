// Package gomod содержит функции для поиска go модулей
package gomod

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/user-Xlll/go-ci/pkg/util"
)

// ModuleInfo - структура для хранения информации о модуле.
type ModuleInfo struct {
	GoModDir     string
	MainSubPaths []string
	UseVendor    bool
}

// Modules - структура для хранения найденных модулей и расположения main пакетов в них.
type Modules struct {
	GoModules map[string]*ModuleInfo
	GoWorkDir string
}

// ErrPackageNameNotFound - ошибка говорит о том, что не найдено имя пакета в go файле.
var ErrPackageNameNotFound = errors.New("package name not found")

var igoneDirForMainSearch = []string{"testdata", "examples", "example"}

func (s *ModuleInfo) ModulePath() string {
	return s.GoModDir
}
func (s *ModuleInfo) ModuleType() string {
	return "GO"
}

func packageName(filePath string) (string, error) {
	ast, err := parser.ParseFile(token.NewFileSet(), filePath, nil, parser.PackageClauseOnly)
	if err != nil {
		return "", fmt.Errorf("parse file error: %w", err)
	}

	if ast.Name == nil {
		return "", fmt.Errorf("%w: %s", ErrPackageNameNotFound, filePath)
	}

	return ast.Name.Name, nil
}

func isIgnoredPath(path string) bool {
	return slices.ContainsFunc(strings.Split(path, string(filepath.Separator)),
		func(s string) bool {
			return slices.Contains(igoneDirForMainSearch, s)
		})
}

func useMainRelPath(curDir, fileDir, path string, use func(relPath string)) error {
	var pkgName string

	relPath, err := filepath.Rel(curDir, fileDir)
	if err != nil {
		return fmt.Errorf("rel path error: %w", err)
	}

	if pkgName, err = packageName(path); err != nil ||
		pkgName != "main" ||
		isIgnoredPath(relPath) { // ignoring this folders for search main package
		return err
	}

	use(relPath)

	return nil
}

// FindGoModules - поиск go модулей в указанной директории.
func FindGoModules(root string) (*Modules, error) {
	res := &Modules{GoModules: map[string]*ModuleInfo{}}

	var lastDir string

	err := filepath.WalkDir(root, func(path string, dEntry fs.DirEntry, dErr error) error {
		if dErr != nil {
			return dErr
		}

		if dEntry.IsDir() {
			return res.visitDir(path, func(mi *ModuleInfo) error {
				absPath, err2 := filepath.Abs(path)
				if err2 != nil {
					return err2
				}

				mi.GoModDir = absPath
				res.GoModules[absPath] = mi

				return nil
			})
		}

		fileDir := filepath.Dir(path)

		if !strings.HasSuffix(dEntry.Name(), ".go") ||
			strings.HasSuffix(dEntry.Name(), "_test.go") ||
			lastDir == fileDir {
			return nil
		}

		lastDir = fileDir
		curDir := fileDir

		for ; strings.HasPrefix(curDir, root); curDir = filepath.Dir(curDir) {
			mi, found := res.GoModules[curDir]
			if !found {
				continue
			}

			return useMainRelPath(curDir, fileDir, path, func(relPath string) {
				mi.MainSubPaths = append(mi.MainSubPaths, relPath)
			})
		}

		return nil
	})

	if err != nil {
		return nil, err //nolint:wrapcheck // error is already wrapped
	}

	return res, nil
}

func (s *Modules) visitDir(path string, use func(*ModuleInfo) error) error {
	if ok, err := util.FileExists(filepath.Join(path, "go.mod")); err != nil {
		return err
	} else if !ok {
		return nil
	}

	if ok, err := util.FileExists(filepath.Join(path, "go.work")); err != nil {
		return err
	} else if ok && s.GoWorkDir == "" {
		s.GoWorkDir = path

		return nil
	}

	var resMi ModuleInfo

	if ok, err := util.DirExists(filepath.Join(path, "vendor")); err != nil {
		return err
	} else if ok {
		resMi.UseVendor = true
	}

	return use(&resMi)

}
