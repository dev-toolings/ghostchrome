package engine

import (
	"encoding/json"
	"testing"
)

func TestExtractSSRPayloads_NextData(t *testing.T) {
	html := `<html><body>
<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"x":1}}}</script>
</body></html>`
	got := ExtractSSRPayloads(html)
	if len(got) != 1 {
		t.Fatalf("want 1 payload, got %d", len(got))
	}
	if got[0].Source != SourceNextData || !got[0].IsJSON {
		t.Fatalf("source=%q isJSON=%v", got[0].Source, got[0].IsJSON)
	}
}

func TestExtractSSRPayloads_NuxtJSON(t *testing.T) {
	html := `<script type="application/json" id="__NUXT_DATA__">[1,2,3]</script>`
	got := ExtractSSRPayloads(html)
	if len(got) != 1 || got[0].Source != SourceNuxtData {
		t.Fatalf("want nuxt-data, got %+v", got)
	}
}

func TestExtractSSRPayloads_WindowAssignments(t *testing.T) {
	cases := []struct {
		name string
		html string
		want SSRSource
	}{
		{"apollo", `<script>window.__APOLLO_STATE__={"a":1};</script>`, SourceApolloState},
		{"initial", `<script>window.__INITIAL_STATE__={"foo":"bar"};</script>`, SourceInitialState},
		{"preloaded", `<script>window.__PRELOADED_STATE__={"x":2};</script>`, SourcePreloaded},
		{"redux", `<script>window.__REDUX_STATE__={"r":3};</script>`, SourceReduxState},
		{"nuxt2_iife", `<script>window.__NUXT__=(function(){return {a:1}}());</script>`, SourceNuxtJS},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractSSRPayloads(tc.html)
			if len(got) != 1 {
				t.Fatalf("want 1, got %d (%+v)", len(got), got)
			}
			if got[0].Source != tc.want {
				t.Fatalf("source=%s, want %s", got[0].Source, tc.want)
			}
		})
	}
}

func TestExtractSSRPayloads_JSONLD(t *testing.T) {
	html := `<html>
<script type="application/ld+json">{"@context":"https://schema.org","@type":"Product","name":"Foo"}</script>
<script type='application/ld+json'>{"@context":"https://schema.org","@type":"Vehicle","name":"Bar"}</script>
<script type="application/ld+json">{"@graph":[{"@type":"BreadcrumbList"},{"@type":"Offer","price":1000}]}</script>
</html>`
	got := ExtractSSRPayloads(html)
	if len(got) != 3 {
		t.Fatalf("want 3 jsonld, got %d", len(got))
	}
	wantTypes := []string{"Product", "Vehicle", "BreadcrumbList"}
	for i, p := range got {
		if p.Source != SourceJSONLD {
			t.Fatalf("idx %d source=%s, want jsonld", i, p.Source)
		}
		if p.Type != wantTypes[i] {
			t.Fatalf("idx %d type=%q, want %q", i, p.Type, wantTypes[i])
		}
		var v interface{}
		if err := json.Unmarshal(p.Data, &v); err != nil {
			t.Fatalf("idx %d invalid JSON: %v", i, err)
		}
	}
}

func TestExtractSSRPayloads_None(t *testing.T) {
	html := `<html><body><h1>Plain HTML, no SSR data.</h1></body></html>`
	if got := ExtractSSRPayloads(html); len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}

func TestExtractSSRPayloads_MixedFrameworks(t *testing.T) {
	html := `<html>
<script id="__NEXT_DATA__" type="application/json">{"a":1}</script>
<script>window.__APOLLO_STATE__={"b":2};</script>
<script type="application/ld+json">{"@type":"Product","name":"X"}</script>
</html>`
	got := ExtractSSRPayloads(html)
	if len(got) != 3 {
		t.Fatalf("want 3 payloads, got %d", len(got))
	}
	wantOrder := []SSRSource{SourceNextData, SourceApolloState, SourceJSONLD}
	for i, src := range wantOrder {
		if got[i].Source != src {
			t.Fatalf("order: idx %d=%s, want %s", i, got[i].Source, src)
		}
	}
}

func TestExtractNextDataBackwardCompat(t *testing.T) {
	html := `<script id="__NEXT_DATA__" type="application/json">{"k":"v"}</script>`
	if got := ExtractNextData(html); string(got) != `{"k":"v"}` {
		t.Fatalf("got %q", got)
	}
}
