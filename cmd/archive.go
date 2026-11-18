package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/user-Xlll/go-ci/pkg/replacereader"
	"github.com/user-Xlll/go-ci/pkg/util"
)

var gzipCmd = &cobra.Command{
	Use:   "archive",
	Short: "make tgz arhive with work folder",
	Long:  `replace all place holders to their values for each file in directory, and write result to tgz archive`,
	RunE:  runArchive,
}

var replaceFileMask = []string{"*.yaml", "*.yml"}
var OutputFile string
var OutputNexus string
var irnoreMasksFile string = ".helmignore"
var gzipComment string
var nexusUrlRe = regexp.MustCompile(`(?i)^(?:https?:\/\/)?(?<host>[a-zA-Z._\-0-9]+)\/(?<repo>[a-zA-Z._\-0-9]+)\/(?<group>[a-zA-Z._\-0-9]+)\/(?<artifact>[a-zA-Z._\-0-9]+):(?<version>[a-zA-Z._\-0-9]+)$`)
var username string
var pass string
var uploadTimeout = 10 * time.Minute
var failIfNotReplaced = true
var ErrNoReplaced = errors.New("no replacements for rules")
var ReplaceRulesJSON string
var WrapDir string

func init() {
	rootCmd.AddCommand(gzipCmd)
	gzipCmd.Flags().StringSliceVarP(&replaceFileMask, "replace-file-mask", "m", replaceFileMask,
		"Matching files for replace place holders")
	gzipCmd.Flags().StringVarP(&OutputFile, "out-file", "f", "", "output file path")
	gzipCmd.Flags().StringVarP(&OutputNexus, "out-nexus", "n", "", "output nexus url")
	gzipCmd.Flags().StringVarP(&gzipComment, "comment", "c", "", "Comment for tgz archive")
	gzipCmd.Flags().StringVarP(&irnoreMasksFile, "ignore-masks-file", "i", irnoreMasksFile, "File name with ignore masks")
	gzipCmd.Flags().StringVarP(&username, "user", "u", "", "user for nexus")
	gzipCmd.Flags().StringVarP(&pass, "password", "p", "", "password for nexus")
	gzipCmd.Flags().DurationVarP(&uploadTimeout, "timeout", "t", uploadTimeout, "timeout for upload to nexus")
	gzipCmd.Flags().BoolVarP(&failIfNotReplaced, "fail-if-not-replaced", "F", failIfNotReplaced,
		"fail if was not replaced any place holders")
	gzipCmd.Flags().StringVarP(&ReplaceRulesJSON, "replace-rules", "r", ReplaceRulesJSON, "JSON object as string with regexp to template replace rules")
	gzipCmd.Flags().StringVarP(&WrapDir, "wrapdir", "d", "", "directory name inside archive")
}

func readFile(path string, use func(io.Reader) error) (resErr error) {
	file, err := os.OpenFile(path, os.O_RDONLY, defaultFilePerm)
	if err != nil {
		return err
	}

	defer func() {
		if err := file.Close(); err != nil {
			resErr = errors.Join(resErr, fmt.Errorf("can't close file %s: %w", path, err))
		}
	}()

	return use(file)
}

func prepareReplaceRules() ([]replacereader.Rule, error) {
	if ReplaceRulesJSON == "" {
		return nil, nil
	}

	m := make(map[string]string)
	if err := json.Unmarshal([]byte(ReplaceRulesJSON), &m); err != nil {
		return nil, fmt.Errorf("bad replace-rules json: %w", err)
	}

	res := make([]replacereader.Rule, 0, len(m))

	for k, v := range m {
		re, err := regexp.Compile(k)
		if err != nil {
			return nil, fmt.Errorf("bad regexp %s in replace-rules: %w", k, err)
		}

		subNames := re.SubexpNames()

		templ, err := util.RenderTemplate(v, func(varName string) (string, bool) {
			if slices.Contains(subNames, varName) {
				return fmt.Sprintf("${%s}", varName), true
			}

			return os.LookupEnv(varName)
		})

		if err != nil {
			return nil, fmt.Errorf("bad template %s in replace-rules: %w", v, err)
		}
		res = append(res, replacereader.Rule{Re: re, Template: templ})
	}

	return res, nil
}

type statKey struct {
	From, To string
}

