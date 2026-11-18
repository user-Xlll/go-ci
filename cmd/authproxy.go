package cmd

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"log"
	"maps"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"sync/atomic"

	"github.com/sethvargo/go-password/password"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/user-Xlll/go-ci/pkg/util"
)

const pipeMode = 0644
const readHeaderTimeout = 5 * time.Second

type authItem struct{ Auth string }

type authFileModel struct {
	Auths map[string]authItem
}
type authHeaderInterface interface {
	All() iter.Seq2[string, authConfiguration]
	Set(string, authConfiguration)
}

type authConfiguration struct {
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	Email         string `json:"email,omitempty"`
	ServerAddress string `json:"serveraddress,omitempty"`
	IdentityToken string `json:"identitytoken,omitempty"`
}
type authHeaderType map[string]authConfiguration

var authProxyCmd = &cobra.Command{
	Use:   "authproxy",
	Short: "Run docker registry auth proxy",
	Long: `Open http port and redicet all request to real podman/docker server 
	and replace temporary token to real registry credentials. 
	Geterator of client auth.json file is pipe, placed default at $HOME/auth_json.generator`,
	RunE: runAuthProxy,
}

var errNoRegistryCredentials = errors.New("no credentials for registry")

var (
	tokenFilePath          string = "auth_json.generator" //reads from this file every generate new token
	currentToken           atomic.Pointer[string]
	dst_address            string        = "http://127.0.0.1:8888"
	lisnetAddress          string        = ":5000"
	tokenSize              int           = 64
	issueTokenDuration     time.Duration = time.Minute
	tokenUserName          string        = "token"
	removeOriginalAuthFile bool          = true
	authFilePath           string
)

func init() {
	rootCmd.AddCommand(authProxyCmd)

	home, err := os.UserHomeDir()

	if err == nil {
		tokenFilePath = filepath.Join(home, tokenFilePath)
	}

	authProxyCmd.Flags().StringVarP(&tokenFilePath, "generatorpath", "g", tokenFilePath,
		"Path to create pipe with geterator of temporaty auth.json file")
	authProxyCmd.Flags().StringVarP(&dst_address, "dst_address", "d", dst_address,
		"Destination address and port for forward request to this docker/podman server")
	authProxyCmd.Flags().StringVarP(&lisnetAddress, "listen", "l", lisnetAddress,
		"Listen address and port for incoming request")
	authProxyCmd.Flags().IntVarP(&tokenSize, "tokensize", "s", tokenSize,
		"Size of temporaty token")
	authProxyCmd.Flags().DurationVarP(&issueTokenDuration, "duration", "i", issueTokenDuration,
		"Duration between two temporaty auth.json generations. Only one  auth.json will be issued per this duration")
	authProxyCmd.Flags().StringVarP(&tokenUserName, "tokenuser", "u", tokenUserName,
		"Username that should be provided for authentication using the temporary token.")
	authProxyCmd.Flags().BoolVarP(&removeOriginalAuthFile, "removeauthfile", "r", removeOriginalAuthFile,
		"Remove original auth files after reading its")
	authProxyCmd.Flags().StringVarP(&authFilePath, "authfile", "a", authFilePath,
		`Full path to auth secrets. If empty, than will be search in: 
		$REGISTRY_AUTH_FILE
		$XDG_RUNTIME_DIR/containers/auth.json
		$HOME/.docker/config.json
		$DOCKER_CONFIG/config.json
		`)
}

func authFilePaths() []string {
	var res []string

	if authFilePath != "" {
		return []string{authFilePath}
	}

	if val, ok := os.LookupEnv("REGISTRY_AUTH_FILE"); ok {
		return []string{val}
	}

	if val, ok := os.LookupEnv("XDG_RUNTIME_DIR"); ok {
		res = append(res, filepath.Join(val, "containers/auth.json"))
	}

	if val, err := os.UserHomeDir(); err == nil {
		res = append(res, filepath.Join(val, ".docker/config.json"))
	}

	if val, ok := os.LookupEnv("DOCKER_CONFIG"); ok {
		res = append(res, filepath.Join(val, "config.json"))
	}

	return res
}

