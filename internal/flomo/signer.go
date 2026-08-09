package flomo

import (
	"crypto/md5" // #nosec G501 -- required by flomo's web request signature protocol.
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// webSigningSalt is part of flomo's public web-client protocol, not a user secret.
const webSigningSalt = "dbbc3dd73364b4084c3a69346e0ce2b2"

func sign(params map[string]any) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := params[key]
		if empty(value) {
			continue
		}
		switch values := value.(type) {
		case []string:
			sort.Strings(values)
			for _, item := range values {
				parts = append(parts, key+"[]="+item)
			}
		case []any:
			items := make([]string, 0, len(values))
			for _, item := range values {
				items = append(items, fmt.Sprint(item))
			}
			sort.Strings(items)
			for _, item := range items {
				parts = append(parts, key+"[]="+item)
			}
		default:
			parts = append(parts, key+"="+fmt.Sprint(value))
		}
	}
	sum := md5.Sum([]byte(strings.Join(parts, "&") + webSigningSalt)) // #nosec G401 -- protocol compatibility.
	return hex.EncodeToString(sum[:])
}

func empty(value any) bool {
	if value == nil {
		return true
	}
	switch value := value.(type) {
	case string:
		return value == ""
	case bool:
		return !value
	case []string:
		return len(value) == 0
	case []any:
		return len(value) == 0
	}
	return false
}
