package sites

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReplayPropagatesMethodHeadersBody(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotCT     string
		gotXFoo   string
		gotBody   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		gotXFoo = r.Header.Get("X-Foo")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		_, _ = w.Write([]byte(`{"hits":[{"id":1},{"id":2}]}`))
	}))
	defer srv.Close()

	res, err := Replay(context.Background(), ReplayInput{
		Method:  "POST",
		URL:     srv.URL + "/api/search",
		Headers: map[string]string{"X-Foo": "bar", "Content-Type": "application/json"},
		Body:    `{"q":"clio"}`,
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/api/search" {
		t.Errorf("method/path mismatch: %s %s", gotMethod, gotPath)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type: got %q", gotCT)
	}
	if gotXFoo != "bar" {
		t.Errorf("X-Foo header lost: %q", gotXFoo)
	}
	if gotBody != `{"q":"clio"}` {
		t.Errorf("body mangled: %q", gotBody)
	}
	if res.Status != 200 {
		t.Errorf("status: got %d", res.Status)
	}
	if res.ItemCount != 2 || res.ItemPath != "hits" {
		t.Errorf("array detect: count=%d path=%q", res.ItemCount, res.ItemPath)
	}
}

func TestReplayDefaultsContentTypeForJSONBody(t *testing.T) {
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	_, err := Replay(context.Background(), ReplayInput{
		Method: "POST",
		URL:    srv.URL,
		Body:   `{"x":1}`,
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if gotCT != "application/json" {
		t.Errorf("expected default JSON CT, got %q", gotCT)
	}
}

func TestReplayStripsNoiseHeaders(t *testing.T) {
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k := range r.Header {
			keys = append(keys, strings.ToLower(k))
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	_, err := Replay(context.Background(), ReplayInput{
		URL: srv.URL,
		Headers: map[string]string{
			":authority":     "evil.example.com",
			"Host":           "evil.example.com",
			"Content-Length": "999",
			"X-Keep":         "yes",
		},
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	for _, k := range keys {
		if k == ":authority" || k == "host" || k == "content-length" {
			t.Errorf("noise header %q should have been stripped", k)
		}
	}
}

func TestReplayTruncatesLargeBodies(t *testing.T) {
	big := strings.Repeat("x", 10_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()

	res, err := Replay(context.Background(), ReplayInput{
		URL:          srv.URL,
		MaxBodyBytes: 512,
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !res.Truncated {
		t.Error("expected Truncated=true")
	}
	if len(res.Body) != 512 {
		t.Errorf("body len: got %d want 512", len(res.Body))
	}
}

func TestMutateAlgoliaParamsOverridesKeys(t *testing.T) {
	original := `{"params":"query=&hitsPerPage=20&filters=brand%3A%22Renault%22"}`
	out, err := MutateAlgoliaParams(original, map[string]string{
		"hitsPerPage": "5",
		"filters":     `brand:"Peugeot"`,
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	var wrap map[string]string
	if err := json.Unmarshal([]byte(out), &wrap); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	params := wrap["params"]
	if !strings.Contains(params, "hitsPerPage=5") {
		t.Errorf("hitsPerPage override missing: %s", params)
	}
	if !strings.Contains(params, "Peugeot") {
		t.Errorf("filters override missing: %s", params)
	}
	if strings.Contains(params, "Renault") {
		t.Errorf("Renault should be replaced: %s", params)
	}
	if !strings.Contains(params, "query=") {
		t.Errorf("untouched query= dropped: %s", params)
	}
}

func TestMutateAlgoliaParamsRejectsNonJSON(t *testing.T) {
	_, err := MutateAlgoliaParams("not json", nil)
	if err == nil {
		t.Fatal("expected error on non-JSON body")
	}
}

func TestFindTopLevelArray(t *testing.T) {
	cases := []struct {
		body      string
		wantPath  string
		wantCount int
	}{
		{`{"hits":[{"a":1},{"a":2},{"a":3}]}`, "hits", 3},
		{`{"data":{"results":[1,2,3,4,5]}}`, "data.results", 5},
		{`{"foo":[1,2],"bar":[1,2,3,4]}`, "bar", 4},
		{`[]`, "", 0},
		{`not json`, "", 0},
		{`{}`, "", 0},
	}
	for _, tc := range cases {
		path, count, _ := findTopLevelArray(tc.body, 1024)
		if path != tc.wantPath || count != tc.wantCount {
			t.Errorf("findTopLevelArray(%q) = (%q, %d) want (%q, %d)", tc.body, path, count, tc.wantPath, tc.wantCount)
		}
	}
}
