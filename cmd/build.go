package cmd

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	cp "github.com/otiai10/copy"

	"github.com/user-Xlll/go-ci/pkg/copycommands"
	"github.com/user-Xlll/go-ci/pkg/modules"
)

type platform struct {
	goos, goarch string
	filename     string
}

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build all main packages",
	Long:  `Build all main packages for different os/arch, and generate Dockefile on SCRATCH.`,
	RunE:  runBuildCmd,
}

// ErrNoTargets - не найдено ни одной цели для сборки.
var ErrNoTargets = errors.New("no targets for build")

// ErrInvalidPlatform - флаг platforms сожержит неправильное значение платформы.
var ErrInvalidPlatform = errors.New("invalid platform format")

var errPanic = errors.New("panic")
var platforms = []string{"windows/386", "windows/amd64", "linux/386", "linux/amd64", "darwin/amd64"}
var ldflags = "-s -w" // remove debug info and symbols
var tags string
var cgoEnabled bool
var dockerfilePlatfrom = "linux/amd64"
var mainPackageName string
var nginxImageHash string
var dockerRegistry = "docker.io/library/nginx"
var mountHTMLPath = "/opt/syngx/html/"
var copyCommands = copycommands.New()

func init() {
	rootCmd.AddCommand(buildCmd)
	flags := buildCmd.Flags()
	flags.StringArrayVarP(&platforms, "platforms", "p", platforms, "Platforms to build in format os/arch")
	flags.StringVarP(&ldflags, "ldflags", "f", ldflags, "Linker flags")
	flags.StringVarP(&tags, "tags", "t", tags, "Build tags")
	flags.BoolVarP(&cgoEnabled, "cgo", "c", cgoEnabled, "Enable CGO")
	flags.StringVarP(&dockerfilePlatfrom, "dockerfile-platform", "d", dockerfilePlatfrom,
		"Platform for Dockerfile, set 'off' if you don't need dockerfile")
	flags.StringVarP(&mainPackageName, "main-package", "m", "", "Set main package name instead of auto detected by dir name")
	flags.StringVarP(&dockerRegistry, "docker-registry", "r", dockerRegistry, "Docker registry")
	flags.StringVarP(&nginxImageHash, "nginx-image-hash", "i", nginxImageHash, "Nginx image hash")
	flags.StringVarP(&mountHTMLPath, "mount-html-path", "", mountHTMLPath, "Path to mount html")
	flags.VarP(&copyCommands, "copy-files", "", "Copy files to image.  format <src>:<dst> or <service>:<src>:<dst>")
}

func buildPackage(mainDir, outPath, goos, goarch string) error {
	var cge = "0"
	if cgoEnabled {
		cge = "1"
	}

	var errOut bytes.Buffer

	cmd := exec.Command("go", "build", "-o", outPath, "-ldflags", ldflags, "-tags", tags, ".") //nolint:gosec //ok
	cmd.Dir = mainDir

	cmd.Env = append(slices.DeleteFunc(os.Environ(), func(n string) bool {
		return strings.HasPrefix(n, "GOOS=") || strings.HasPrefix(n, "GOARCH=") || strings.HasPrefix(n, "CGO_ENABLED=")
	}), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED="+cge) //nolint:strconcat // not in cycle

	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build module %s failed: %w\n%s", mainDir, err, errOut.String())
	}

	return nil
}

func platformToBinatyName(platformName string) (*platform, error) {
	var (
		res   platform
		found bool
	)

	res.goos, res.goarch, found = strings.Cut(strings.ToLower(platformName), "/")
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrInvalidPlatform, platformName)
	}

	var ext = ""
	if res.goos == "windows" {
		ext = ".exe"
	}

	res.filename = fmt.Sprintf("app_%s_%s%s", res.goos, res.goarch, ext)

	return &res, nil
}

var runScriptCache = map[string]struct{}{}

func buildNPM(bi modules.BuildInfo) error {
	hashKey := bi.BuildDir + ":" + bi.BuildCommand
	if _, ok := runScriptCache[hashKey]; ok {
		log.Println("Already built", bi.BuildDir, "with command", bi.BuildCommand)
		return nil
	}

	runScriptCache[hashKey] = struct{}{}

	log.Println("Run command: npm run ", bi.BuildCommand, ", in directory", bi.BuildDir)
	cmd := exec.Command("npm", "run", bi.BuildCommand)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = bi.BuildDir

	return cmd.Run()
}

