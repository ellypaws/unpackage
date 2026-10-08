// Package safety reads Discord Safety Hub and safety notice responses copied from a browser.
package safety

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

const maxPasteBytes = 8 << 20

const discordEpoch = 1420070400000

// Report is everything learned from pasted responses during this session.
type Report struct {
	Standing          int
	Username          string
	DSAEligible       bool
	AppealEligible    bool
	AppealEligibility []int
	HubLoaded         bool
	Violations        []Violation
}

// Violation joins a Safety Hub classification with the notices that reported it.
type Violation struct {
	ID             string
	Classification *Classification
	Notices        []Notice
}

type Classification struct {
	ID              string
	Type            int
	Description     string
	ExplainerLink   string
	Actions         []Action
	Expires         time.Time
	Flagged         []Flagged
	AppealStatus    int
	COPPA, Spam     bool
	AppealIngestion int
	Guild           *Guild
}

type Action struct {
	ID           string
	Type         int
	Descriptions []string
}

type Flagged struct {
	Type, ID, Content string
	Attachments       []string
}

type Guild struct {
	Name       string
	MemberType int
}

// Notice is a safety embed from the official Discord system DM.
type Notice struct {
	MessageID, ClassificationID string
	System                      bool
	Time                        time.Time
	Header, Body, LearnMore     string
}

// Result describes what one paste added.
type Result struct {
	Notices, Classifications int
	Hub                      bool
}

type hubResponse struct {
	Classifications      []classificationJSON `json:"classifications"`
	GuildClassifications []classificationJSON `json:"guild_classifications"`
	AccountStanding      *struct {
		State int `json:"state"`
	} `json:"account_standing"`
	DSAEligible       bool   `json:"is_dsa_eligible"`
	AppealEligible    bool   `json:"is_appeal_eligible"`
	Username          string `json:"username"`
	AppealEligibility []int  `json:"appeal_eligibility"`
}

type classificationJSON struct {
	ID                 string `json:"id"`
	ClassificationType int    `json:"classification_type"`
	Description        string `json:"description"`
	ExplainerLink      string `json:"explainer_link"`
	Actions            []struct {
		ID           string   `json:"id"`
		ActionType   int      `json:"action_type"`
		Descriptions []string `json:"descriptions"`
	} `json:"actions"`
	MaxExpirationTime *string `json:"max_expiration_time"`
	FlaggedContent    []struct {
		Type        string `json:"type"`
		ID          string `json:"id"`
		Content     string `json:"content"`
		Attachments []struct {
			Filename string `json:"filename"`
		} `json:"attachments"`
	} `json:"flagged_content"`
	AppealStatus *struct {
		Status int `json:"status"`
	} `json:"appeal_status"`
	IsCOPPA             bool `json:"is_coppa"`
	IsSpam              bool `json:"is_spam"`
	AppealIngestionType int  `json:"appeal_ingestion_type"`
	GuildMetadata       *struct {
		Name       string `json:"name"`
		MemberType int    `json:"member_type"`
	} `json:"guild_metadata"`
}

