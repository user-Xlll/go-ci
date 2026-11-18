package nexus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	nexusHost      string
	repositoryName string

	user     string
	password string

	waitTimeout = time.Minute
	retryCount  = 3
)

func doRPC[T any](ctx context.Context, tid int, action, method string, data ...any) (r T, _ error) {
	type nexusRPC struct {
		Action string `json:"action"`
		Method string `json:"method"`
		Type   string `json:"type"`
		Tid    int    `json:"tid"`
		Data   []any  `json:"data"`
	}

	body, err := json.Marshal(nexusRPC{Action: action, Method: method, Data: data, Type: "rpc", Tid: tid})
	if err != nil {
		return r, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("https://%s/service/extdirect", nexusHost),
		bytes.NewReader(body))
	if err != nil {
		log.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(user, password)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return r, err
	}

	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode != http.StatusOK {
		return r, fmt.Errorf("expected status code %d, got %d", http.StatusOK, res.StatusCode)
	}

	var fullResponse struct {
		Result struct {
			Data T `json:"data"`
		} `json:"result"`
	}

	if err := json.NewDecoder(res.Body).Decode(&fullResponse); err != nil {
		return r, err
	}

	return fullResponse.Result.Data, nil
}

// RootCmd - root command for nexus.
var RootCmd = &cobra.Command{
	Use:   "nexus",
	Short: "Commands for working with nexus",
	Long:  `Commands for working with nexus`,
}

var IsDebug func() bool
var strictMode = true

func init() {
	getHashCmd := &cobra.Command{
		Use:   "image-hash",
		Short: "Get hash of image",
		Long:  `Get hash of remote docker image by image registry:tag`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("expected 1 argument with image repository-name.nexus-host/registry:tag, got %d", len(args))
			}

			registry, tag, found := strings.Cut(args[0], ":")
			if !found {
				return fmt.Errorf("expected image registry:tag, got %s", args[0])
			}

			if repositoryName == "" {
				if repositoryName, registry, found = strings.Cut(registry, "."); !found {
					return fmt.Errorf("when --repository is absent, expected image repository-name.nexus-host/registry:tag, got %s", args[0])
				}
			}

			if nexusHost == "" {
				if nexusHost, registry, found = strings.Cut(registry, "/"); !found {
					return fmt.Errorf("when --nexus-host is absent, expected image nexus-host/registry:tag, got %s", args[0])
				}
			}

			if IsDebug() {
				fmt.Fprintf(os.Stderr, "search image %s:%s in  nexus %s\n",
					registry, tag, nexusHost,
				)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), waitTimeout)
			defer cancel()

			var version string
			var err error
			for attempt := 0; attempt < retryCount; attempt++ {
				version, err = getRemoteImageHash(ctx, registry, tag)
				if err == nil {
					break
				}
				if ctx.Err() != nil {
					return fmt.Errorf("timed out waiting for image: %w", err)
				}
				if attempt+1 < retryCount {
					select {
					case <-ctx.Done():
						return fmt.Errorf("timed out waiting for image: %w", err)
					case <-time.After(time.Second):
					}
				}
			}

			if err != nil {
				return err
			}

			fmt.Print(version)

			return nil
		}}

	getHashCmd.Flags().StringVarP(&user, "user", "u", user, "user for nexus")
	getHashCmd.Flags().StringVarP(&password, "password", "p", password, "password for nexus")
	getHashCmd.Flags().StringVarP(&nexusHost, "nexus-host", "n", nexusHost, "hostname of nexus. Extracts from image name, if not provided")
	getHashCmd.Flags().StringVarP(&repositoryName, "repository", "r", repositoryName, "name of docker repository. Extracts from image name, if not provide")
	getHashCmd.Flags().DurationVarP(&waitTimeout, "wait-timeout", "", waitTimeout, "time to wait for image in nexus, deafault 1m")
	getHashCmd.Flags().IntVarP(&retryCount, "retry-count", "", retryCount, "number of retries to get image hash")
	getHashCmd.Flags().BoolVarP(&strictMode, "strict", "", strictMode, "strict mode, return error if tag exists more than one repository")
	RootCmd.AddCommand(getHashCmd)
}

var ErrTagNotFound = fmt.Errorf("tag not found")
var ErrTagDublicateInStrictMOde = fmt.Errorf("tag dublicate in many repositorys")

func getRemoteImageHash(ctx context.Context, registry string, tag string) (string, error) {
	val, err := doRPC[[]map[string]any](ctx, 26, "coreui_Search", "read", map[string]any{
		"formatSearch": true,
		"page":         1,
		"start":        0,
		"limit":        300,
		"filter": []map[string]string{
			{"property": "format", "value": "docker"},
			{"property": "attributes.docker.imageTag", "value": tag},
			{"property": "attributes.docker.imageName", "value": registry},
		}})

	if err != nil {
		return "", err
	}

	if IsDebug() {
		fmt.Fprintf(os.Stderr, "found tags: %v\n", val)
	}

	if strictMode {
		for i := 0; i < len(val)-1; i++ {
			if val[i]["repositoryName"] != val[i+1]["repositoryName"] {
				return "", fmt.Errorf("%w, tag %s in %v and %v", ErrTagDublicateInStrictMOde, tag, val[i]["repositoryName"], val[i+1]["repositoryName"])
			}
		}

	}

	for _, component := range val {
		componentJson, err := json.Marshal(component)
		if err != nil {
			return "", fmt.Errorf("can't marshal to json component %v: %w", component, err)
		}

		assets, err := doRPC[[]struct {
			ID         string `json:"id"`
			Attributes struct {
				Checksum struct {
					SHA256 string `json:"sha256"`
				} `json:"checksum"`
			} `json:"attributes"`
		}](ctx, 35, "coreui_Component", "readComponentAssets", map[string]any{
			"formatSearch": true,
			"page":         1,
			"start":        0,
			"limit":        300,
			"filter": []map[string]string{
				{"property": "repositoryName", "value": component["repositoryName"].(string)},
				{"property": "componentModel", "value": string(componentJson)},
			}})

		if err != nil {
			return "", fmt.Errorf("can't get assets for component %v: %w", string(componentJson), err)
		}

		for _, a := range assets {
			if a.Attributes.Checksum.SHA256 != "" {
				return a.Attributes.Checksum.SHA256, nil
			}
		}
	}

	return "", fmt.Errorf("%w, tag %s", ErrTagNotFound, tag)
}