func buildTarget(name, outDir string, bi modules.BuildInfo) error {
	var eg errgroup.Group

	if bi.ModuleInfo.ModuleType() == "NPM" {
		return buildNPM(bi)
	}

	for _, platform := range platforms {
		eg.Go(func() error {
			pl, err2 := platformToBinatyName(platform)
			if err2 != nil {
				return err2
			}

			if err2 = buildPackage(bi.BuildDir, filepath.Join(outDir, pl.filename), pl.goos, pl.goarch); err2 != nil {
				return fmt.Errorf("can't build %s for platform %s: %w", name, platform, err2)
			}

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return err
	}

	return nil
}

func runBuildCmd(_ *cobra.Command, args []string) error {
	mr, err := modules.FindModules(workDir)
	if err != nil {
		return fmt.Errorf("can't find go modules: %w", err)
	}

	dstDir, err := getDestinationDir(args, "build-out-*")
	if err != nil {
		return err
	}

	if len(mr.Services) == 0 {
		return ErrNoTargets
	}

	for name, target := range mr.Services {
		outDir := filepath.Join(dstDir, name)

		log.Println("Build", name, "as", target.ModuleInfo.ModuleType())

		if err := os.Mkdir(outDir, defaultDirPerm); err != nil {
			return fmt.Errorf("can't create directory %s: %w", outDir, err)
		}

		if err := buildTarget(name, outDir, target); err != nil {
			return err
		}

		additionalCommands, err := prepareCopyCommands(name, outDir)
		if err != nil {
			return err
		}

		if err = writeDockerfile(outDir, target, additionalCommands); err != nil {
			return err
		}
	}

	log.Println("Done.")

	return nil
}

func prepareCopyCommands(service, outDir string) ([]string, error) {
	commands := []string{}
	for _, command := range slices.Concat(copyCommands[""], copyCommands[service]) {
		if err := cp.Copy(command.Src, filepath.Join(outDir, command.Src)); err != nil {
			return nil, err
		}
		commands = append(commands, command.String())
	}
	return commands, nil
}

func copyDirNoSymLink(src, dst string) error {
	opts := cp.Options{OnSymlink: func(link string) cp.SymlinkAction {
		realPath, err := filepath.EvalSymlinks(link)
		if err != nil { //nolint:errorreturn //ok
			return cp.Skip
		}

		path, err := filepath.Rel(src, realPath)
		if err != nil || strings.HasPrefix(path, "..") || filepath.Base(link) == "localtime" {
			return cp.Deep
		}

		return cp.Shallow
	}}

	if err := cp.Copy(src, dst, opts); err != nil {
		return fmt.Errorf("can't copy dir %s to %s: %w", src, dst, err)
	}

	return nil
}

func copyFile(src, dst string) (resErr error) {
	sf, err := os.OpenFile(src, os.O_RDONLY, defaultFilePerm) //nolint:gosec //checked
	if err != nil {
		return fmt.Errorf("can't open source file %s: %w", src, err)
	}

	defer func() {
		resErr = errors.Join(resErr, sf.Close())
	}()

	df, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, defaultFilePerm) //nolint:gosec //checked
	if err != nil {
		return fmt.Errorf("can't open destination file %s: %w", dst, err)
	}

	defer func() {
		resErr = errors.Join(resErr, df.Close())
	}()

	if _, err = io.Copy(df, sf); err != nil {
		return fmt.Errorf("can't copy %s: %w", src, err)
	}

	return nil
}

func writeDockerfile(dstDir string, bi modules.BuildInfo, additionalCommands []string) (resErr error) {
	if dockerfilePlatfrom == "" || strings.EqualFold(dockerfilePlatfrom, "off") {
		return nil
	}

	DockerfilePath := filepath.Clean(filepath.Join(dstDir, "Dockerfile"))

	if bi.ModuleInfo.ModuleType() == "NPM" {
		htmlDir := filepath.Join(dstDir, "html")

		if err := os.Rename(bi.BuildOutputDir, htmlDir); err != nil {
			return fmt.Errorf("can't move html dir from %s to %s: %w", bi.BuildOutputDir, htmlDir, err)
		}

		baseImage := dockerRegistry
		if nginxImageHash != "" {
			baseImage = fmt.Sprintf("%s@sha256:%s", dockerRegistry, nginxImageHash)
		}

		dockerFileData := []string{
			fmt.Sprintf("FROM %s", baseImage),
			fmt.Sprintf("COPY html/ %s", mountHTMLPath),
		}
		dockerFileData = append(dockerFileData, additionalCommands...)

		if err := os.WriteFile(DockerfilePath, []byte(strings.Join(dockerFileData, "\n")), defaultFilePerm); err != nil {
			return fmt.Errorf("can't write Dockerfile for htlm: %w", err)
		}

		return nil
	}

	platf, err := platformToBinatyName(dockerfilePlatfrom)
	if err != nil {
		return fmt.Errorf("bad dockerfilePlatfrom %s: %w", dockerfilePlatfrom, err)
	}

	log.Println("Writing Dockerfile for", dockerfilePlatfrom)

	if err = copyCerts(dstDir); err != nil {
		return err
	}

	if err = copyDirNoSymLink("/usr/share/zoneinfo", filepath.Join(dstDir, "zoneinfo")); err != nil {
		return err
	}

	if err = os.Mkdir(filepath.Join(dstDir, "emptydir"), defaultDirPerm); err != nil {
		return fmt.Errorf("can't create emptydir in %s: %w", dstDir, err)
	}

	etcDir := filepath.Join(dstDir, "etc")
	if err = os.Mkdir(etcDir, defaultDirPerm); err != nil {
		return fmt.Errorf("can't create etc dir: %w", err)
	}

	if err = os.WriteFile(filepath.Join(etcDir, "group"),
		[]byte("root:x:0:\napp:x:1000:"),
		defaultFilePerm); err != nil {
		return fmt.Errorf("can't write group file: %w", err)
	}

	if err = os.WriteFile(filepath.Join(etcDir, "passwd"),
		[]byte("root:x:0:0:root:/root:/sbin/nologin\napp:x:1000:1000:app:/app:/sbin/nologin"),
		defaultFilePerm); err != nil {
		return fmt.Errorf("can't write group file: %w", err)
	}

	file, err := os.OpenFile(DockerfilePath, os.O_CREATE|os.O_WRONLY, defaultFilePerm)
	if err != nil {
		return fmt.Errorf("can't create Dockerfile: %w", err)
	}

	defer func() {
		if r := recover(); r != nil {
			if errP, ok := r.(error); ok {
				resErr = errors.Join(resErr, errP)
			} else {
				resErr = errors.Join(resErr, fmt.Errorf("%w:%v", errPanic, r))
			}
		}

		resErr = errors.Join(resErr, file.Close())
	}()

	writer := bufio.NewWriter(file)

	write := func(s ...string) { //nolint:mustpanic //ok
		for _, str := range s {
			if _, err = writer.WriteString(str); err != nil {
				panic(err)
			}
		}
	}

	write("FROM --platform=", dockerfilePlatfrom, " scratch\n")
	write("COPY /etc /etc\n")
	write("COPY --chown=app:app ", platf.filename, " /app/service\n")
	write("COPY --chown=app:app certs/ /etc/ssl/certs/\n")
	write("COPY --chown=app:app zoneinfo/ /usr/share/zoneinfo/\n")
	write("COPY --chown=app:app emptydir/ /tmp/\n")
	for _, command := range additionalCommands {
		write(fmt.Sprintf("%s\n", command))
	}
	write("USER app\n")
	write(`ENTRYPOINT ["/app/service"]`)

	err = writer.Flush()
	if err != nil {
		return fmt.Errorf("can't flush Dockerfile: %w", err)
	}

	return nil
}

func copyCerts(dstDir string) (resErr error) {
	list, err := os.ReadDir("/etc/ssl/certs")
	if err != nil {
		return fmt.Errorf("can't read /etc/ssl/certs: %w", err)
	}

	certsDst := filepath.Join(dstDir, "certs")
	if err = os.Mkdir(certsDst, defaultDirPerm); err != nil {
		return fmt.Errorf("can't create certs dir: %w", err)
	}

	for _, de := range list {
		if filepath.Ext(de.Name()) != ".crt" || de.IsDir() {
			continue
		}

		if err = copyFile(filepath.Join("/etc/ssl/certs", de.Name()),
			filepath.Join(certsDst, de.Name())); err != nil {
			return err
		}
	}

	return nil
}
