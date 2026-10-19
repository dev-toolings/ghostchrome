package engine

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/proto"
)

func TestAuditURLRedactsSensitiveQueryValues(t *testing.T) {
	got := auditURL("http://localhost:8790/api/events?cursor=42&token=abc&apiKey=xyz")
	if strings.Contains(got, "abc") || strings.Contains(got, "xyz") {
		t.Fatalf("auditURL leaked a secret: %s", got)
	}
	if !strings.Contains(got, "cursor=42") {
		t.Fatalf("auditURL dropped a harmless value: %s", got)
	}
	if plain := auditURL("http://localhost:8790/api/bots"); plain != "http://localhost:8790/api/bots" {
		t.Fatalf("auditURL changed a URL without a query: %s", plain)
	}
}

func TestInitiatorFramesKeepsApplicationFramesAcrossAsyncParents(t *testing.T) {
	line := float64(9)
	initiator := &proto.NetworkInitiator{
		Type: proto.NetworkInitiatorTypeScript,
		Stack: &proto.RuntimeStackTrace{
			CallFrames: []*proto.RuntimeCallFrame{
				{FunctionName: "attempt", URL: "http://localhost:1420/src/lib/api.ts?t=1", LineNumber: 450},
				{FunctionName: "fetchFn", URL: "http://localhost:1420/node_modules/.vite/deps/chunk.js", LineNumber: 3},
			},
			Parent: &proto.RuntimeStackTrace{
				CallFrames: []*proto.RuntimeCallFrame{
					{FunctionName: "", URL: "http://localhost:1420/src/screens/conversation/conversation.tsx", LineNumber: 229},
				},
			},
		},
		URL:        "http://localhost:1420/src/lib/api.ts",
		LineNumber: &line,
	}
	got := initiatorFrames(initiator, 4)
	want := []string{
		"attempt /src/lib/api.ts:451",
		"(anonymous) /src/screens/conversation/conversation.tsx:230",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("initiatorFrames = %#v, want %#v", got, want)
	}
	if limited := initiatorFrames(initiator, 1); len(limited) != 1 {
		t.Fatalf("initiatorFrames ignored the depth: %#v", limited)
	}
}

func TestInitiatorFramesFallsBackToTheInitiatorURL(t *testing.T) {
	line := float64(11)
	got := initiatorFrames(&proto.NetworkInitiator{Type: proto.NetworkInitiatorTypeParser, URL: "http://localhost:1420/index.html", LineNumber: &line}, 4)
	if want := []string{"parser /index.html:12"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("initiatorFrames = %#v, want %#v", got, want)
	}
}
