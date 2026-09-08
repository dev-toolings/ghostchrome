package engine

import (
	"encoding/json"
	"regexp"
	"strings"
)

// SSRSource classifies the origin of an SSR data payload found in a page's
// HTML. Different frameworks expose their state in different shapes; we
// surface the source so downstream tooling can pick a parser per format.
type SSRSource string

const (
	SourceNextData     SSRSource = "next"          // <script id="__NEXT_DATA__" type="application/json">
	SourceNuxtData     SSRSource = "nuxt"          // <script id="__NUXT_DATA__" type="application/json"> (Nuxt 3)
	SourceNuxtJS       SSRSource = "nuxt-js"       // window.__NUXT__=... (Nuxt 2; raw JS, not JSON)
	SourceApolloState  SSRSource = "apollo"        // window.__APOLLO_STATE__=...
	SourceInitialState SSRSource = "initial-state" // window.__INITIAL_STATE__=...
	SourcePreloaded    SSRSource = "preloaded"     // window.__PRELOADED_STATE__=...
	SourceReduxState   SSRSource = "redux"         // window.__REDUX_STATE__=...
	SourceJSONLD       SSRSource = "jsonld"        // <script type="application/ld+json">
	SourceRSC          SSRSource = "rsc"           // self.__next_f.push([1, "<chunks>"]) (Next.js App Router)
)

// SSRPayload is one structured data island found in HTML. Data is the raw
// payload as it appeared in the page — JSON for the *Data sources and
// JSON-LD, plain JS expression for *State / Nuxt 2. IsJSON tells the caller
// whether they can json.Unmarshal it directly.
type SSRPayload struct {
	Source SSRSource       `json:"source"`
	Data   json.RawMessage `json:"data"`
	IsJSON bool            `json:"is_json"`
	// Type is set for JSON-LD payloads to surface schema.org @type
	// without forcing the caller to re-parse.
	Type string `json:"type,omitempty"`
}

// ExtractSSRPayloads finds every supported SSR data island in the HTML and
// returns them in order of appearance. Sites that ship multiple frameworks
// (e.g. Next + Apollo, or several JSON-LD blocks) get one entry per island.
//
// Always-safe: returns nil on absence rather than an error. Callers decide
// what to do with the empty list (often: fall back to Chrome).
func ExtractSSRPayloads(html string) []SSRPayload {
	var out []SSRPayload

	if d := extractScriptByID(html, "__NEXT_DATA__"); d != nil {
		out = append(out, SSRPayload{Source: SourceNextData, Data: d, IsJSON: true})
	}
	if d := extractScriptByID(html, "__NUXT_DATA__"); d != nil {
		out = append(out, SSRPayload{Source: SourceNuxtData, Data: d, IsJSON: true})
	}
	for _, jsVar := range []struct {
		name string
		src  SSRSource
	}{
		{"__NUXT__", SourceNuxtJS},
		{"__APOLLO_STATE__", SourceApolloState},
		{"__INITIAL_STATE__", SourceInitialState},
		{"__PRELOADED_STATE__", SourcePreloaded},
		{"__REDUX_STATE__", SourceReduxState},
	} {
		if d, isJSON := extractWindowAssignment(html, jsVar.name); d != nil {
			out = append(out, SSRPayload{Source: jsVar.src, Data: d, IsJSON: isJSON})
		}
	}
	out = append(out, extractJSONLD(html)...)
	out = append(out, ExtractRSCPayloads(html)...)
	return out
}

// extractScriptByID returns the body of <script id="<id>"> blocks where the
// id attribute can appear in any order relative to the type attribute. Used
// for Next.js (__NEXT_DATA__) and Nuxt 3 (__NUXT_DATA__) — both serve a
// strict JSON payload.
func extractScriptByID(html, id string) []byte {
	needle := `id="` + id + `"`
	i := strings.Index(html, needle)
	if i < 0 {
		// Try single-quoted variant (rare but legal HTML).
		needle = `id='` + id + `'`
		i = strings.Index(html, needle)
		if i < 0 {
			return nil
		}
	}
	// Walk back to the enclosing <script tag opener so we don't false-match
	// id attributes elsewhere on the page.
	open := strings.LastIndex(html[:i], "<script")
	if open < 0 {
		return nil
	}
	gt := strings.IndexByte(html[i:], '>')
	if gt < 0 {
		return nil
	}
	bodyStart := i + gt + 1
	end := strings.Index(html[bodyStart:], "</script>")
	if end < 0 {
		return nil
	}
	body := strings.TrimSpace(html[bodyStart : bodyStart+end])
	if body == "" {
		return nil
	}
	return []byte(body)
}

