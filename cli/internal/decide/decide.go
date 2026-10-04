// Package decide holds mcpit's decision points. Every choice in the app is a typed
// question answered by a decision model (Clef-flash, or any System One API model
// such as local jev). The LLM never makes these decisions.
package decide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Question is one System One API question: type noul, choice or score.
type Question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"` // map[string]string for choice, []string for score
}

type Answer struct {
	Type          string             `json:"type"`
	Noul          float64            `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
	Score         float64            `json:"score,omitempty"`
	Model         string             `json:"-"`
}

// Yes reports a noul answer above 0.5.
func (a Answer) Yes() bool { return a.Noul >= 0.5 }

// Sure reports whether the answer is clear enough to act on without escalation.
func (a Answer) Sure() bool {
	switch a.Type {
	case "noul":
		return a.Noul <= 0.25 || a.Noul >= 0.75
	case "choice":
		return a.Probabilities[a.Choice] >= 0.6
	default:
		return true
	}
}

// Provider answers a batch of questions about one state.
type Provider interface {
	Name() string
	Ask(ctx context.Context, state any, qs map[string]Question) (map[string]Answer, error)
}

// MaxQuestions is the System One API limit per call.
const MaxQuestions = 64

// Logger records every decision.
type Logger func(point, subject, model string, a Answer)

// Decider wraps a provider with batching and logging. A nil provider means
// "no decision model": callers then use their rule-based fallback.
type Decider struct {
	P   Provider
	Log Logger
}

var ErrNoModel = errors.New("no decision model configured")

func (d *Decider) Available() bool { return d != nil && d.P != nil }

func (d *Decider) Model() string {
	if !d.Available() {
		return "rules"
	}
	return d.P.Name()
}

// Ask answers questions in batches of MaxQuestions; subjects maps question id to a
// short label for the log.
func (d *Decider) Ask(ctx context.Context, point string, state any, qs map[string]Question, subjects map[string]string) (map[string]Answer, error) {
	if !d.Available() {
		return nil, ErrNoModel
	}
	ids := make([]string, 0, len(qs))
	for id := range qs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := map[string]Answer{}
	for start := 0; start < len(ids); start += MaxQuestions {
		end := min(start+MaxQuestions, len(ids))
		batch := map[string]Question{}
		for _, id := range ids[start:end] {
			batch[id] = qs[id]
		}
		ans, err := d.P.Ask(ctx, state, batch)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", point, err)
		}
		for id, a := range ans {
			a.Model = d.P.Name()
			out[id] = a
			if d.Log != nil {
				d.Log(point, subjects[id], d.P.Name(), a)
			}
		}
	}
	return out, nil
}

// Choice builds a choice question from ordered options.
func Choice(instructions string, options [][2]string) Question {
	m := map[string]string{}
	for _, o := range options {
		m[o[0]] = o[1]
	}
	return Question{Type: "choice", Instructions: instructions, Criteria: m}
}

func Noul(instructions string) Question { return Question{Type: "noul", Instructions: instructions} }

// systemOneRequest/Response are the wire format shared by Clef and jev.
type systemOneRequest struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
}

type systemOneResponse struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
}

func decodeAnswers(raw []byte) (map[string]Answer, error) {
	var env struct {
		Result  *systemOneResponse `json:"result"`
		Success *bool              `json:"success"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && env.Result != nil {
		return env.Result.Answers, nil
	} else if err == nil && env.Success != nil && !*env.Success {
		if len(env.Errors) > 0 {
			return nil, errors.New(env.Errors[0].Message)
		}
		return nil, errors.New("decision model call failed")
	}
	var r systemOneResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	if r.Answers == nil {
		return nil, errors.New("decision model returned no answers")
	}
	return r.Answers, nil
}

var httpTimeout = 60 * time.Second
