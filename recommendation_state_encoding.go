package main

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Intern repeated string values across multi-library reports. Scene identifiers,
// titles and evidence remain exact; only their serialized representation changes.
func internRecommendationStrings(raw []byte) ([]byte, error) {
	var state any
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	values := []string{}
	ids := map[string]int{}
	var pack func(any) any
	pack = func(v any) any {
		switch x := v.(type) {
		case string:
			id, ok := ids[x]
			if !ok {
				id = len(values)
				ids[x] = id
				values = append(values, x)
			}
			return map[string]any{"$s": id}
		case []any:
			for i := range x {
				x[i] = pack(x[i])
			}
			return x
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				x[k] = pack(x[k])
			}
			return x
		default:
			return v
		}
	}
	packed := pack(state)
	return json.Marshal(map[string]any{"strings": values, "state": packed})
}

func expandRecommendationStrings(raw []byte) ([]byte, error) {
	var wire struct {
		Strings []string `json:"strings"`
		State   any      `json:"state"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}
	if wire.State == nil {
		return nil, fmt.Errorf("missing recommendation state")
	}
	var unpack func(any) (any, error)
	unpack = func(v any) (any, error) {
		switch x := v.(type) {
		case map[string]any:
			if len(x) == 1 {
				if ref, ok := x["$s"]; ok {
					n, valid := ref.(float64)
					if !valid || n < 0 || n >= float64(len(wire.Strings)) || n != float64(int(n)) {
						return nil, fmt.Errorf("invalid recommendation string reference")
					}
					return wire.Strings[int(n)], nil
				}
			}
			for k, v := range x {
				u, e := unpack(v)
				if e != nil {
					return nil, e
				}
				x[k] = u
			}
			return x, nil
		case []any:
			for i := range x {
				u, e := unpack(x[i])
				if e != nil {
					return nil, e
				}
				x[i] = u
			}
			return x, nil
		default:
			return v, nil
		}
	}
	state, err := unpack(wire.State)
	if err != nil {
		return nil, err
	}
	return json.Marshal(state)
}
