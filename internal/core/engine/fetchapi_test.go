package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFastFetchAPI_GETJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"hello":"world"}`)
	}))
	defer srv.Close()

	res, err := FastFetchAPI(context.Background(), APIRequest{URL: srv.URL})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Status != 200 || !res.IsJSON {
		t.Fatalf("status=%d isJSON=%v", res.Status, res.IsJSON)
	}
	var v map[string]string
	if err := json.Unmarshal(res.JSON, &v); err != nil || v["hello"] != "world" {
		t.Fatalf("payload mismatch: %+v err=%v", v, err)
	}
}

func TestFastFetchAPI_POSTBodyAndHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method=%s", r.Method)
		}
		if got := r.Header.Get("X-Custom"); got != "yes" {
			t.Errorf("custom header missing, got %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body) // echo
	}))
	defer srv.Close()

	res, err := FastFetchAPI(context.Background(), APIRequest{
		Method:  "POST",
		URL:     srv.URL,
		Headers: map[string]string{"X-Custom": "yes"},
		Body:    []byte(`{"q":"test"}`),
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !res.IsJSON {
		t.Fatalf("expected JSON, got %s", res.BodyText)
	}
	if !strings.Contains(string(res.JSON), `"q":"test"`) {
		t.Fatalf("body not echoed: %s", res.JSON)
	}
}

func TestFastFetchAPI_NonJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "hello plain text")
	}))
	defer srv.Close()

	res, err := FastFetchAPI(context.Background(), APIRequest{URL: srv.URL})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsJSON || res.BodyText != "hello plain text" {
		t.Fatalf("unexpected: isJSON=%v body=%q", res.IsJSON, res.BodyText)
	}
}

func TestAlgoliaQuery_BuildsRequest(t *testing.T) {
	var got struct {
		Path        string
		AppID       string
		APIKey      string
		ContentType string
		Body        string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Path = r.URL.Path
		got.AppID = r.Header.Get("X-Algolia-Application-Id")
		got.APIKey = r.Header.Get("X-Algolia-API-Key")
		got.ContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		got.Body = string(body)
		_, _ = io.WriteString(w, `{"hits":[],"nbHits":0}`)
	}))
	defer srv.Close()

	res, err := AlgoliaQuery(context.Background(), AlgoliaOpts{
		AppID:      "APP",
		APIKey:     "KEY",
		Index:      "idx",
		Endpoint:   srv.URL + "/1/indexes/idx/query",
		ParamsForm: "query=x&hitsPerPage=5",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !res.IsJSON || res.Status != 200 {
		t.Fatalf("status=%d isJSON=%v", res.Status, res.IsJSON)
	}
	if got.Path != "/1/indexes/idx/query" {
		t.Errorf("path: %q", got.Path)
	}
	if got.AppID != "APP" || got.APIKey != "KEY" {
		t.Errorf("auth headers wrong: %+v", got)
	}
	if got.ContentType != "application/json" {
		t.Errorf("content-type: %q", got.ContentType)
	}
	if !strings.Contains(got.Body, `"params":"query=x`) {
		t.Errorf("body: %q", got.Body)
	}
}

func TestAlgoliaQuery_RequiresFields(t *testing.T) {
	if _, err := AlgoliaQuery(context.Background(), AlgoliaOpts{}); err == nil {
		t.Fatal("expected error on empty opts")
	}
}
