package engine

import (
	"encoding/json"
	"testing"
)

func TestExtractRSCPayloads_SingleChunk(t *testing.T) {
	// Real-world chunk shape: id : JSON, separated by \n inside the
	// JSON-encoded string. The push() arg uses JSON escaping (\n, \").
	html := `<html><body>
<script>self.__next_f.push([1, "0:[\"hello\"]\n2:[{\"id\":42}]\n"])</script>
</body></html>`
	got := ExtractRSCPayloads(html)
	if len(got) != 2 {
		t.Fatalf("want 2, got %d (%+v)", len(got), got)
	}
	if got[0].Type != "0" || got[1].Type != "2" {
		t.Fatalf("ids: %q %q", got[0].Type, got[1].Type)
	}
	if !got[0].IsJSON || !got[1].IsJSON {
		t.Fatalf("isJSON: %v %v", got[0].IsJSON, got[1].IsJSON)
	}
	var v0 []string
	if err := json.Unmarshal(got[0].Data, &v0); err != nil || v0[0] != "hello" {
		t.Fatalf("chunk 0: %v %v", err, v0)
	}
	var v2 []map[string]int
	if err := json.Unmarshal(got[1].Data, &v2); err != nil || v2[0]["id"] != 42 {
		t.Fatalf("chunk 2: %v %v", err, v2)
	}
}

func TestExtractRSCPayloads_MultipleChunks(t *testing.T) {
	html := `<script>self.__next_f.push([1, "1:[\"a\"]\n"])</script>
<script>self.__next_f.push([1, "2:[\"b\"]\n"])</script>
<script>self.__next_f.push([1, "3a:[\"c\"]\n"])</script>`
	got := ExtractRSCPayloads(html)
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	wantIDs := []string{"1", "2", "3a"}
	for i, p := range got {
		if p.Type != wantIDs[i] {
			t.Fatalf("idx %d id=%s want %s", i, p.Type, wantIDs[i])
		}
	}
}

func TestExtractRSCPayloads_ModuleRefAndText(t *testing.T) {
	// I[...] is a module ref (not strict JSON because of the bare identifier
	// on the right). T... is a text token. Both should be returned with
	// IsJSON=false so callers know not to json.Unmarshal them.
	html := `<script>self.__next_f.push([1, "12:I[70852,[\"chunks\"],\"Component\"]\n13:T82a,raw text continuation\n"])</script>`
	got := ExtractRSCPayloads(html)
	if len(got) != 2 {
		t.Fatalf("want 2, got %d (%+v)", len(got), got)
	}
	for _, p := range got {
		if p.IsJSON {
			t.Fatalf("expected IsJSON=false for %q value=%s", p.Type, p.Data)
		}
	}
}

func TestExtractRSCPayloads_None(t *testing.T) {
	html := `<html><body><h1>plain</h1></body></html>`
	if got := ExtractRSCPayloads(html); got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func TestExtractSSRPayloads_IncludesRSC(t *testing.T) {
	html := `<script>self.__next_f.push([1, "5:[\"data\"]\n"])</script>`
	got := ExtractSSRPayloads(html)
	if len(got) != 1 || got[0].Source != SourceRSC {
		t.Fatalf("want 1 rsc payload, got %+v", got)
	}
}
