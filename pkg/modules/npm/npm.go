package npm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

type OutputInfo struct {
	Parent    *ModuleInfo
	BuildDir  string
	BuildType string
}

type ModuleInfo struct {
	Name       string
	Path       string
	Scripts    map[string][]OutputInfo
	workspaces []*ModuleInfo
}

type npmPackage struct {
	Name       string            `json:"name"`
	Workspaces []string          `json:"workspaces"`
	Scripts    map[string]string `json:"scripts"`
}

var buildTypeOutputs = map[string]string{
	"vite":       "dist",
	"storybook":  "storybook-static",
	"docusaurus": "build"}

var parser = syntax.NewParser()

func (s *ModuleInfo) ModulePath() string {
	return s.Path
}

func (s *ModuleInfo) ModuleType() string {
	return "NPM"
}

func (s *ModuleInfo) findScriptOutputs(script string) (res []OutputInfo, runOther []string, _ error) {
	fl, err := parser.Parse(strings.NewReader(script), "")

	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse script %s: %w, module: %s", script, err, s.Path)
	}

	syntax.Walk(fl, func(node syntax.Node) bool {
		if ce, ok := node.(*syntax.CallExpr); ok {
			buildType := ce.Args[0].Lit()

			if len(ce.Args) > 2 && buildType == "npm" && ce.Args[1].Lit() == "run" {
				var isWorkspaces bool
				var scriptName string

				for _, arg := range ce.Args[2:] {
					sa := arg.Lit()

					if strings.HasPrefix(sa, "--") {
						if sa == "--workspaces" {
							isWorkspaces = true
						}
					} else if scriptName == "" {
						scriptName = sa
					}
				}

				if isWorkspaces {
					for _, w := range s.workspaces {
						if outs, found := w.Scripts[scriptName]; found {
							res = append(res, outs...)
						}
					}

					return false
				}

				runOther = append(runOther, scriptName)

				return false
			}

			if outDir, ok := buildTypeOutputs[buildType]; ok {
				res = append(res, OutputInfo{
					Parent:    s,
					BuildDir:  filepath.Join(s.Path, outDir),
					BuildType: buildType,
				})
			}

			return false
		}

		return true
	})

	return res, runOther, nil
}

func normalizedWorkspaces(dir string, workspaces []string) ([]string, error) {
	normalizedWorkspaces := make([]string, 0, len(workspaces))

	for _, ws := range workspaces {
		if !strings.HasSuffix(ws, "/*") {
			normalizedWorkspaces = append(normalizedWorkspaces, filepath.Join(dir, ws))
			continue
		}

		scanDir := filepath.Join(dir, strings.TrimSuffix(ws, "/*"))
		wsDir, err := os.ReadDir(scanDir)

		if err != nil {
			return nil, fmt.Errorf("failed to read workspace dir %s: %w, module %s", ws, err, dir)
		}

		for _, dEntry := range wsDir {
			if !dEntry.IsDir() {
				continue
			}

			normalizedWorkspaces = append(normalizedWorkspaces, filepath.Join(scanDir, dEntry.Name()))
		}
	}

	return normalizedWorkspaces, nil
}

func (s *ModuleInfo) addCallOtherOutputs(callsOthers map[string][]string) {
	var needAdd func(scriptName string) (res []OutputInfo)

	needAdd = func(scriptName string) (res []OutputInfo) {
		for _, subscriptName := range callsOthers[scriptName] {
			res = append(res, s.Scripts[subscriptName]...)
			res = append(res, needAdd(subscriptName)...)
		}

		return res
	}

	for caller := range callsOthers {
		if outs := needAdd(caller); len(outs) > 0 {
			s.Scripts[caller] = append(s.Scripts[caller], outs...)
		}
	}
}

func newModuleInfo(dir string, pi *npmPackage) (*ModuleInfo, error) {
	res := &ModuleInfo{Name: pi.Name, Path: dir, Scripts: make(map[string][]OutputInfo)}
	normalizedWorkspaces, err := normalizedWorkspaces(dir, pi.Workspaces)

	if err != nil {
		return nil, fmt.Errorf("can't normalize workspaces: %w, module %s", err, dir)
	}

	for _, path := range normalizedWorkspaces {
		pi, err := readPackage(path)

		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("failed to check package %s: %w, workspace %s", dir, err, path)
		}

		wmi, err := newModuleInfo(path, pi)
		if err != nil {
			return nil, fmt.Errorf("failed to create module info: %w, path: %s", err, path)
		}

		res.workspaces = append(res.workspaces, wmi)
	}

	callsOthers := make(map[string][]string)

	for scriptName, script := range pi.Scripts {
		if sn, _, _ := strings.Cut(scriptName, ":"); sn != "build" {
			continue
		}

		outs, runOther, err := res.findScriptOutputs(script)
		if err != nil {
			return nil, fmt.Errorf("failed to find script %s outputs: %w, module %s", script, err, dir)
		}

		if len(runOther) > 0 {
			callsOthers[scriptName] = runOther
		}

		if len(outs) == 0 {
			continue
		}

		res.Scripts[scriptName] = append(res.Scripts[scriptName], outs...)
	}

	res.addCallOtherOutputs(callsOthers)

	return res, nil
}

func readPackage(path string) (*npmPackage, error) {
	fl, err := os.OpenFile(filepath.Join(path, "package.json"), os.O_RDONLY, os.ModePerm)
	if err != nil {
		return nil, err
	}

	var pi npmPackage

	if err = errors.Join(json.NewDecoder(fl).Decode(&pi), fl.Close()); err != nil {
		return nil, err
	}

	return &pi, nil
}

// FindNPMModules - поиск npm модулей в указанной директории.
func FindNPMModules(root string) ([]*ModuleInfo, error) {
	founds := make([]*ModuleInfo, 0)

	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if _, err := os.Stat(filepath.Join(path, ".npmrc")); err != nil {
				return nil
			}

			pi, err := readPackage(path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return nil
				}

				return fmt.Errorf("failed to check package %s: %w", path, err)
			}

			nm, err := newModuleInfo(path, pi)

			if err != nil {
				return fmt.Errorf("failed to create module info: %w, path: %s", err, path)
			}

			founds = append(founds, nm)

			return filepath.SkipDir
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return founds, nil
}
