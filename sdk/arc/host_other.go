//go:build !wasip1

package arc

import (
	"encoding/json"
	"errors"
)

func init() {
	hostCall = func(string, any) (json.RawMessage, error) {
		return nil, errors.New("arc: host calls are only possible inside the server")
	}
}