type messageJSON struct {
	ID     string `json:"id"`
	Embeds []struct {
		Type   string `json:"type"`
		Fields []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"fields"`
	} `json:"embeds"`
}

// Parse recognizes a Safety Hub object or a message list holding safety notices.
// Unrecognized input returns ok false with no error so callers can report it plainly.
func Parse(data []byte) (Report, bool, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '[' && trimmed[0] != '{' {
		return Report{}, false, nil
	}
	if len(trimmed) > maxPasteBytes {
		return Report{}, true, fmt.Errorf("pasted response exceeds 8 MiB")
	}
	if trimmed[0] == '{' {
		return parseHub(trimmed)
	}
	return parseMessages(trimmed)
}

// Likely is a cheap check for pasted text that Parse would recognize.
func Likely(text string) bool {
	probe := text[:min(len(text), 64<<10)]
	trimmed := strings.TrimLeft(probe, " \t\r\n")
	if !strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "{") {
		return false
	}
	for _, marker := range []string{`"incident_time"`, `"safety_policy_notice"`, `"safety_system_notification"`, `"classifications"`, `"account_standing"`} {
		if strings.Contains(probe, marker) {
			return true
		}
	}
	return false
}

func parseHub(data []byte) (Report, bool, error) {
	var hub hubResponse
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&hub); err != nil {
		if bytes.Contains(data, []byte(`"classifications"`)) {
			return Report{}, true, fmt.Errorf("invalid Safety Hub response: %w", err)
		}
		return Report{}, false, nil
	}
	if hub.AccountStanding == nil && hub.Classifications == nil && hub.GuildClassifications == nil {
		return Report{}, false, nil
	}
	if err := trailing(decoder); err != nil {
		return Report{}, true, err
	}
	report := Report{Username: hub.Username, DSAEligible: hub.DSAEligible, AppealEligible: hub.AppealEligible, AppealEligibility: slices.Clone(hub.AppealEligibility), HubLoaded: true}
	if hub.AccountStanding != nil {
		report.Standing = hub.AccountStanding.State
	}
	for _, raw := range slices.Concat(hub.Classifications, hub.GuildClassifications) {
		c, err := classification(raw)
		if err != nil {
			return Report{}, true, err
		}
		report.Violations = append(report.Violations, Violation{ID: c.ID, Classification: &c})
	}
	report.sort()
	return report, true, nil
}

func classification(raw classificationJSON) (Classification, error) {
	if !snowflake(raw.ID) {
		return Classification{}, fmt.Errorf("classification has no id")
	}
	c := Classification{ID: raw.ID, Type: raw.ClassificationType, Description: raw.Description, ExplainerLink: raw.ExplainerLink, COPPA: raw.IsCOPPA, Spam: raw.IsSpam, AppealIngestion: raw.AppealIngestionType}
	for _, a := range raw.Actions {
		c.Actions = append(c.Actions, Action{ID: a.ID, Type: a.ActionType, Descriptions: slices.Clone(a.Descriptions)})
	}
	if raw.MaxExpirationTime != nil && *raw.MaxExpirationTime != "" {
		expires, err := time.Parse(time.RFC3339Nano, *raw.MaxExpirationTime)
		if err != nil {
			return Classification{}, fmt.Errorf("invalid max_expiration_time")
		}
		c.Expires = expires
	}
	for _, f := range raw.FlaggedContent {
		flagged := Flagged{Type: f.Type, ID: f.ID, Content: f.Content}
		for _, a := range f.Attachments {
			flagged.Attachments = append(flagged.Attachments, a.Filename)
		}
		c.Flagged = append(c.Flagged, flagged)
	}
	if raw.AppealStatus != nil {
		c.AppealStatus = raw.AppealStatus.Status
	}
	if raw.GuildMetadata != nil {
		c.Guild = &Guild{Name: raw.GuildMetadata.Name, MemberType: raw.GuildMetadata.MemberType}
	}
	return c, nil
}

func parseMessages(data []byte) (Report, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return Report{}, false, nil
	}
	var report Report
	recognized := false
	for decoder.More() {
		var message messageJSON
		if err := decoder.Decode(&message); err != nil {
			if recognized {
				return Report{}, true, fmt.Errorf("invalid Discord message response: %w", err)
			}
			return Report{}, false, nil
		}
		for _, embed := range message.Embeds {
			if embed.Type != "safety_policy_notice" && embed.Type != "safety_system_notification" {
				continue
			}
			recognized = true
			if !snowflake(message.ID) {
				return Report{}, true, fmt.Errorf("safety notice has no message id")
			}
			notice := Notice{MessageID: message.ID, System: embed.Type == "safety_system_notification"}
			timeField := "incident_time"
			if notice.System {
				timeField = "timestamp"
			}
			for _, field := range embed.Fields {
				switch field.Name {
				case "classification_id":
					notice.ClassificationID = strings.TrimSpace(field.Value)
				case timeField:
					at, err := unixTime(field.Value, !notice.System)
					if err != nil {
						return Report{}, true, err
					}
					notice.Time = at
				case "header":
					notice.Header = field.Value
				case "body":
					notice.Body = field.Value
				case "learn_more_link":
					notice.LearnMore = field.Value
				}
			}
			if !notice.System && notice.Time.IsZero() {
				return Report{}, true, fmt.Errorf("safety notice has no incident_time")
			}
			report.addNotice(notice)
		}
	}
	if _, err := decoder.Token(); err != nil {
		if recognized {
			return Report{}, true, fmt.Errorf("invalid Discord message response: %w", err)
		}
		return Report{}, false, nil
	}
	if err := trailing(decoder); err != nil {
		return Report{}, recognized, err
	}
	if !recognized {
		return Report{}, false, nil
	}
	report.sort()
	return report, true, nil
}

func trailing(decoder *json.Decoder) error {
	var rest json.RawMessage
	err := decoder.Decode(&rest)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("trailing JSON data")
	}
	return fmt.Errorf("invalid Discord response: %w", err)
}

// unixTime parses an embed field holding unix seconds. Incident times are whole seconds
// written with a ".0" suffix; system notification timestamps carry microseconds.
func unixTime(value string, whole bool) (time.Time, error) {
	seconds, fraction, _ := strings.Cut(strings.TrimSpace(value), ".")
	if seconds == "" || strings.Trim(fraction, "0123456789") != "" || whole && strings.Trim(fraction, "0") != "" {
		return time.Time{}, fmt.Errorf("invalid incident_time")
	}
	n, err := strconv.ParseInt(seconds, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}, fmt.Errorf("invalid incident_time")
	}
	nanos := int64(0)
	if fraction != "" {
		digits := (fraction + "000000000")[:9]
		nanos, _ = strconv.ParseInt(digits, 10, 64)
	}
	return time.Unix(n, nanos), nil
}

func snowflake(id string) bool {
	n, err := strconv.ParseUint(id, 10, 64)
	return err == nil && n > 0
}

// SnowflakeTime is the creation time encoded in a Discord ID.
func SnowflakeTime(id string) time.Time {
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil || n == 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(n>>22) + discordEpoch)
}

func (r *Report) addNotice(n Notice) {
	key := cmp.Or(n.ClassificationID, "notice-"+n.MessageID)
	i := slices.IndexFunc(r.Violations, func(v Violation) bool { return v.ID == key })
	if i < 0 {
		r.Violations = append(r.Violations, Violation{ID: key})
		i = len(r.Violations) - 1
	}
	v := &r.Violations[i]
	if j := slices.IndexFunc(v.Notices, func(existing Notice) bool { return existing.MessageID == n.MessageID }); j >= 0 {
		v.Notices[j] = n
		return
	}
	v.Notices = append(v.Notices, n)
	slices.SortFunc(v.Notices, func(a, b Notice) int { return a.Time.Compare(b.Time) })
}

// Merge folds a newer paste into the report. Classifications are replaced by ID,
// notices are deduplicated by message ID, and account fields follow the latest hub.
func (r *Report) Merge(next Report) Result {
	var result Result
	if next.HubLoaded {
		r.Standing = next.Standing
		r.Username = next.Username
		r.DSAEligible = next.DSAEligible
		r.AppealEligible = next.AppealEligible
		r.AppealEligibility = slices.Clone(next.AppealEligibility)
		r.HubLoaded = true
		result.Hub = true
	}
	for _, v := range next.Violations {
		i := slices.IndexFunc(r.Violations, func(existing Violation) bool { return existing.ID == v.ID })
		if i < 0 {
			r.Violations = append(r.Violations, Violation{ID: v.ID})
			i = len(r.Violations) - 1
		}
		if v.Classification != nil {
			if r.Violations[i].Classification == nil {
				result.Classifications++
			}
			r.Violations[i].Classification = v.Classification
		}
		for _, n := range v.Notices {
			if !slices.ContainsFunc(r.Violations[i].Notices, func(existing Notice) bool { return existing.MessageID == n.MessageID }) {
				result.Notices++
			}
			r.addNotice(n)
		}
	}
	r.sort()
	return result
}

func (r *Report) sort() {
	slices.SortStableFunc(r.Violations, func(a, b Violation) int {
		return cmp.Or(b.Time().Compare(a.Time()), strings.Compare(b.ID, a.ID))
	})
}

// Clone copies the report so a background query can read it while the UI changes it.
func (r Report) Clone() Report {
	r.AppealEligibility = slices.Clone(r.AppealEligibility)
	r.Violations = slices.Clone(r.Violations)
	for i := range r.Violations {
		r.Violations[i].Notices = slices.Clone(r.Violations[i].Notices)
	}
	return r
}

// Incident is the reported incident time, falling back to when Discord created the classification.
func (v Violation) Incident() (time.Time, bool) {
	for _, n := range v.Notices {
		if !n.System && !n.Time.IsZero() {
			return n.Time, true
		}
	}
	return SnowflakeTime(v.ID), false
}

// Time orders violations: incident time when known, otherwise classification creation.
func (v Violation) Time() time.Time {
	at, _ := v.Incident()
	return at
}

// Seconds lists the exact incident seconds that identify messages for this violation.
func (v Violation) Seconds() []int64 {
	var out []int64
	for _, n := range v.Notices {
		if !n.System && !n.Time.IsZero() {
			out = append(out, n.Time.Unix())
		}
	}
	return slices.Compact(slices.Sorted(slices.Values(out)))
}

// MessageIDs lists flagged message IDs reported by the Safety Hub.
func (v Violation) MessageIDs() []string {
	if v.Classification == nil {
		return nil
	}
	var out []string
	for _, f := range v.Classification.Flagged {
		if (f.Type == "" || f.Type == "message") && snowflake(f.ID) {
			out = append(out, f.ID)
		}
	}
	return slices.Compact(slices.Sorted(slices.Values(out)))
}

// Title names the violation by its description, classification type, or notice header.
func (v Violation) Title() string {
	if c := v.Classification; c != nil {
		return cmp.Or(strings.TrimSpace(c.Description), ClassificationLabel(c.Type))
	}
	for _, n := range v.Notices {
		if n.Header != "" {
			return n.Header
		}
	}
	return "Community guidelines violation"
}

// State summarizes where the violation stands for filtering and color.
func (v Violation) State(now time.Time) string {
	if c := v.Classification; c != nil {
		switch c.AppealStatus {
		case AppealPending:
			return StatePending
		case AppealUpheld:
			return StateUpheld
		case AppealInvalidated:
			return StateInvalidated
		}
		if !c.Expires.IsZero() && !c.Expires.After(now) {
			return StateExpired
		}
		return StateActive
	}
	return StateNotice
}

// Scope is "server" for guild classifications and "account" otherwise.
func (v Violation) Scope() string {
	if v.Classification != nil && v.Classification.Guild != nil {
		return "server"
	}
	return "account"
}

// Index maps messages to the violations that identify them.
type Index struct {
	ByID     map[string]int
	BySecond map[int64]int
}

func (r Report) Index() Index {
	index := Index{ByID: map[string]int{}, BySecond: map[int64]int{}}
	for i, v := range r.Violations {
		for _, id := range v.MessageIDs() {
			if _, ok := index.ByID[id]; !ok {
				index.ByID[id] = i
			}
		}
		for _, second := range v.Seconds() {
			if _, ok := index.BySecond[second]; !ok {
				index.BySecond[second] = i
			}
		}
	}
	return index
}

// Seconds maps every incident second to the notice message that reported it.
func (r Report) Seconds() map[int64]int64 {
	out := map[int64]int64{}
	for _, v := range r.Violations {
		for _, n := range v.Notices {
			if n.System || n.Time.IsZero() {
				continue
			}
			id, _ := strconv.ParseInt(n.MessageID, 10, 64)
			out[n.Time.Unix()] = id
		}
	}
	return out
}

// MessageIDs lists every flagged message ID across violations.
func (r Report) MessageIDs() []string {
	set := map[string]bool{}
	for _, v := range r.Violations {
		for _, id := range v.MessageIDs() {
			set[id] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}