func readAuthFile(filePath string) (authHeaderType, error) {
	fl, err := os.OpenFile(filePath, os.O_RDONLY, defaultFilePerm)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth value: %w", err)
	}

	defer fl.Close()

	dc := json.NewDecoder(bufio.NewReader(fl))

	var data authFileModel

	if err := dc.Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode auth file: %w", err)
	}

	res := make(authHeaderType, len(data.Auths))

	for registry, auth := range data.Auths {
		if auth.Auth != "" {
			logopass, err := base64.StdEncoding.DecodeString(auth.Auth)

			if err != nil {
				return nil, fmt.Errorf("failed to decode base64 auth value: %w", err)
			}

			var ac authConfiguration

			ac.Username, ac.Password, _ = strings.Cut(string(logopass), ":")
			res[registry] = ac
		}
	}

	return res, nil
}

func makeAuthFileData(token string) (string, error) {
	model := authFileModel{Auths: map[string]authItem{}}

	ra := registryAuth.Load()
	if ra == nil {
		return "", errNoRegistryCredentials
	}

	for registry := range *ra {
		data := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", tokenUserName, token)))
		model.Auths[registry] = authItem{Auth: data}
	}

	jsonData, err := json.MarshalIndent(model, "", "  ")
	if err != nil {
		return ", ", fmt.Errorf("failed to marshal auth file: %w", err)
	}

	return string(jsonData), nil
}

func tokenGenerator() error {
	if err := syscall.Mkfifo(tokenFilePath, pipeMode); err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("failed to create pipe: %w", err)
		}
	}

	if err := os.Chmod(tokenFilePath, pipeMode); err != nil {
		return fmt.Errorf("failed to change pipe mode: %w", err)
	}

	log.Println("waiting token issue requests at", tokenFilePath, "...")

	for {
		file, err := os.OpenFile(tokenFilePath, os.O_WRONLY, os.ModeNamedPipe) // to open pipe to write
		if err != nil {
			return fmt.Errorf("failed to open pipe: %w", err)
		}

		if err := readCredentials(); err != nil {
			return fmt.Errorf("failed to read credentials: %w", err)
		}

		token, err := password.Generate(tokenSize, 1, 1, false, true)
		if err != nil {
			return fmt.Errorf("failed to generate token: %w", err)
		}

		authData, err1 := makeAuthFileData(token)
		if err1 != nil {
			return fmt.Errorf("failed to make auth file: %w", err1)
		}

		if _, err = file.WriteString(authData); err != nil {
			if errors.Is(err, unix.EPIPE) {
				_ = file.Close()

				continue
			}

			return fmt.Errorf("failed to write to pipe: %w", err)
		}

		currentToken.Store(&token)
		log.Println("auth.json sucessfully generated!")

		if err = file.Close(); err != nil {
			return fmt.Errorf("failed to close pipe: %w", err)
		}

		time.Sleep(issueTokenDuration)
	}
}

var registryAuth atomic.Pointer[authHeaderType]

func readCredentials() error {
	res := make(authHeaderType)

	for _, path := range authFilePaths() {
		if found, err := util.FileExists(path); err != nil {
			return fmt.Errorf("failed to check exists of %s: %w", path, err)
		} else if !found {
			continue
		}

		auths, err := readAuthFile(path)

		if err != nil {
			return fmt.Errorf("failed to read auth file %s: %w", path, err)
		}

		log.Println("readed auth file:", path)
		maps.Copy(res, auths)

		if removeOriginalAuthFile {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("failed to remove auth file %s: %w", path, err)
			}

			log.Println("removed auth file:", path)
		}

	}

	registryAuth.Store(&res)

	return nil
}

func runAuthProxy(_ *cobra.Command, _ []string) error {
	go func() {
		if err := tokenGenerator(); err != nil {
			log.Fatal(err)
		}
	}()

	return serveHTTPProxy()
}

func patchRequestAuth(req *http.Request, headerKey string) error {
	vals := req.Header[headerKey]

	for i := range vals {
		if err := patchAuthValue(&vals[i]); err != nil {
			return fmt.Errorf("failed patch auth %s value: %w", headerKey, err)
		}
	}

	return nil
}

