package engine

import (
	"encoding/json"
	"regexp"
	"strings"
)

// rscPushRE captures the JSON-encoded string argument of a Next.js App
// Router RSC chunk push:
//
//	self.__next_f.push([1, "<chunk text, JSON-escaped>"])
//
// The chunks together form a stream of `<id>:<value>` lines (where id is
// a hex tag and value is JSON or RSC element tree). We grab the raw
// JSON-encoded string and re-decode it via encoding/json so escapes like
// \", \n, \uXXXX are handled correctly.
var rscPushRE = regexp.MustCompile(`(?s)self\.__next_f\.push\(\[\s*1\s*,\s*("(?:[^"\\]|\\.)*")\s*\]\)`)

// rscLineRE matches one `<hexid>:<value>` line within a reconstructed RSC
// stream. The id is hex digits; the value runs until the next newline.
var rscLineRE = regexp.MustCompile(`(?m)^([0-9a-fA-F]+):(.*)$`)

// ExtractRSCPayloads decodes Next.js App Router RSC chunks and returns one
// SSRPayload per logical entry (`<id>:<value>`). Values that parse as JSON
// (object, array, string) are surfaced with IsJSON=true; module refs and
// element trees are returned as raw strings (callers that care can run
// them through V8 — but the JSON ones cover most data needs).
//
// Stream format reference:
//
//	0:["$","html",null,{...}]      RSC element tree
//	1:I[123,[...],"Comp"]          module / chunk reference
//	2:[...listings JSON...]        plain JSON data island
//	3:T82a,Lorem ipsum…            text token
//
// We keep all of them; downstream extractors filter by id or by "starts
// with `[` / `{`" depending on what they need.
func ExtractRSCPayloads(html string) []SSRPayload {
	matches := rscPushRE.FindAllStringSubmatch(html, -1)
	if len(matches) == 0 {
		return nil
	}
	var stream strings.Builder
	for _, m := range matches {
		var s string
		if err := json.Unmarshal([]byte(m[1]), &s); err != nil {
			continue
		}
		stream.WriteString(s)
	}
	if stream.Len() == 0 {
		return nil
	}

	out := []SSRPayload{}
	for _, line := range rscLineRE.FindAllStringSubmatch(stream.String(), -1) {
		id := line[1]
		val := strings.TrimSpace(line[2])
		if val == "" {
			continue
		}
		out = append(out, SSRPayload{
			Source: SourceRSC,
			Data:   asJSONOrString(val),
			IsJSON: looksLikeJSON(val),
			Type:   id,
		})
	}
	return out
}

// asJSONOrString returns a json.RawMessage. When `s` is valid JSON it is
// passed through verbatim; otherwise it is JSON-encoded as a string so the
// containing envelope still marshals cleanly. Required because RSC streams
// mix JSON values, module refs (`I[...]`) and text tokens (`T82a,…`) — we
// keep all three but force the second/third into transportable strings.
func asJSONOrString(s string) json.RawMessage {
	if looksLikeJSON(s) {
		return json.RawMessage(s)
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return encoded
}
