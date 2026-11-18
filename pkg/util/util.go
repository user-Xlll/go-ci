// Package util - пакет со вспомогательными функциями.
package util

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// BadResponseCodeError - ошибка при получении неправильного http кода ответа.
type BadResponseCodeError int

var ErrVariableNotFound = errors.New("variable not found")

func (s BadResponseCodeError) Error() string {
	return fmt.Sprintf("bad http response code %d", s)
}

// FileExists - проверяет существование файла.
func FileExists(path string) (bool, error) {
	return exists(path, func(fi os.FileInfo) bool { return !fi.IsDir() })
}

// DirExists - проверяет существование директории.
func DirExists(path string) (bool, error) {
	return exists(path, func(fi os.FileInfo) bool { return fi.IsDir() })
}

func exists(path string, filter func(os.FileInfo) bool) (bool, error) {
	fi, err := os.Stat(path)

	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}

		return false, fmt.Errorf("cant check file exists %s: %w", path, err)
	}

	if !filter(fi) {
		return false, nil
	}

	return true, nil
}

func HttpGetJSON[T any](ctx context.Context, url, username, password string) (*T, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("Can't create request: %w", err)
	}

	req.SetBasicAuth(username, password)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("can't get token: %w", err)
	}

	defer res.Body.Close()

	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		return nil, BadResponseCodeError(res.StatusCode)
	}

	var data T

	if err = json.NewDecoder(res.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("can't decoding response: %w", err)
	}

	return &data, nil
}

func FilePathMatch(pattern string, path string) (bool, error) {
	if ok, err := filepath.Match(pattern, path); err != nil || ok {
		return ok, err
	}

	if ok, err := filepath.Match(pattern, filepath.Base(path)); err != nil || ok {
		return ok, err
	}

	for _, dirName := range filepath.SplitList(filepath.Dir(path)) {
		if ok, err := filepath.Match(pattern, dirName+string(filepath.Separator)); err != nil || ok {
			return ok, err
		}
	}

	return false, nil
}

func RenderTemplate(text string, getVariable func(string) (string, bool)) (string, error) {
	var res strings.Builder
	varNameIndex := -1
	varNameLength := 0

	for i, r := range text {
		switch {
		case r == '$':
			varNameIndex = i + 1
			continue
		case varNameIndex == -1:
			res.WriteRune(r)
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (varNameLength > 0 && unicode.IsDigit(r)) || r == '_' || r == '-':
			varNameLength++
		default:
			if varNameLength > 0 {
				varname := text[varNameIndex : varNameIndex+varNameLength]
				val, found := getVariable(varname)
				if !found {
					return "", fmt.Errorf("%w: %s", ErrVariableNotFound, varname)
				}
				res.WriteString(val)
			} else {
				res.WriteRune('$')
				res.WriteRune(r)
			}

			varNameIndex = -1

		}
	}
	if varNameIndex > -1 {
		if varNameLength > 0 {
			varname := text[varNameIndex : varNameIndex+varNameLength]
			val, found := getVariable(varname)
			if !found {
				return "", fmt.Errorf("%w: %s", ErrVariableNotFound, varname)
			}
			res.WriteString(val)
		} else {
			res.WriteRune('$')
		}
	}

	return res.String(), nil
}

func OpenAny(dir string, names ...string) (io.ReadCloser, error) {
	for _, name := range names {
		file, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return nil, err
		}

		return file, nil
	}

	return nil, fmt.Errorf("%w, names %v", os.ErrNotExist, names)
}