func unmarshallAs(data []byte, targets ...authHeaderInterface) (authHeaderInterface, error) {
	var err error
	for _, target := range targets {
		if err = json.Unmarshal(data, target); err == nil {
			return target, nil
		}
	}

	if debugMode {
		return nil, fmt.Errorf("failed json unmarshal auth header: %w, data: %s", err, data)
	}

	return nil, fmt.Errorf("failed json unmarshal auth header: %w", err)
}

func patchAuthValue(value *string) error {
	data, err := base64.StdEncoding.DecodeString(*value)
	if err != nil {
		return fmt.Errorf("failed decode base64 auth value: %w", err)
	}

	authHeaderValue, err1 := unmarshallAs(data, new(authHeaderType), new(authConfiguration))
	if err1 != nil {
		return err1
	}

	var modified bool

	pPass := currentToken.Load()
	ra := registryAuth.Load()

	if pPass == nil || ra == nil {
		log.Println("Proxing request, but no temporaty token was generated")

		return nil
	}

	for registry, auth := range authHeaderValue.All() {
		if auth.Username == tokenUserName && auth.Password == *pPass {
			newAuth, ok := (*ra)[registry]
			if !ok {
				return fmt.Errorf("%w: %s", errNoRegistryCredentials, registry)
			}

			authHeaderValue.Set(registry, newAuth)
			modified = true
		}
	}

	if !modified {
		return nil
	}

	data, err = json.Marshal(authHeaderValue)
	if err != nil {
		return fmt.Errorf("failed json marshal auth header: %w", err)
	}

	*value = base64.StdEncoding.EncodeToString(data)

	return nil
}

func (s authHeaderType) All() iter.Seq2[string, authConfiguration] {
	return maps.All(s)
}

func (s authHeaderType) Set(key string, val authConfiguration) {
	s[key] = val
}

func (s *authConfiguration) All() iter.Seq2[string, authConfiguration] {
	return func(yield func(string, authConfiguration) bool) {
		yield(s.ServerAddress, *s)
	}
}

func (s *authConfiguration) Set(server string, val authConfiguration) {
	val.ServerAddress = server
	*s = val
}

func patchRequest(req *http.Request) error {
	if err := patchRequestAuth(req, "X-Registry-Auth"); err != nil {
		return fmt.Errorf("failed patch X-Registry-Auth: %w", err)
	}

	if err := patchRequestAuth(req, "X-Registry-Config"); err != nil {
		return fmt.Errorf("failed patch X-Registry-Config: %w", err)
	}

	return nil
}

func writeError(writer http.ResponseWriter, statusCode int, err error) {
	writer.WriteHeader(statusCode)
	_, _ = writer.Write([]byte(err.Error()))
}

func requestHandler(writer http.ResponseWriter, req *http.Request) {
	req.Host = dst_address
	req.URL.Host = dst_address
	req.RequestURI = ""
	req.URL.Scheme = "http"

	if err := patchRequest(req); err != nil {
		log.Println("failed patch request:", err)
		writeError(writer, http.StatusInternalServerError, err)

		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println("failed request to upsream:", req.URL, ":", err)
		writeError(writer, http.StatusBadGateway, err)

		return
	}

	defer func() {
		if err1 := resp.Body.Close(); err1 != nil {
			log.Println("failed close response body:", err1)
		}
	}()

	maps.Copy(writer.Header(), resp.Header)
	writer.WriteHeader(resp.StatusCode)

	if _, err = io.Copy(writer, resp.Body); err != nil {
		log.Println("failed write respons to downstream:", err)
	}
}

func serveHTTPProxy() error {
	lst, err := net.Listen("tcp", lisnetAddress) //nolint:gosec //ok
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	log.Println("listen http request on", lisnetAddress, "for redirect to", dst_address)

	srv := &http.Server{Handler: http.HandlerFunc(requestHandler),
		ReadHeaderTimeout: readHeaderTimeout}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)

	go func() {
		<-ch
		log.Println("stopping server...")

		_ = srv.Shutdown(context.Background())
	}()

	return srv.Serve(lst) //nolint:wrapcheck //ok
}
