package uade

import (
	"strconv"
	"strings"
)

type DeltaPanel struct{ ID, HTML string }
type HiddenField struct{ Name, Value string }
type DeltaResult struct {
	Panels []DeltaPanel
	Hidden []HiddenField
}
type DeltaFailure string

func ParseDelta(text string, maxNodes, maxChars int) (DeltaResult, DeltaFailure) {
	if maxNodes < 0 || maxChars < 0 {
		return DeltaResult{}, "delta_invalid_limits"
	}
	if len(text) > maxChars {
		return DeltaResult{}, "delta_too_large"
	}
	chars := []rune(text)
	cur, n := 0, 0
	out := DeltaResult{}
	read := func() (string, bool) {
		i := strings.IndexRune(string(chars[cur:]), '|')
		if i < 0 {
			return "", false
		}
		v := string(chars[cur : cur+i])
		cur += i + 1
		return v, true
	}
	for cur < len(chars) {
		if strings.TrimSpace(string(chars[cur:])) == "" {
			break
		}
		if n >= maxNodes {
			return DeltaResult{}, "delta_too_many_nodes"
		}
		lt, ok := read()
		if !ok {
			return DeltaResult{}, "delta_missing_delimiter"
		}
		ln, e := strconv.Atoi(lt)
		if e != nil || ln < 0 {
			return DeltaResult{}, "delta_invalid_length"
		}
		typ, ok := read()
		if !ok {
			return DeltaResult{}, "delta_missing_delimiter"
		}
		id, ok := read()
		if !ok {
			return DeltaResult{}, "delta_missing_delimiter"
		}
		if ln > len(chars)-cur {
			return DeltaResult{}, "delta_truncated"
		}
		content := string(chars[cur : cur+ln])
		cur += ln
		if cur >= len(chars) || chars[cur] != '|' {
			return DeltaResult{}, "delta_missing_delimiter"
		}
		cur++
		n++
		switch typ {
		case "error":
			return DeltaResult{}, "delta_error"
		case "pageRedirect":
			return DeltaResult{}, "delta_redirect"
		case "updatePanel":
			out.Panels = append(out.Panels, DeltaPanel{id, content})
		case "hiddenField":
			out.Hidden = append(out.Hidden, HiddenField{id, content})
		}
	}
	return out, ""
}