// windowAssignmentRE captures the right-hand side of an assignment of the
// form `window.<NAME>=<expr>;` or `window["<NAME>"]=<expr>;` up to either a
// trailing semicolon followed by another statement, or the end of the
// containing script. We keep the regex deliberately loose; the caller
// decides whether the captured expression is JSON or arbitrary JS.
//
// We DO NOT try to parse the JS — bigger sites wrap their state in IIFEs
// like `(function(a,b){return {...}}(...))` which only V8 can evaluate.
var windowAssignmentRE = regexp.MustCompile(`(?s)window(?:\.([A-Z_]+)|\["([A-Z_]+)"\])\s*=\s*(.+?)(?:;\s*(?:window\.|var |let |const |function |</script>)|</script>)`)

// extractWindowAssignment returns the raw RHS of `window.<name> = ...` and a
// flag indicating whether the RHS is "obviously" JSON (starts with `{` or
// `[` and balances). For non-JSON values (Nuxt 2 IIFE), we still return the
// raw expression so consumers can run it through V8 if needed.
func extractWindowAssignment(html, name string) ([]byte, bool) {
	for _, m := range windowAssignmentRE.FindAllStringSubmatch(html, -1) {
		// m[1] is dotted access, m[2] is bracket access — exactly one is set.
		matched := m[1]
		if matched == "" {
			matched = m[2]
		}
		if matched != name {
			continue
		}
		expr := strings.TrimSpace(m[3])
		expr = strings.TrimRight(expr, ";")
		expr = strings.TrimSpace(expr)
		if expr == "" {
			continue
		}
		isJSON := looksLikeJSON(expr)
		// Wrap non-JSON expressions (Nuxt 2 IIFE, function calls) as JSON
		// strings so the containing envelope still marshals cleanly.
		if !isJSON {
			encoded, err := json.Marshal(expr)
			if err == nil {
				return encoded, false
			}
		}
		return []byte(expr), isJSON
	}
	return nil, false
}

func looksLikeJSON(s string) bool {
	if len(s) == 0 {
		return false
	}
	if s[0] != '{' && s[0] != '[' && s[0] != '"' {
		return false
	}
	var dummy interface{}
	return json.Unmarshal([]byte(s), &dummy) == nil
}

// jsonLDRE captures every <script type="application/ld+json"> block. Both
// attribute orderings are common on real-world pages.
var jsonLDRE = regexp.MustCompile(`(?is)<script[^>]+type\s*=\s*["']application/ld\+json["'][^>]*>(.*?)</script>`)

// extractJSONLD returns one SSRPayload per JSON-LD block. We classify by
// looking at the @type field for ergonomics (callers can filter on
// Product / Offer / Vehicle / RealEstateListing without a second pass).
func extractJSONLD(html string) []SSRPayload {
	matches := jsonLDRE.FindAllStringSubmatch(html, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]SSRPayload, 0, len(matches))
	for _, m := range matches {
		body := strings.TrimSpace(m[1])
		if body == "" {
			continue
		}
		// Some pages prefix with HTML comments (<!-- -->). Strip a leading
		// CDATA wrapper too, which YouTube and a handful of CMSes emit.
		body = strings.TrimPrefix(body, "<!--")
		body = strings.TrimSuffix(body, "-->")
		body = strings.TrimSpace(body)
		body = strings.TrimPrefix(body, "<![CDATA[")
		body = strings.TrimSuffix(body, "]]>")
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}

		raw := json.RawMessage(body)
		typeName := jsonLDType(body)
		out = append(out, SSRPayload{
			Source: SourceJSONLD,
			Data:   raw,
			IsJSON: looksLikeJSON(body),
			Type:   typeName,
		})
	}
	return out
}

// jsonLDType extracts schema.org @type from a JSON-LD blob. Handles single
// objects, arrays of objects, and the @graph wrapper. Returns "" when the
// blob is invalid or has no recognisable @type.
func jsonLDType(s string) string {
	var any interface{}
	if err := json.Unmarshal([]byte(s), &any); err != nil {
		return ""
	}
	return firstType(any)
}

func firstType(v interface{}) string {
	switch x := v.(type) {
	case map[string]interface{}:
		if t, ok := x["@type"].(string); ok && t != "" {
			return t
		}
		// @graph (multi-entity JSON-LD) is the canonical wrapper for
		// pages that describe several entities at once.
		if g, ok := x["@graph"]; ok {
			return firstType(g)
		}
	case []interface{}:
		for _, item := range x {
			if t := firstType(item); t != "" {
				return t
			}
		}
	}
	return ""
}
