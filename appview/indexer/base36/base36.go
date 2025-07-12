// mostly copied from gitea/modules/indexer/internal/base32

package base36

import (
	"fmt"
	"strconv"
)

func Encode(i int64) string {
	return strconv.FormatInt(i, 36)
}

func Decode(s string) (int64, error) {
	i, err := strconv.ParseInt(s, 36, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid base36 integer %q: %w", s, err)
	}
	return i, nil
}
