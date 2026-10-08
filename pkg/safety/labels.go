package safety

import "fmt"

const (
	AppealPending     = 1
	AppealUpheld      = 2
	AppealInvalidated = 3
)

const (
	StateActive      = "active"
	StatePending     = "pending"
	StateUpheld      = "upheld"
	StateInvalidated = "invalidated"
	StateExpired     = "expired"
	StateNotice      = "notice"
)

// Standings is the Safety Hub account standing scale from best to worst.
var Standings = []int{100, 200, 300, 400, 500}

var standingLabels = map[int]string{100: "All good", 200: "Limited", 300: "Very limited", 400: "At risk", 500: "Suspended"}

var standingDetails = map[int]string{
	100: "No active violations limit the account.",
	200: "Some features may be limited.",
	300: "Several features are limited.",
	400: "Another violation may suspend the account.",
	500: "The account is suspended.",
}

var classificationLabels = map[int]string{
	1:    "Unknown",
	100:  "Unsolicited pornography",
	200:  "Non-consensual pornography",
	210:  "Glorifying violence",
	220:  "Hate speech",
	230:  "Cracked accounts",
	240:  "Illicit goods",
	250:  "Social engineering",
	280:  "Child safety",
	290:  "Harassment and bullying",
	310:  "Harassment and bullying",
	320:  "Hateful conduct",
	390:  "Harassment and bullying",
	600:  "Child safety",
	650:  "Child safety",
	711:  "Impersonation",
	720:  "Ban evasion",
	3010: "Malicious conduct",
	3030: "Spam",
	4000: "Non-consensual adult content",
	4010: "Fraud",
	4130: "Owned a doxxing server",
	4140: "Owned a copyright infringement server",
	5010: "Child safety",
	5090: "Child self-endangerment",
	5245: "Member of a harassment server",
	5305: "Member of a doxxing server",
	5411: "Underage",
	5440: "Member of a copyright infringement server",
	5485: "Copyright infringement",
}

var actionLabels = map[int]string{
	0:  "Permanent ban",
	1:  "Temporary ban",
	2:  "Global quarantine",
	3:  "Verification required",
	4:  "Warning",
	5:  "Marked as spammer",
	6:  "Channel marked as spam",
	7:  "Message marked as spam",
	8:  "Disabled for suspicious activity",
	9:  "Limited access",
	10: "Channel scheduled for deletion",
	11: "Message content removed",
	12: "Server invites disabled",
	13: "User content removed",
	14: "Username cleared",
	15: "Server access limited",
	16: "Message removed",
	20: "Server deleted",
	22: "Profile cleared",
}

var appealLabels = map[int]string{0: "No appeal", AppealPending: "Appeal pending", AppealUpheld: "Appeal denied", AppealInvalidated: "Appeal accepted"}

var appealDetails = map[int]string{
	0:                 "No appeal has been recorded for this violation.",
	AppealPending:     "Discord is reviewing the appeal.",
	AppealUpheld:      "Discord reviewed the appeal and kept the violation.",
	AppealInvalidated: "Discord accepted the appeal and removed the violation.",
}

var ingestionLabels = map[int]string{0: "Web form", 1: "Age verification", 2: "In the app"}

var eligibilityLabels = map[int]string{1: "DSA appeal", 2: "In-app appeal", 3: "Age verification"}

var memberLabels = map[int]string{1: "Owner", 2: "Member"}

var StateLabels = map[string]string{
	StateActive:      "Active",
	StatePending:     "Appeal pending",
	StateUpheld:      "Appeal denied",
	StateInvalidated: "Appeal accepted",
	StateExpired:     "Expired",
	StateNotice:      "Notice only",
}

func label(labels map[int]string, value int, kind string) string {
	if text, ok := labels[value]; ok {
		return text
	}
	return fmt.Sprintf("%s %d", kind, value)
}

func StandingLabel(state int) string      { return label(standingLabels, state, "Standing") }
func StandingDetail(state int) string     { return standingDetails[state] }
func ClassificationLabel(kind int) string { return label(classificationLabels, kind, "Classification") }
func ActionLabel(kind int) string         { return label(actionLabels, kind, "Action") }
func AppealLabel(status int) string       { return label(appealLabels, status, "Appeal status") }
func AppealDetail(status int) string      { return appealDetails[status] }
func IngestionLabel(kind int) string      { return label(ingestionLabels, kind, "Appeal type") }
func EligibilityLabel(kind int) string    { return label(eligibilityLabels, kind, "Appeal type") }
func MemberLabel(kind int) string         { return label(memberLabels, kind, "Member type") }