func runArchive(_ *cobra.Command, _ []string) (resErr error) {
	rules, err := prepareReplaceRules()
	if err != nil {
		return fmt.Errorf("bad replace rules %s: %w", ReplaceRulesJSON, err)
	}

	if (OutputNexus == "" && OutputFile == "") || (OutputNexus != "" && OutputFile != "") {
		return fmt.Errorf("output file or nexus url must be specified")
	}

	if OutputFile != "" {
		log.Println("process directory", workDir, "to file", OutputFile)

		file, err := os.OpenFile(OutputFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, defaultFilePerm)
		if err != nil {
			return fmt.Errorf("can't create output file %s: %w", OutputFile, err)
		}

		defer func() {
			if err := file.Close(); err != nil {
				resErr = errors.Join(resErr, fmt.Errorf("can't close output file %s: %w", OutputFile, err))
			}
		}()

		return tarGzip(file, rules)
	}

	log.Println("process directory", workDir, "to nexus", OutputNexus)
	ctx, cancel := context.WithTimeout(context.Background(), uploadTimeout)
	defer cancel()
	pr, pw := io.Pipe()

	errC := make(chan error, 1)
	go func() {
		defer close(errC)
		defer pr.Close()
		errC <- upload2nexus(ctx, pr)
	}()

	err2 := tarGzip(pw, rules)
	if err2 != nil {
		cancel()
	}
	if err := pw.Close(); err != nil {
		return fmt.Errorf("can't close tar writer: %w", err)
	}

	err1 := <-errC

	if err1 != nil || err2 != nil {
		return errors.Join(err1, err2)
	}

	return nil
}

func tarGzip(dst io.Writer, rules []replacereader.Rule) (resErr error) {
	gzipWriter := gzip.NewWriter(dst)
	gzipWriter.Comment = gzipComment

	defer func() {
		if err := gzipWriter.Close(); err != nil {
			resErr = errors.Join(resErr, fmt.Errorf("can't close gzip writer: %w", err))
		}
	}()

	tarWriter := tar.NewWriter(gzipWriter)
	defer func() {
		if err := tarWriter.Close(); err != nil {
			resErr = errors.Join(resErr, fmt.Errorf("can't close tar writer: %w", err))
		}
	}()

	var ignoreMasks []string
	var ignoreIfRootDir string
	var replaceStat = make(map[statKey][]string)
	var ruleMatchCount = make(map[*regexp.Regexp]int)

	walkErr := filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if irnoreMasksFile == "" || strings.ToUpper(irnoreMasksFile) == "OFF" {
				return nil
			}

			ignFile := filepath.Join(path, irnoreMasksFile)
			if helmIgnoreData, err := os.ReadFile(ignFile); err != nil {
				if !os.IsNotExist(err) {
					return fmt.Errorf("can't read %s file: %w", irnoreMasksFile, err)
				}
			} else {
				log.Println("found file with ignore masks:", ignFile)

				ignoreMasks = slices.DeleteFunc(strings.Split(string(helmIgnoreData), "\n"), func(e string) bool {
					e = strings.TrimSpace(e)

					return strings.HasPrefix(e, "#") || e == ""
				})

				ignoreIfRootDir = path + string(os.PathSeparator)
			}

			return nil
		}

		if ignoreMasks != nil && !strings.HasPrefix(path, ignoreIfRootDir) {
			ignoreMasks = nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		relPaht := strings.TrimPrefix(path, workDir+"/")

		if WrapDir != "" {
			relPaht = filepath.Join(WrapDir, relPaht)
		}

		for _, mask := range ignoreMasks {
			matched, err := util.FilePathMatch(mask, strings.TrimPrefix(path, ignoreIfRootDir+"/"))
			if err != nil {
				return fmt.Errorf("chek mask %s error: %w", mask, err)
			}

			if matched {
				return nil
			}
		}

		header, err := tar.FileInfoHeader(info, "")
		header.Name = relPaht

		return readFile(path, func(r io.Reader) error {
			needReplaces := false

			for _, mask := range replaceFileMask {
				if matched, err := util.FilePathMatch(mask, relPaht); err != nil {
					return fmt.Errorf("check mask %s error: %w", mask, err)
				} else if matched {
					needReplaces = true

					break
				}
			}

			if needReplaces {
				log.Println("replace in", relPaht)
				reader := replacereader.NewReplaceReader(r, rules,
					func(lineNum int, oldValue, newValue string, rule *replacereader.Rule) {
						key := statKey{From: oldValue, To: newValue}
						replaceStat[key] = append(replaceStat[key], fmt.Sprintf("%s:%d", relPaht, lineNum))
						ruleMatchCount[rule.Re]++
						log.Printf("replaced %s to %s at line %d", oldValue, newValue, lineNum)
					})
				buf, err := io.ReadAll(reader)
				if err != nil {
					return err
				}

				header.Size = int64(len(buf))
				r = bytes.NewReader(buf)
			}

			if err = tarWriter.WriteHeader(header); err != nil {
				return fmt.Errorf("can't write tar header for %s: %w", relPaht, err)
			}

			_, err = io.Copy(tarWriter, r)
			if err != nil {
				return fmt.Errorf("can't write tar body for %s: %w", relPaht, err)
			}

			return nil
		})
	})

	if walkErr != nil {
		return walkErr
	}

	if failIfNotReplaced {
		var noReplaces []string

		for _, rule := range rules {
			if ruleMatchCount[rule.Re] == 0 {
				noReplaces = append(noReplaces, rule.Re.String())
			}
		}

		if len(noReplaces) > 0 {
			return fmt.Errorf("%w: %s", ErrNoReplaced, strings.Join(noReplaces, ","))
		}
	}

	return printReport(replaceStat)
}

