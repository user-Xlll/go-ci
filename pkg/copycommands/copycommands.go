package copycommands

import (
	"fmt"
	"regexp"
)

var re = regexp.MustCompile(`^((?P<svc>[^:]+):)?(?P<src>[^:]+):(?P<dst>[^:]+)$`)

// в этой структуре ключами являются названия сервисов
// каждому сервису соответсвует свой набор команд копирования
// если ключ - пустое значние, то набор команд применим для всех сервисов
type copyCommands map[string][]copyCommand

func New() copyCommands {
	return make(copyCommands)
}

func (s copyCommands) String() string {
	return ""
}

func (s copyCommands) Set(value string) error {
	match := re.FindStringSubmatch(value)
	if match == nil {
		return fmt.Errorf("unable to parse --copy-files %s flag", value)
	}

	res := map[string]string{}
	for i, name := range re.SubexpNames() {
		if i == 0 || len(name) == 0 {
			continue
		}
		res[name] = match[i]
	}

	svc := res["svc"]
	s[svc] = append(s[svc], copyCommand{Src: res["src"], Dst: res["dst"]})

	return nil
}

func (s copyCommands) Type() string {
	return "copyCommands"
}

type copyCommand struct {
	Src string
	Dst string
}

func (s copyCommand) String() string {
	return fmt.Sprintf("COPY --chown=app:app %s %s", s.Src, s.Dst)
}
