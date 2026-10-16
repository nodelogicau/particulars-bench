package dataset

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// AbstentionStratum is the stratum of questions whose id ends in "_abs".
const AbstentionStratum = "abstention"

// Question is one LongMemEval question with its history.
type Question struct {
	ID       string
	Type     string
	Question string
	// Answer is the reference answer (for abstention questions, the
	// explanation of why it is unanswerable).
	Answer  string
	Date    time.Time
	DateRaw string
	// Sessions are the history sessions, oldest first.
	Sessions         []Session
	AnswerSessionIDs []string
}

// Session is one dated chat session.
type Session struct {
	ID      string
	Date    time.Time
	DateRaw string
	Turns   []Turn
}

// Turn is one message in a session.
type Turn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Abstention reports whether the question is unanswerable from its history.
func (q Question) Abstention() bool { return strings.HasSuffix(q.ID, "_abs") }

// Stratum is the question's sampling stratum: its type, or "abstention".
func (q Question) Stratum() string {
	if q.Abstention() {
		return AbstentionStratum
	}
	return q.Type
}

type rawQuestion struct {
	QuestionID         string          `json:"question_id"`
	QuestionType       string          `json:"question_type"`
	Question           string          `json:"question"`
	Answer             json.RawMessage `json:"answer"`
	QuestionDate       string          `json:"question_date"`
	HaystackDates      []string        `json:"haystack_dates"`
	HaystackSessionIDs []string        `json:"haystack_session_ids"`
	HaystackSessions   [][]Turn        `json:"haystack_sessions"`
	AnswerSessionIDs   []string        `json:"answer_session_ids"`
}

// dateLayout is LongMemEval's timestamp format, e.g. "2023/04/10 (Mon) 17:50".
// Times carry no zone and are read as UTC.
const dateLayout = "2006/01/02 (Mon) 15:04"

// ParseDate parses a LongMemEval timestamp.
func ParseDate(s string) (time.Time, error) {
	return time.ParseInLocation(dateLayout, strings.TrimSpace(s), time.UTC)
}

// Load parses a dataset file.
func Load(path string) ([]Question, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse parses dataset JSON.
func Parse(b []byte) ([]Question, error) {
	var raws []rawQuestion
	if err := json.Unmarshal(b, &raws); err != nil {
		return nil, fmt.Errorf("parsing dataset: %w", err)
	}
	qs := make([]Question, 0, len(raws))
	for _, r := range raws {
		q, err := r.question()
		if err != nil {
			return nil, fmt.Errorf("question %s: %w", r.QuestionID, err)
		}
		qs = append(qs, q)
	}
	return qs, nil
}

func (r rawQuestion) question() (Question, error) {
	q := Question{
		ID:               r.QuestionID,
		Type:             r.QuestionType,
		Question:         r.Question,
		DateRaw:          r.QuestionDate,
		AnswerSessionIDs: r.AnswerSessionIDs,
	}
	// Answers are strings, but a few are bare numbers.
	var s string
	if err := json.Unmarshal(r.Answer, &s); err == nil {
		q.Answer = s
	} else {
		q.Answer = strings.TrimSpace(string(r.Answer))
	}
	var err error
	if q.Date, err = ParseDate(r.QuestionDate); err != nil {
		return q, err
	}
	if len(r.HaystackDates) != len(r.HaystackSessions) || len(r.HaystackSessionIDs) != len(r.HaystackSessions) {
		return q, fmt.Errorf("haystack dates, ids, and sessions differ in length")
	}
	for i, turns := range r.HaystackSessions {
		d, err := ParseDate(r.HaystackDates[i])
		if err != nil {
			return q, err
		}
		q.Sessions = append(q.Sessions, Session{ID: r.HaystackSessionIDs[i], Date: d, DateRaw: r.HaystackDates[i], Turns: turns})
	}
	sort.SliceStable(q.Sessions, func(i, j int) bool { return q.Sessions[i].Date.Before(q.Sessions[j].Date) })
	return q, nil
}
