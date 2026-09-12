package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

const maxIncidentPasteBytes = 8 << 20

type incidentMessage struct {
	Embeds []struct {
		Type   string `json:"type"`
		Fields []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"fields"`
	} `json:"embeds"`
}

// IncidentSecondsJSON recognizes copied Discord safety-notice responses.
func IncidentSecondsJSON(data []byte) ([]int64, bool, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, false, nil
	}
	if len(trimmed) > maxIncidentPasteBytes {
		return nil, true, fmt.Errorf("pasted response exceeds 8 MiB")
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil, false, nil
	}
	seen := map[int64]bool{}
	recognized := false
	for decoder.More() {
		var message incidentMessage
		if err = decoder.Decode(&message); err != nil {
			if recognized {
				return nil, true, fmt.Errorf("invalid Discord message response: %w", err)
			}
			return nil, false, nil
		}
		for _, embed := range message.Embeds {
			if embed.Type != "safety_policy_notice" {
				continue
			}
			recognized = true
			found := false
			for _, field := range embed.Fields {
				if field.Name != "incident_time" {
					continue
				}
				second, parseErr := incidentSecond(field.Value)
				if parseErr != nil {
					return nil, true, parseErr
				}
				seen[second] = true
				found = true
			}
			if !found {
				return nil, true, fmt.Errorf("safety notice has no incident_time")
			}
		}
	}
	if _, err = decoder.Token(); err != nil {
		if recognized {
			return nil, true, fmt.Errorf("invalid Discord message response: %w", err)
		}
		return nil, false, nil
	}
	var trailing json.RawMessage
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, recognized, fmt.Errorf("trailing JSON data")
		}
		return nil, recognized, fmt.Errorf("invalid Discord message response: %w", err)
	}
	if !recognized {
		return nil, false, nil
	}
	seconds := make([]int64, 0, len(seen))
	for second := range seen {
		seconds = append(seconds, second)
	}
	slices.Sort(seconds)
	return seconds, true, nil
}

func incidentSecond(value string) (int64, error) {
	whole, fraction, _ := strings.Cut(strings.TrimSpace(value), ".")
	if whole == "" || strings.Trim(fraction, "0") != "" {
		return 0, fmt.Errorf("invalid incident_time")
	}
	second, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || second <= 0 {
		return 0, fmt.Errorf("invalid incident_time")
	}
	return second, nil
}
