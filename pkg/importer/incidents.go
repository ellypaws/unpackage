package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const maxIncidentPasteBytes = 8 << 20

type incidentMessage struct {
	ID     string `json:"id"`
	Embeds []struct {
		Type   string `json:"type"`
		Fields []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"fields"`
	} `json:"embeds"`
}

// IncidentSecondsJSON recognizes copied Discord safety-notice responses and
// maps each incident second to the id of the notice message that reported it.
func IncidentSecondsJSON(data []byte) (map[int64]int64, bool, error) {
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
	seen := map[int64]int64{}
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
			messageID, idErr := strconv.ParseInt(message.ID, 10, 64)
			if idErr != nil || messageID <= 0 {
				return nil, true, fmt.Errorf("safety notice has no message id")
			}
			found := false
			for _, field := range embed.Fields {
				if field.Name != "incident_time" {
					continue
				}
				second, parseErr := incidentSecond(field.Value)
				if parseErr != nil {
					return nil, true, parseErr
				}
				seen[second] = messageID
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
	return seen, true, nil
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