func printReport(replaceStat map[statKey][]string) error {
	type reportRow struct {
		From, To string
		Lines    []string
	}

	report := make([]reportRow, 0, len(replaceStat))

	for jsonKey, jsonValue := range replaceStat {
		report = append(report, reportRow{From: jsonKey.From, To: jsonKey.To, Lines: jsonValue})
	}

	ec := json.NewEncoder(os.Stdout)
	ec.SetIndent("", "  ")

	return ec.Encode(report)
}

func upload2nexus(ctx context.Context, data io.Reader) (resErr error) {
	vals := nexusUrlRe.FindStringSubmatch(OutputNexus)
	if len(vals) != 6 {
		return fmt.Errorf("invalid nexus url: %s, right format: %v", OutputNexus, nexusUrlRe)
	}

	if username == "" || pass == "" {
		return fmt.Errorf("username or password is empty")
	}

	host, repo, group, artifact, version := vals[1], vals[2], vals[3], vals[4], vals[5]
	bodyR, bodyW := io.Pipe()
	defer bodyR.Close()

	mpw := multipart.NewWriter(bodyW)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("https://%s/service/rest/v1/components?repository=%s", host, repo), bodyR)
	if err != nil {
		return fmt.Errorf("build request error: %w", err)
	}

	req.Header.Set("Content-Type", mpw.FormDataContentType())
	req.SetBasicAuth(username, pass)

	var bodyErr error
	defer func() {
		if bodyErr != nil {
			resErr = errors.Join(bodyErr, resErr)
		}
	}()

	go func() {
		defer bodyW.Close()
		defer func() {
			if ctx.Err() != nil {
				return
			}

			mpw.Close() //commit only if no cancel
		}()

		mpw.WriteField("generate-pom", "true")
		mpw.WriteField("groupId", group)
		mpw.WriteField("artifactId", artifact)
		mpw.WriteField("packaging", "tar.gz")
		mpw.WriteField("version", version)
		mpw.WriteField("asset0.extension", "tgz")
		mpw.WriteField("asset0.classifier", "distrib")
		wr, err := mpw.CreateFormFile("asset0", "distributive.tgz")
		if err != nil {
			bodyErr = fmt.Errorf("create form field error: %w", err)
		}

		if _, err2 := io.Copy(wr, data); err2 != nil {
			if ctx.Err() == nil {
				bodyErr = fmt.Errorf("write body error: %w", err2)
			}

			return
		}
		//	fmt.Println(len(buf.Bytes()))
	}()

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request to nexus error: %w", err)

	}

	defer res.Body.Close()

	if res.StatusCode != http.StatusNoContent {
		resp, _ := io.ReadAll(res.Body)

		return fmt.Errorf("bad response from nexus: status %s, data: %s", res.Status, string(resp))
	}

	return nil
}
