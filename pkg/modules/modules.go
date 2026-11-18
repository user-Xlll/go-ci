package modules

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/user-Xlll/go-ci/pkg/modules/gomod"
	"github.com/user-Xlll/go-ci/pkg/modules/npm"
)

type IModuleInfo interface {
	ModulePath() string
	ModuleType() string
}

type BuildInfo struct {
	ModuleInfo     IModuleInfo
	BuildDir       string
	BuildCommand   string
	BuildOutputDir string
}

type Modules struct {
	GOModules        []*gomod.ModuleInfo
	GoWorkDir        string
	NPMModules       []*npm.ModuleInfo
	Services         map[string]BuildInfo
	Module2ShortName map[IModuleInfo]string
}

func (s *Modules) ModuleShortName(m IModuleInfo) string {
	if r, ok := s.Module2ShortName[m]; ok {
		return r
	}

	panic(fmt.Sprintf("ModuleShortName: unknown module %s", m.ModulePath()))
}

// MakeShortNames - генерирует уникальные имена модулей по их пути.
func MakeShortNames(fullPaths []string) map[string]string {
	res := make(map[string]string, len(fullPaths))
	shortNames := make(map[string][]string, len(fullPaths))
	var trimSuffix string

	for {
		for _, path := range fullPaths {
			name := filepath.Base(path)
			shortNames[name] = append(shortNames[name], filepath.Join(path, trimSuffix))
		}

		if len(shortNames) == 1 && len(fullPaths) > 1 {
			trimSuffix = filepath.Join(filepath.Base(fullPaths[0]), trimSuffix)
			for i, v := range fullPaths {
				fullPaths[i] = filepath.Dir(v) //trim same suffixes

			}

			shortNames = make(map[string][]string, len(fullPaths))
		} else {
			break
		}
	}

	for name, paths := range shortNames {
		if len(paths) == 1 {
			res[paths[0]] = name

			continue
		}

		dirs := make([]string, len(paths))

		for i, path := range paths {
			d := filepath.Dir(path)
			dirs[i] = d
		}

		for path2, name2 := range MakeShortNames(dirs) {
			var newname string

			if name2 == "." {
				newname = name
			} else {
				newname = fmt.Sprintf("%s_%s", name, name2)
			}

			res[filepath.Join(path2, name)] = newname
		}
	}

	return res
}

func FindModules(root string) (*Modules, error) {
	var eg errgroup.Group

	res := Modules{Services: make(map[string]BuildInfo), Module2ShortName: make(map[IModuleInfo]string)}

	eg.Go(func() error {
		gomod, err := gomod.FindGoModules(root)
		if err != nil {
			return err
		}

		res.GOModules, res.GoWorkDir = slices.Collect(maps.Values(gomod.GoModules)), gomod.GoWorkDir

		return nil
	})

	eg.Go(func() error {
		np, err := npm.FindNPMModules(root)
		if err != nil {
			return err
		}
		res.NPMModules = np

		return nil
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	virtualPaths := make(map[string]BuildInfo)

	for _, m := range res.GOModules {
		for _, sb := range m.MainSubPaths {

			dir := filepath.Join(m.GoModDir, sb)

			virtualPaths[filepath.Join(m.GoModDir, sb)] = BuildInfo{ModuleInfo: m,
				BuildDir:       dir,
				BuildCommand:   "build",
				BuildOutputDir: dir}
		}
	}

	for _, m := range res.NPMModules {
		for cmd, outs := range m.Scripts {
			for _, out := range outs {
				virtualPath := filepath.Join(filepath.Dir(out.Parent.Path), out.BuildType, filepath.Base(out.Parent.Path))

				virtualPaths[virtualPath] = BuildInfo{ModuleInfo: m,
					BuildDir:       m.ModulePath(),
					BuildOutputDir: out.BuildDir,
					BuildCommand:   cmd}
			}
		}
	}

	for path, short := range MakeShortNames(slices.Collect(maps.Keys(virtualPaths))) {
		res.Services[short] = virtualPaths[path]
	}

	allModules := make(map[string]IModuleInfo)

	for _, m := range res.GOModules {
		allModules[m.GoModDir] = m
	}

	for _, m := range res.NPMModules {
		allModules[m.Path] = m
	}

	for path, short := range MakeShortNames(slices.Collect(maps.Keys(allModules))) {
		res.Module2ShortName[allModules[path]] = short
	}

	return &res, nil
}
