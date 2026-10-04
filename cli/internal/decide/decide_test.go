package decide

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A fake System One endpoint: checks batching (≤64 questions) and decoding.
func TestBatchingAndDecode(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req systemOneRequest
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.Questions) > MaxQuestions {
			http.Error(w, "too many questions", 400)
			return
		}
		ans := map[string]Answer{}
		for id, q := range req.Questions {
			ans[id] = Answer{Type: q.Type, Noul: 0.9}
		}
		json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"model": "clef-flash", "answers": ans}, "success": true})
	}))
	defer srv.Close()
	logged := 0
	d := &Decider{P: &Clef{Model: "clef-flash", AccountID: "acc", Token: "t", BaseURL: srv.URL}, Log: func(string, string, string, Answer) { logged++ }}
	qs := map[string]Question{}
	for i := 0; i < 100; i++ {
		qs[fmt.Sprintf("q%d", i)] = Noul("Is this a data API?")
	}
	ans, err := d.Ask(context.Background(), "req.kind", "state", qs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ans) != 100 || calls != 2 || logged != 100 || !ans["q5"].Yes() || !ans["q5"].Sure() {
		t.Fatalf("answers=%d calls=%d logged=%d", len(ans), calls, logged)
	}
	if (&Decider{}).Available() {
		t.Fatal("empty decider must be unavailable")
	}
}

func TestSure(t *testing.T) {
	if (Answer{Type: "noul", Noul: 0.5}).Sure() || !(Answer{Type: "noul", Noul: 0.1}).Sure() {
		t.Fatal("noul thresholds")
	}
	if (Answer{Type: "choice", Choice: "a", Probabilities: map[string]float64{"a": 0.4}}).Sure() {
		t.Fatal("choice threshold")
	}
}
